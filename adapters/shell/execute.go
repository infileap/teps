package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"sansheng/core"
	"sansheng/organs/executor"
	"sansheng/store"
)

// skill.invoke：把一个已注册的技能**真的跑起来**。
//
// 这条 op 的写法是「一半纯、一半脏」：
//   - 要跑什么（executor.NewPlan）与算不算成功（executor.Judge）在 organs/executor 里，是纯的；
//   - spawn、计时、收字节、落盘在这里，是脏的。
//
// 判定权不在本文件里。本文件从头到尾没有任何一处能说「ok」：
// 它只把原始观测交给核验器，再把核验器给的 Verdict 原样翻译成链上的 outcome。
//
// 链上写的是**两条**执行记录（intent 先写、resolution 后写，用 attempt 串起来），
// 而且两条都走 arbiter.observe 那条既有通道 —— 不是另开一条记录流。
// 于是「没有 intent 的 resolution 被拒」「verified_by 为空被拒」这两条守卫，
// 对真实执行和对 curl 手写的作用一模一样。

const (
	// maxCapture 是每个输出流最多留住的字节数。超出的丢掉，但**记住丢过**。
	maxCapture = 64 << 10
	// 幂等键的后缀：一次逻辑尝试 = 两条记录，用同一把 attempt 串起来。
	keyIntent     = ":intent"
	keyResolution = ":resolution"
	// 默认策略名。策略不是 attempt 的键，只是给人读的，所以允许调用方改。
	strategyInvoke = "skill.invoke"
)

// invokeBody 是 skill.invoke 的请求体。
type invokeBody struct {
	// Ref 是内容哈希。跑什么由它钉死，不由调用方挑文件。
	Ref string `json:"ref"`
	// Attempt 是一次逻辑尝试的 id（契约 A3：键标识一次尝试，不标识一次网络请求）。
	Attempt string `json:"attempt"`
	// TaskClass 是后验的分组键之一。没有它，这次执行无法与别的执行比较。
	TaskClass string `json:"task_class"`
	// Input 是调用方给的输入参数，会以 canonical JSON 写进子进程的标准输入。
	Input json.RawMessage `json:"input"`
	// Strategy 覆盖链上记的策略名，缺省 skill.invoke。
	Strategy string `json:"strategy"`
	// Elevate 必须显式给 true 才会尝试把证据从 E0 提到 E1：
	// 提级是一次**写**（要用同一份载荷再注册一次），
	// 而写不能有默认值 —— 这与 skill.promote 要求显式 name/from/to 是同一条纪律。
	Elevate bool `json:"elevate"`
}

// chainRef 是链上一条记录的位置。
type chainRef struct {
	Seq  uint64 `json:"seq"`
	Head string `json:"head"`
}

// elevationBody 报告「这次执行对证据等级做了什么」。
type elevationBody struct {
	Level   string        `json:"level"`
	From    string        `json:"from,omitempty"`
	To      string        `json:"to,omitempty"`
	Ref     string        `json:"ref,omitempty"`
	Reason  string        `json:"reason"`
	Refused *core.Refusal `json:"refused,omitempty"`
}

// invokeResult 是 skill.invoke 的响应体。
//
// 它有四个字段是刻意的：
//   - outcome 与 verified_by 同时出现 —— 结论和它的署名一起给，才叫证据；
//   - intent 与 resolution 两个位置 —— 只有 resolution 为 null 时才说明「没结论」；
//   - observed 是核验器看到的**原始事实**，让人能自己复核判定；
//   - checks 是逐条断言的判定结果，含判不了的与为什么判不了。
type invokeResult struct {
	Attempt    string             `json:"attempt"`
	Skill      string             `json:"skill"`
	TaskClass  string             `json:"task_class"`
	Ref        string             `json:"ref"`
	Kind       string             `json:"kind"`
	Replayed   bool               `json:"replayed"`
	Intent     *chainRef          `json:"intent"`
	Resolution *chainRef          `json:"resolution"`
	Outcome    string             `json:"outcome"`
	VerifiedBy string             `json:"verified_by,omitempty"`
	Reason     string             `json:"reason"`
	LatencyMS  int64              `json:"latency_ms"`
	Observed   *executor.Observed `json:"observed,omitempty"`
	Plan       *executor.Plan     `json:"plan,omitempty"`
	Checks     []executor.Check   `json:"checks,omitempty"`
	Elevation  *elevationBody     `json:"elevation,omitempty"`
	Open       int                `json:"open"`
}

