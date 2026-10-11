package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"teps/core"
)

// 执行形态的调度键。它们与 store/manifest.go 的 Kind 取值逐字相同，
// 但**不 import store**：器官之间不直接调用，技能店是可以整体摘换的。
// 适配器负责把 manifest 投影成 Declaration，映射只发生在那一处。
const (
	KindAlgorithm = "algorithm"
	KindCode      = "code"
	KindScript    = "script"
	KindLLMAPI    = "llm_api"
	KindRLPolicy  = "rl_policy"
	KindHuman     = "human"
)

// Declaration 是 manifest 里「执行器真正需要」的那几个字段的投影。
//
// 投影而不是直接吃 manifest，有两个理由：一是器官不互相 import；
// 二是这个结构体本身就是一份声明——凡是这里没出现的字段，
// 执行器的行为就不许依赖它。想加一个字段，就得先解释它为什么属于「执行判据」。
type Declaration struct {
	// SkillID 只用来回显与署名，不参与任何判定。
	SkillID string
	// Kind 是调度键。执行器只认 script，其余每种的拒绝理由都单独写。
	Kind string
	// Interpreter 是脚本的解释器：绝对路径，或是 PATH 上的名字。
	Interpreter string
	// Files 是 payload 的路径清单（只用来算入口，不读内容）。
	Files []string
	// Assert 逐字来自 manifest 的 verify_assert。判定只许看它。
	Assert []string
	// BudgetMS 是 manifest 声明的预算毫秒数，同时是硬时限。
	BudgetMS int64
	// Input 是调用方给的原始输入 JSON（可为空）。
	Input []byte
	// Raw 表示 Input 已经是送去标准输入的那串字节，不要再当 JSON 规范化。
	//
	// 组合的串行步就靠它：上一步的 stdout 是**任意字节**，不是 API 输入。
	// 拿 JSON 规范化器去处理它（json.Decoder 只读第一个值就停，剩下的当尾随数据忽略）
	// 会把第一个值之后的一切**静默丢掉**——一个缺了尾巴的输入，
	// 会让下游给出一个看起来正常的错答案。这种错最难查，因为它不报错。
	Raw bool
}

// Plan 是「要跑什么」的全部答案。
//
// 它是纯的：同样的 Declaration 永远给同样的 Plan，一个字节都不差。
// 所以同一个 ref（内容哈希）在一次执行里只会对应一条命令，
// 这正是 skill.invoke 钉死 ref 的含义。
type Plan struct {
	SkillID string `json:"skill"`
	// Rules 是这份计划被判定所依据的那组断言的**身份**。
	//
	// 它存在是因为链上记了「哪个核验器判的」（verify_assert），
	// 却没记「拿哪几条断言判的」。技能被重新发布、断言换过之后，
	// 后验仍然把新的运行折进同一个键 —— 于是它报出来的那个数
	// 变成了**两个不同问题的平均值**。
	Rules       string   `json:"rules"`
	Kind        string   `json:"kind"`
	Interpreter string   `json:"interpreter"`
	Entrypoint  string   `json:"entrypoint"`
	Argv        []string `json:"argv"`
	TimeoutMS   int64    `json:"timeout_ms"`
	Assert      []string `json:"verify_assert"`
	StdinBytes  int      `json:"stdin_bytes"`
	// Stdin 是送进子进程标准输入的那串字节，末尾补一个换行。
	// 规范化发生在这里而不是适配器里，是为了让「同样的输入给同样的输出」
	// 连「子进程到底看见哪串字节」都包含在内。
	//
	// Raw 输入（组合的串行步）不做规范化：那是上游的原始字节。
	Stdin []byte `json:"-"`
	// StdinCanonical 说明这串 stdin 是不是被规范化过。
	// 报告出来，是因为「输入被重新排版过」和「输入原样送达」是两件不同的事，
	// 而下游看到的行为会因此不同。
	StdinCanonical bool `json:"stdin_canonical"`
}