// observeBody 是 arbiter.observe 的请求形状。intent 就是 outcome 为空的那一次。
type observeBody struct {
	Attempt   string  `json:"attempt"`
	Skill     string  `json:"skill"`
	TaskClass string  `json:"task_class"`
	Strategy  string  `json:"strategy"`
	Outcome   string  `json:"outcome"`
	Verified  string  `json:"verified_by"`
	Latency   float64 `json:"latency"`
	Cost      float64 `json:"cost"`
}

// handleExecute 认领 skill.invoke。
func (s *server) handleExecute(f core.Frame, out *core.Frame) bool {
	if f.T != "skill.invoke" {
		return false
	}
	if s.skills == nil {
		out.Err = core.Refuse(core.CodeBadRequest, "no skill store is attached")
		return true
	}
	var req invokeBody
	if refusal := decodeBody(f.Body, &req); refusal != nil {
		out.Err = refusal
		return true
	}
	if req.Attempt == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"an invocation needs an attempt id: without it the execution can never be resolved")
		return true
	}
	if req.Ref == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"an invocation needs a ref: the content hash is what pins what runs")
		return true
	}
	if req.TaskClass == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"an invocation needs a task_class: an execution with no task class cannot be priced or compared")
		return true
	}

	// 同一个 attempt 已经在链上：回放既有结论，绝不再跑第二遍。
	// 这是契约 A3 那条「超时之后重发＝新 attempt」的反面 ——
	// 客户端要是复用了同一把键，跑第二遍就是两次效果藏在一次尝试里。
	if intent, resolution, refusal := s.lookupAttempt(req.Attempt); refusal != nil {
		out.Err = refusal
		return true
	} else if intent != nil {
		if resolution == nil {
			out.Err = core.Refuse(core.CodeBadRequest,
				"attempt "+req.Attempt+" has an intent on the chain and no resolution (the contract calls that unknown): adjudicate it explicitly, then use a new attempt id")
			return true
		}
		res := s.replay(req, intent, resolution)
		out.Seq, out.Rev = resolution.Seq, resolution.Seq
		out.Body = mustJSON(res)
		return true
	}

	pkg, m, refusal := s.skills.Resolve(req.Ref)
	if refusal != nil {
		out.Err = refusal
		return true
	}

	names := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	plan, refusal := executor.NewPlan(executor.Declaration{
		SkillID:     m.SkillID,
		Kind:        string(m.Kind),
		Interpreter: m.Env.Interpreter,
		Files:       names,
		Assert:      m.Verify,
		BudgetMS:    m.Budget.MS,
		Input:       req.Input,
	})
	if refusal != nil {
		out.Err = refusal
		return true
	}

	// A7：预算在动作之前声明。执行前就按 manifest 声明的预算扣账，
	// 超时不退还 —— 时间是花掉了的，退还是另一种记账方式，不是这一条。
	if refusal := s.acct.Spend(core.Budget{MS: plan.TimeoutMS}); refusal != nil {
		out.Err = refusal
		return true
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = strategyInvoke
	}

	// intent 先上链。哪怕下面这一步把进程跑崩、或者根本回不来，
	// 链上留下的是「尝试过、没有结论」，而不是一处静悄悄的空白。
	seq, head, open, refusal := s.observe(req.Attempt+keyIntent, observeBody{
		Attempt: req.Attempt, Skill: m.SkillID, TaskClass: req.TaskClass, Strategy: strategy,
	})
	if refusal != nil {
		out.Err = refusal
		return true
	}

	obs := s.runSandbox(plan, pkg)
	j := executor.Judge(plan.Assert, obs)

	res := &invokeResult{
		Attempt: req.Attempt, Skill: m.SkillID, TaskClass: req.TaskClass,
		Ref: req.Ref, Kind: string(m.Kind),
		Intent:     &chainRef{Seq: seq, Head: head},
		Outcome:    string(j.Verdict),
		VerifiedBy: j.VerifiedBy,
		Reason:     j.Reason,
		LatencyMS:  obs.LatencyMS,
		Observed:   &obs,
		Plan:       &plan,
		Checks:     j.Checks,
		Open:       open,
	}

	if outcome, write := j.Verdict.Outcome(); write {
		rseq, rhead, ropen, refusal := s.observe(req.Attempt+keyResolution, observeBody{
			Attempt: req.Attempt, Skill: m.SkillID, TaskClass: req.TaskClass, Strategy: strategy,
			Outcome: outcome, Verified: j.VerifiedBy, Latency: float64(obs.LatencyMS),
		})
		if refusal != nil {
			out.Err = refusal
			return true
		}
		res.Resolution = &chainRef{Seq: rseq, Head: rhead}
		res.Open = ropen
		out.Seq, out.Rev = rseq, rseq
	} else {
		// 判不了 ⇒ 不写 resolution。链上留着一条始终 open 的 intent，
		// 而契约对这个形状的读法就是 unknown：
		// 「intent 之后没有同名 resolution ⇒ outcome = unknown，必须显式裁决，不许默认成 ok」。
		res.Reason += "；this attempt is left open on the chain on purpose: an open intent IS the honest third state, and writing any of the five outcomes here would be a lie"
		out.Seq, out.Rev = seq, seq
	}

	if !req.Elevate {
		res.Elevation = &elevationBody{
			Reason: "no elevation was requested: a stronger evidence claim is a write, and writes are asked for explicitly (elevate: true)",
		}
	} else if e := executor.Elevate(j, m.Evidence); !e.Elevated() {
		res.Elevation = &elevationBody{Reason: e.Reason}
	} else {
		res.Elevation = s.elevate(m, pkg, e, req.Attempt)
	}

	out.Body = mustJSON(res)
	return true
}

// lookupAttempt 一次日志回放，找出某个 attempt 的 intent 与 resolution。
//
// 回放而不是另建一张表：链是真相，内存里的表只是缓存（契约 §8），
// 而这条判断一旦错了就是「同一个 attempt 跑了两次」，宁可信盘。
func (s *server) lookupAttempt(attempt string) (*core.Entry, *core.Entry, *core.Refusal) {
	var intent, resolution *core.Entry
	refusal := s.log.Replay(func(e core.Entry) error {
		entry := e
		switch entry.Key {
		case attempt + keyIntent:
			intent = &entry
		case attempt + keyResolution:
			resolution = &entry
		}
		return nil
	})
	if refusal != nil {
		return nil, nil, refusal
	}
	return intent, resolution, nil
}

// replay 把链上已经有的结论重新渲染成响应。
//
// 注意它做不了的一件事：执行记录里只有一个布尔 OK，没有五态 outcome，
// 所以回放只能给出 ok / failed。这是复用既有记录形状的代价，不是这里的选择。
func (s *server) replay(req invokeBody, intent, resolution *core.Entry) *invokeResult {
	var rec executionRecord
	outcome := "unknown"
	reason := "this attempt is already on the chain: its resolution is replayed and the skill is NOT run a second time"
	if err := json.Unmarshal(resolution.Body, &rec); err != nil {
		reason = "the stored resolution could not be read: " + err.Error()
	} else if rec.OK == nil {
		// 不该发生：resolution 的 OK 一定非 nil。真发生了就得说出来。
		reason = "the stored resolution has no ok field: the record is not a resolution"
	} else if *rec.OK {
		outcome = "ok"
	} else {
		outcome = "failed"
	}
	return &invokeResult{
		Attempt: req.Attempt, Skill: rec.Skill, TaskClass: rec.TaskClass, Ref: req.Ref,
		Replayed:   true,
		Intent:     &chainRef{Seq: intent.Seq, Head: intent.Head},
		Resolution: &chainRef{Seq: resolution.Seq, Head: resolution.Head},
		Outcome:    outcome,
		VerifiedBy: rec.Verified,
		Reason:     reason,
		LatencyMS:  int64(rec.Latency),
	}
}