// Plan 从声明算出一次执行，或者拒绝。
//
// 这里的所有拒绝都是**执行之前**的拒绝，所以它们不写任何链上记录：
// 没有发生的事不该留下执行史。时间轴上的拒绝（超时）与启动失败不是这里管的，
// 那两件是核验器据原始观测判出来的 Verdict。
func NewPlan(d Declaration) (Plan, *core.Refusal) {
	p := Plan{SkillID: d.SkillID, Kind: d.Kind}

	// 1. 调度键。每一种不支持的 kind 都有自己的理由，不许合成一句
	//    「不支持的类型」——那句话会让人以为换个类型名就能过。
	switch d.Kind {
	case KindScript:
	case KindAlgorithm:
		return p, core.Refuse(core.CodeBadRequest,
			"kind=algorithm has no process to spawn: it is an in-kernel primitive, and running it out of process would not be the same execution")
	case KindCode:
		return p, core.Refuse(core.CodeBadRequest,
			"kind=code needs a build step, and the Skill ABI carries source rather than a binary: this executor does not compile, and will not pretend it ran")
	case KindLLMAPI:
		return p, core.Refuse(core.CodeBadRequest,
			"kind=llm_api needs a network call, and env.network must be false: a skill that fetches at runtime is not reproducible")
	case KindRLPolicy:
		return p, core.Refuse(core.CodeBadRequest,
			"kind=rl_policy needs a policy runtime this executor does not host: without it the only source would be self-report, and self-report is not evidence")
	case KindHuman:
		return p, core.Refuse(core.CodeBadRequest,
			"kind=human has no process to spawn: a step completed by a person is a claim, not a verifiable execution")
	case "":
		return p, core.Refuse(core.CodeBadRequest, "the declaration has no kind: without a dispatch key there is no execution form")
	default:
		return p, core.Refuse(core.CodeBadRequest, "unknown kind "+d.Kind+": this executor does not guess how to run it")
	}

	// 2. 解释器。含空白的一律拒绝：那多半是「解释器 + 参数」被塞进了一个字段，
	//    而接受它就等于自己造一个 shell 出来。这里的拒绝是显式的，不是静默切分。
	if d.Interpreter == "" {
		return p, core.Refuse(core.CodeBadRequest, "env.interpreter is empty: with no interpreter there is no reproducible execution")
	}
	if strings.ContainsAny(d.Interpreter, " \t\n") {
		return p, core.Refuse(core.CodeBadRequest,
			"env.interpreter contains whitespace ("+d.Interpreter+"): it must be one interpreter, not a command line")
	}
	p.Interpreter = d.Interpreter

	// 3. 预算显式（A7）。0 不是「不限时」，是「没声明」，而不限时的执行不许开始。
	if d.BudgetMS <= 0 {
		return p, core.Refuse(core.CodeBadRequest,
			fmt.Sprintf("budget.ms must be positive (it is %d): an execution with no declared deadline is an unbounded one", d.BudgetMS))
	}
	p.TimeoutMS = d.BudgetMS

	// 4. 没有声明，就没有核验。跑一个判不了的技能只会消耗预算，
	//    所以这里直接拒绝，而不是跑完再判一个 unknown。
	if len(d.Assert) == 0 {
		return p, core.Refuse(core.CodeBadRequest,
			"verify_assert is empty: the executor does not run what cannot be judged, because running it once is the whole basis of the judgement")
	}
	p.Assert = append([]string(nil), d.Assert...)
	p.Rules = RuleID(d.Assert)

	// 5. 入口。Skill ABI 里**没有** entrypoint 字段（这是已知缺口），
	//    所以入口必须从 payload 自身推出来，且推导规则要唯一：
	//    恰好一个文件，或者恰好一个 main / main.*；其余一律拒绝并列出候选。
	entry, refusal := pickEntrypoint(d.Files)
	if refusal != nil {
		return p, refusal
	}
	p.Entrypoint = entry
	p.Argv = []string{p.Interpreter, p.Entrypoint}

	// 6. 标准输入。API 输入要规范化；串行步的输入是上游的原始字节，不许碰。
	stdin := d.Input
	if !d.Raw {
		canon, refusal := core.Canonical(d.Input)
		if refusal != nil {
			return p, refusal
		}
		stdin = canon
	}
	p.Stdin = append(append([]byte(nil), stdin...), '\n')
	p.StdinBytes = len(p.Stdin)
	p.StdinCanonical = !d.Raw
	return p, nil
}

// pickEntrypoint 是入口推导的全部规则。它不读文件内容，只看路径清单。
func pickEntrypoint(files []string) (string, *core.Refusal) {
	if len(files) == 0 {
		return "", core.Refuse(core.CodeBadRequest, "the payload is empty: there is nothing to run")
	}
	// 名字先自己校验一遍。盘上格式已经查过，但「先校验再拼接」是这里唯一
	// 一次把外部字符串变成路径的地方，重复一次比漏一次便宜。
	names := make([]string, 0, len(files))
	for _, f := range files {
		if f == "" || path.IsAbs(f) || path.Clean(f) != f || strings.HasPrefix(f, "..") {
			return "", core.Refuse(core.CodeBadRequest, "unsafe payload path: "+f)
		}
		names = append(names, f)
	}
	sort.Strings(names)

	if len(names) == 1 {
		return names[0], nil
	}
	var mains []string
	for _, n := range names {
		base := path.Base(n)
		if base == "main" || strings.HasPrefix(base, "main.") {
			mains = append(mains, n)
		}
	}
	if len(mains) == 1 {
		return mains[0], nil
	}
	return "", core.Refuse(core.CodeBadRequest, fmt.Sprintf(
		"the payload has %d files and no single entrypoint [%s]: the Skill ABI has no entrypoint field, so the rule is exactly one payload file or exactly one main / main.*",
		len(names), strings.Join(names, " ")))
}

// RuleID is the identity of a set of declared assertions.
//
// The order is deliberately **not** part of the identity: every assertion must
// hold, so reordering them cannot change the verdict. An identity should track
// what changes the answer, not what changes the text — otherwise every
// cosmetic edit to a manifest looks like a new rule set, and the signal drowns.
func RuleID(asserts []string) string {
	canon := append([]string(nil), asserts...)
	sort.Strings(canon)
	h := sha256.New()
	for _, a := range canon {
		h.Write([]byte(a))
		// 分隔符是必要的：没有它，["ab","c"] 与 ["a","bc"] 会算出同一个身份。
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