// observe 走 arbiter.observe 那条既有通道写一条执行记录。
func (s *server) observe(key string, b observeBody) (uint64, string, int, *core.Refusal) {
	raw, err := json.Marshal(b)
	if err != nil {
		return 0, "", 0, core.Refuse(core.CodeBadRequest, "the observation could not be rendered")
	}
	var out core.Frame
	s.arbiterObserve(core.Frame{T: "arbiter.observe", V: core.ProtocolVersion, Key: key, Body: raw}, &out)
	if out.Err != nil {
		return 0, "", 0, out.Err
	}
	var resp struct {
		Head string `json:"head"`
		Open int    `json:"open"`
	}
	if err := json.Unmarshal(out.Body, &resp); err != nil {
		return out.Seq, "", 0, core.Refuse(core.CodeBadRequest, "the observation response could not be read")
	}
	return out.Seq, resp.Head, resp.Open, nil
}

// elevate 把「一次成功的真实执行」落成一条更强的证据声明。
//
// 做法不是改盘上的东西（manifest 是内容寻址的，改一个字节就换了 ref，
// 而「改一改就当提级」正是本项目反复付学费的那类静默错），而是用**同一份载荷**
// 再注册一次，只把 evidence 从 E0 改成 E1，再把 canary 指针移到新的 ref 上。于是：
//
//   - 原来那个 E0 的 ref 原样留在历史里，没有被就地「解释」成 E1；
//   - store/register.go 里「E0 不许 promote 到 stable」那道门一行都没动，
//     它仍然拦得住任何没有真实执行背书的技能；
//   - 提级的依据就是链上那两条执行记录，attempt 是它们的串联键。
//
// 这里**只**允许走到 E1。E2 要第二个独立来源，E3/E4 要复核与生产运行，
// 一次本地执行提供不了其中任何一样。
func (s *server) elevate(m *store.Manifest, pkg *store.Package, e executor.Elevation, attempt string) *elevationBody {
	files := make(map[string][]byte, len(pkg.Files))
	for name, hash := range pkg.Files {
		body, err := s.skills.CAS.Get(hash)
		if err != nil {
			return &elevationBody{Reason: "the payload could not be re-read for elevation: " + err.Error()}
		}
		files[name] = body
	}
	next := *m
	next.Evidence = e.Level
	res, refusal := s.skills.Register(store.Registration{Manifest: &next, Files: files})
	if refusal != nil {
		// 执行本身已经核验通过了，它的 resolution 也已经上链了。
		// 提级失败不该把这次执行说成失败 —— 那不是事实。
		return &elevationBody{
			Reason:  "the execution verified ok, but the elevated claim was refused: " + refusal.Reason,
			Refused: refusal,
		}
	}
	return &elevationBody{
		Level: string(e.Level),
		From:  string(m.Evidence),
		To:    string(next.Evidence),
		Ref:   res.Ref,
		Reason: e.Reason + ": the same payload was registered again as " + res.Ref +
			" and canary moved to it; the justification is the execution pair keyed by attempt " + attempt,
	}
}

// runSandbox 是这一整条路上唯一真正产生副作用的地方。
//
// 它的输出**只有原始事实**：退出码、是否超时、收到多少字节、花了多久。
// 它不返回任何成功/失败的判断 —— 那个判断是 executor.Judge 的事。
//
// 「沙箱」在这里的确切含义（不夸大）：一次性临时目录 + 擦干净的环境变量 +
// 硬时限（到点杀整个进程组）+ 输出上限 + 载荷只读。它**不含**内核级隔离：
// 没有 namespace、没有 seccomp、没有网络隔断，所以它挡不住一个存心作恶的脚本
// （env.network=false 是 store 拦的声明，不是这里拦的能力）。
func (s *server) runSandbox(plan executor.Plan, pkg *store.Package) executor.Observed {
	obs := executor.Observed{Exit: -1}
	refuse := func(format string, args ...any) executor.Observed {
		obs.Refused = true
		obs.Note = fmt.Sprintf(format, args...)
		return obs
	}

	root, err := os.MkdirTemp("", "sansheng-exec-")
	if err != nil {
		return refuse("no sandbox directory could be created: %v", err)
	}
	defer os.RemoveAll(root)

	payload := filepath.Join(root, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		return refuse("the sandbox payload directory could not be created: %v", err)
	}
	for name, hash := range pkg.Files {
		body, err := s.skills.CAS.Get(hash)
		if err != nil {
			return refuse("payload file %s could not be read from the CAS: %v", name, err)
		}
		dst := filepath.Join(payload, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return refuse("payload directory for %s could not be created: %v", name, err)
		}
		// 只读落盘：技能不该在执行过程中改自己的载荷，那会让「跑的是这个 ref」
		// 变成一句没法复核的话。
		if err := os.WriteFile(dst, body, 0o444); err != nil {
			return refuse("payload file %s could not be written into the sandbox: %v", name, err)
		}
	}

	// 标准输入落成文件而不是管道：os.File 直接交给子进程，少一个挂在 Wait 上的
	// 拷贝 goroutine，也就少一个「子进程不读 stdin 就永远不结束」的坑。
	stdinPath := filepath.Join(root, "stdin.json")
	if err := os.WriteFile(stdinPath, plan.Stdin, 0o444); err != nil {
		return refuse("the sandbox stdin file could not be written: %v", err)
	}
	in, err := os.Open(stdinPath)
	if err != nil {
		return refuse("the sandbox stdin file could not be opened: %v", err)
	}
	defer in.Close()

	interpreter, refusal := resolveInterpreter(plan.Interpreter)
	if refusal != nil {
		return refuse("%s", refusal.Reason)
	}

	cmd := exec.Command(interpreter, plan.Argv[1:]...)
	cmd.Dir = payload
	cmd.Env = []string{
		"PATH=/usr/bin:/bin", "HOME=" + root, "TMPDIR=" + root,
		"LANG=C", "LC_ALL=C",
	}
	cmd.Stdin = in
	stdout := &capBuffer{limit: maxCapture}
	stderr := &capBuffer{limit: maxCapture}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// 自成进程组：到点要杀的是**整棵**进程树，不只是挂名的那个解释器。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return refuse("the host refused to start the process: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var werr error
	select {
	case werr = <-done:
	case <-time.After(time.Duration(plan.TimeoutMS) * time.Millisecond):
		obs.TimedOut = true
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		werr = <-done
	}
	obs.LatencyMS = time.Since(start).Milliseconds()
	if !obs.TimedOut {
		obs.Exit = exitCode(werr)
	}
	obs.Stdout, obs.StdoutTruncated = stdout.String(), stdout.truncated
	obs.Stderr, obs.StderrTruncated = stderr.String(), stderr.truncated
	return obs
}

// resolveInterpreter 把解释器解析成一条具体路径。
//
// exec.Command 对不含斜杠的名字会用**服务端自己**的 PATH 去找，
// 那样同一个 ref 在不同机器上跑的就是不同的解释器。解析一次、钉死它，
// 失败就明说，而不是回落到别的什么东西上。
func resolveInterpreter(name string) (string, *core.Refusal) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	full, err := exec.LookPath(name)
	if err != nil {
		return "", core.Refuse(core.CodeBadRequest, "the interpreter is not on PATH: "+name)
	}
	return full, nil
}

// exitCode 把 Wait 的错误翻成退出码：-1 表示被信号杀掉（含我们自己的超时杀）。
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// capBuffer 收到超过上限的字节就丢掉，但记住自己丢过。
//
// 「被截断」这个事实一路传到核验器，让它不许拿一份不完整的观测去判假：
// 没找到 READY 可能只是因为后面还有字节没留住。
type capBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	room := c.limit - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

func (c *capBuffer) String() string { return c.buf.String() }
