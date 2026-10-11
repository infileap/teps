package executor

import (
	"strings"
	"testing"

	"teps/core"
	"teps/evidence"
)

// ---------- 核验器：只有声明能判，执行器的自评不算证据 ----------

func TestOkNeedsEveryDeclaredAssertionToHold(t *testing.T) {
	j := Judge([]string{"exit == 0", "stdout contains \"READY\""}, Observed{Exit: 0, Stdout: "READY\n"})
	if j.Verdict != VerdictOK {
		t.Fatalf("want ok, got %s (%s)", j.Verdict, j.Reason)
	}
	if j.VerifiedBy != VerifierName {
		t.Fatalf("a verdict without a verifier is an opinion; got verified_by=%q", j.VerifiedBy)
	}
	if len(j.Checks) != 2 {
		t.Fatalf("every assertion must leave a trace, got %d checks", len(j.Checks))
	}
}

// 这个测试是整件事的核心：执行者自报成功（exit 0），核验器据声明判失败。
func TestSelfReportedSuccessIsNotSuccess(t *testing.T) {
	// 脚本说了「我成功了」（退出码 0），但它没有产出声明里要求的那个词。
	obs := Observed{Exit: 0, Stdout: "FAIL\n"}
	j := Judge([]string{"exit == 0", "stdout contains \"READY\""}, obs)
	if j.Verdict == VerdictOK {
		t.Fatal("exit 0 was treated as evidence: the executor just certified itself")
	}
	if j.Verdict != VerdictFailed {
		t.Fatalf("want failed, got %s (%s)", j.Verdict, j.Reason)
	}
	if !strings.Contains(j.Reason, "declared assertion is false") {
		t.Fatalf("the reason must name what failed, got %q", j.Reason)
	}
	// 两条明细都要在，而且第一条是真、第二条是假：不是笼统的「不行」。
	if len(j.Checks) != 2 || j.Checks[0].Result != ResultTrue || j.Checks[1].Result != ResultFalse {
		t.Fatalf("checks must be per assertion, got %+v", j.Checks)
	}
}

func TestNoAssertionMeansNoVerdict(t *testing.T) {
	// 没有声明就没有核验 —— 而没有核验永远不等于成功。
	if j := Judge(nil, Observed{Exit: 0}); j.Verdict != VerdictUnknown {
		t.Fatalf("no assertions must be unknown, got %s", j.Verdict)
	}
	// 有声明但读不懂，同样只能判「无法判定」。
	j := Judge([]string{"我大概成功了吧"}, Observed{Exit: 0})
	if j.Verdict != VerdictUnknown {
		t.Fatalf("an unreadable assertion must be unknown, got %s (%s)", j.Verdict, j.Reason)
	}
	if j.Checks[0].Result != ResultUnevaluable {
		t.Fatalf("an unreadable assertion must be unevaluable, got %s", j.Checks[0].Result)
	}
}

func TestADefiniteFalseBeatsSomethingUnjudgeable(t *testing.T) {
	// 一条确凿为假 + 一条读不懂 ⇒ failed（有一条被证明了），不是 unknown。
	j := Judge([]string{"exit == 3", "stderr == ?"}, Observed{Exit: 0, Stderr: "x"})
	if j.Verdict != VerdictFailed {
		t.Fatalf("a proven failure is a proven failure, got %s (%s)", j.Verdict, j.Reason)
	}
}

func TestTimeoutOutranksTheExitCode(t *testing.T) {
	obs := Observed{Exit: -1, TimedOut: true, LatencyMS: 301}
	j := Judge([]string{"exit == 0"}, obs)
	if j.Verdict != VerdictTimeout {
		t.Fatalf("a killed process is a timeout, not %s", j.Verdict)
	}
	if len(j.Checks) != 0 {
		t.Fatal("a timeout is a fact about time, not a conclusion about assertions")
	}
}

func TestRefusedIsNotAConsequenceOfTheAssertions(t *testing.T) {
	// 宿主拒绝启动：连一次执行都没发生，断言根本无从谈起。
	obs := Observed{Refused: true, Note: "exec: \"nosuch\": executable file not found", Exit: -1}
	j := Judge([]string{"exit == 0"}, obs)
	if j.Verdict != VerdictRefused {
		t.Fatalf("want refused, got %s", j.Verdict)
	}
	if !strings.Contains(j.Reason, "executable file not found") {
		t.Fatalf("the host's own words must survive into the reason, got %q", j.Reason)
	}
}

func TestMalformedAssertionsNeverEvaluateToTrue(t *testing.T) {
	obs := Observed{Exit: 0, Stdout: "READY", Stderr: ""}
	for _, bad := range []string{
		"", "   ", "exit", "exit 0", "== 0", "exit === 0", "exit = 0",
		"stdout contains READY", "stdout contains \"READY\" junk",
		"stdout == READY", "stdout == \"READY\" junk", "exit == \"0\"", "stdout < 3",
		"stdout contains 3", "nope == 0", "exit <> 0", "latency_ms == \"1\"",
	} {
		j := Judge([]string{bad}, obs)
		if j.Verdict != VerdictUnknown {
			t.Fatalf("assertion %q judged %s; a malformed assertion must be unjudgeable", bad, j.Verdict)
		}
		if j.Checks[0].Result != ResultUnevaluable {
			t.Fatalf("assertion %q produced %s", bad, j.Checks[0].Result)
		}
	}
}

func TestTruncationCannotProveAFailure(t *testing.T) {
	asserts := []string{"stdout contains \"READY\"", "stdout == \"AB\""}

	// 观测完整：找不到就是找不到 ⇒ 确凿为假。
	full := Judge(asserts, Observed{Exit: 0, Stdout: "AB"})
	if full.Verdict != VerdictFailed {
		t.Fatalf("a complete observation must be able to prove a failure, got %s", full.Verdict)
	}

	// 同一份前缀，但告诉核验器后面还有字节：找不到不等于没出现 ⇒ 判不了。
	cut := Judge(asserts, Observed{Exit: 0, Stdout: "AB", StdoutTruncated: true})
	if cut.Verdict != VerdictUnknown {
		t.Fatalf("a truncated observation must not prove a failure, got %s (%s)", cut.Verdict, cut.Reason)
	}

	// 但在截下来的前缀里**找到了**，那是确凿的：整个输出里一定有它。
	found := Judge([]string{"stdout contains \"A\""}, Observed{Exit: 0, Stdout: "AB", StdoutTruncated: true})
	if found.Verdict != VerdictOK {
		t.Fatalf("a needle found inside the prefix is proof, got %s", found.Verdict)
	}
}

func TestEveryNumericOperator(t *testing.T) {
	obs := Observed{Exit: 3, LatencyMS: 10}
	yes := []string{"exit > 2", "exit >= 3", "exit < 4", "exit <= 3", "exit == 3", "exit != 2",
		"latency_ms <= 10", "timed_out == false", "stderr == \"\"", "stdout != \"x\""}
	for _, a := range yes {
		if j := Judge([]string{a}, obs); j.Verdict != VerdictOK {
			t.Fatalf("assertion %q should hold, got %s (%s)", a, j.Verdict, j.Reason)
		}
	}
	no := []string{"exit > 3", "exit >= 4", "exit < 3", "exit <= 2", "exit == 0", "exit != 3",
		"timed_out == true", "stderr != \"\""}
	for _, a := range no {
		if j := Judge([]string{a}, obs); j.Verdict != VerdictFailed {
			t.Fatalf("assertion %q should be false, got %s", a, j.Verdict)
		}
	}
}

func TestUnknownVerdictHasNoChainOutcome(t *testing.T) {
	// 这一条守的是「不许默认成成功」：unknown 不能映射成任何 outcome，
	// 所以它根本写不成 resolution，链上留下的是 open 的 intent。
	if _, ok := VerdictUnknown.Outcome(); ok {
		t.Fatal("unknown must not be writable as a resolution")
	}
	for _, v := range []Verdict{VerdictOK, VerdictFailed, VerdictTimeout, VerdictRefused} {
		out, ok := v.Outcome()
		if !ok || out != string(v) {
			t.Fatalf("verdict %s must map to the chain outcome %q, got %q ok=%v", v, v, out, ok)
		}
	}
	if _, ok := Verdict("degraded").Outcome(); ok {
		t.Fatal("the executor does not produce degraded, and must not pretend to")
	}
}

func TestJudgeIsAPureFunction(t *testing.T) {
	asserts := []string{"exit == 0", "stdout contains \"READY\"", "latency_ms <= 100"}
	obs := Observed{Exit: 0, Stdout: "READY", LatencyMS: 7}
	a, b := Judge(asserts, obs), Judge(asserts, obs)
	if a.Verdict != b.Verdict || a.Reason != b.Reason || len(a.Checks) != len(b.Checks) {
		t.Fatalf("same input must give the same judgement:\n%+v\n%+v", a, b)
	}
	for i := range a.Checks {
		if a.Checks[i] != b.Checks[i] {
			t.Fatalf("check %d differs between two identical runs", i)
		}
	}
}

// ---------- 计划：从声明算出要跑什么，纯的 ----------

func scriptDecl() Declaration {
	return Declaration{
		SkillID:     "runner.ok",
		Kind:        KindScript,
		Interpreter: "/bin/sh",
		Files:       []string{"run.sh"},
		Assert:      []string{"exit == 0"},
		BudgetMS:    2000,
		Input:       []byte(`{"b":1,"a":2}`),
	}
}

func mustPlan(t *testing.T, d Declaration) Plan {
	t.Helper()
	p, refusal := NewPlan(d)
	if refusal != nil {
		t.Fatalf("unexpected refusal: %s (%s)", refusal.Code, refusal.Reason)
	}
	return p
}

func wanted(t *testing.T, d Declaration, needle string) *core.Refusal {
	t.Helper()
	p, refusal := NewPlan(d)
	if refusal == nil {
		t.Fatalf("want a refusal containing %q, got plan %+v", needle, p)
	}
	if !strings.Contains(refusal.Reason, needle) {
		t.Fatalf("refusal reason %q does not mention %q", refusal.Reason, needle)
	}
	if refusal.Code != core.CodeBadRequest {
		t.Fatalf("an unrunnable declaration is a bad_request, got %s", refusal.Code)
	}
	return refusal
}

func TestPlanRefusesEveryKindItCannotRun(t *testing.T) {
	// 每一种不支持的 kind 都有自己的理由：合成一句「不支持」会让人
	// 以为改个类型名就能过，而实际原因是不同的。
	for kind, needle := range map[string]string{
		KindAlgorithm: "no process to spawn",
		KindCode:      "needs a build step",
		KindLLMAPI:    "needs a network call",
		KindRLPolicy:  "policy runtime",
		KindHuman:     "a claim, not a verifiable execution",
		"":            "no kind",
		"bogus":       "unknown kind",
	} {
		d := scriptDecl()
		d.Kind = kind
		wanted(t, d, needle)
	}
}

func TestPlanRefusesAnUnboundedBudget(t *testing.T) {
	// A7：预算在动作之前声明。0 不是「不限时」，是「没声明」。
	for _, ms := range []int64{0, -1} {
		d := scriptDecl()
		d.BudgetMS = ms
		wanted(t, d, "budget.ms must be positive")
	}
}

func TestPlanRefusesWithoutDeclaredAssertions(t *testing.T) {
	d := scriptDecl()
	d.Assert = nil
	wanted(t, d, "verify_assert is empty")
}

func TestPlanRefusesAnInterpreterThatLooksLikeACommandLine(t *testing.T) {
	d := scriptDecl()
	d.Interpreter = "sh -c"
	wanted(t, d, "must be one interpreter, not a command line")
}

func TestPlanDerivesTheEntrypointFromThePayload(t *testing.T) {
	// 恰好一个文件就是入口。
	p := mustPlan(t, scriptDecl())
	if p.Entrypoint != "run.sh" {
		t.Fatalf("a single-file payload is its own entrypoint, got %q", p.Entrypoint)
	}

	// 多文件时只认「恰好一个 main / main.*」。
	d := scriptDecl()
	d.Files = []string{"lib/util.sh", "main.sh", "notes.txt"}
	if p := mustPlan(t, d); p.Entrypoint != "main.sh" {
		t.Fatalf("want main.sh, got %q", p.Entrypoint)
	}

	// 两个 main.* 也一样是歧义。
	d.Files = []string{"a/main.sh", "b/main.go"}
	wanted(t, d, "no single entrypoint")

	// 没有 main.* 时必须拒绝，并且把候选列出来让人能改。
	d.Files = []string{"a.sh", "b.sh"}
	refusal := wanted(t, d, "no single entrypoint")
	if !strings.Contains(refusal.Reason, "a.sh") || !strings.Contains(refusal.Reason, "b.sh") {
		t.Fatalf("the refusal must name the candidates, got %q", refusal.Reason)
	}
}

func TestPlanRefusesAnUnsafePayloadPath(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "a/../../b", ""} {
		d := scriptDecl()
		d.Files = []string{bad}
		wanted(t, d, "unsafe payload path")
	}
}

func TestPlanPinsTheCommandToThePayload(t *testing.T) {
	p := mustPlan(t, scriptDecl())
	if len(p.Argv) != 2 || p.Argv[0] != "/bin/sh" || p.Argv[1] != "run.sh" {
		t.Fatalf("argv must be exactly [interpreter entrypoint], got %q", p.Argv)
	}
	// 输入被规范化成 canonical JSON：同一个输入论写法如何，子进程看见的字节一样。
	if string(p.Stdin) != "{\"a\":2,\"b\":1}\n" {
		t.Fatalf("stdin must be canonical JSON plus a newline, got %q", p.Stdin)
	}
	if p.StdinBytes != len(p.Stdin) {
		t.Fatalf("stdin_bytes %d does not match the bytes on the wire (%d)", p.StdinBytes, len(p.Stdin))
	}
	if p.TimeoutMS != 2000 {
		t.Fatalf("the declared budget is the deadline, got %d", p.TimeoutMS)
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	a := mustPlan(t, scriptDecl())
	d := scriptDecl()
	d.Files = nil // 输入顺序不该影响结果
	d.Files = []string{"run.sh"}
	b := mustPlan(t, d)
	if a.Entrypoint != b.Entrypoint || a.Interpreter != b.Interpreter ||
		a.TimeoutMS != b.TimeoutMS || string(a.Stdin) != string(b.Stdin) {
		t.Fatalf("the same declaration must give the same plan:\n%+v\n%+v", a, b)
	}
}

func TestPlanDoesNotAliasTheDeclaration(t *testing.T) {
	d := scriptDecl()
	d.Assert = []string{"exit == 0"}
	p := mustPlan(t, d)

	// 计划一旦算出来，就是一张不再受调用方影响的图。
	d.Assert[0] = "exit == 99"
	d.Files[0] = "evil.sh"
	if p.Assert[0] != "exit == 0" {
		t.Fatalf("the plan aliases the caller's assertion slice: %q", p.Assert[0])
	}
	if p.Entrypoint != "run.sh" {
		t.Fatalf("the plan aliases the caller's file slice: %q", p.Entrypoint)
	}
}

// ---------- 提级：一次执行只许走一步，E0 → E1 ----------

func TestElevationIsOnlyEverOneStep(t *testing.T) {
	ok := Judgement{Verdict: VerdictOK}
	cases := []struct {
		name     string
		j        Judgement
		declared evidence.Level
		want     evidence.Level
	}{
		{"一次核验 ok 且必败用例确实失败 ⇒ E0 提到 E1", ok, evidence.L0, evidence.L1},
		{"已经是 E1 就不再提", ok, evidence.L1, ""},
		{"E2 不会被执行拉到 E1（那是降级）", ok, evidence.L2, ""},
		{"E4 同样不动", ok, evidence.L4, ""},
		{"没核验 ok 就不提", Judgement{Verdict: VerdictFailed}, evidence.L0, ""},
		{"超时不提", Judgement{Verdict: VerdictTimeout}, evidence.L0, ""},
		{"判不了不提", Judgement{Verdict: VerdictUnknown}, evidence.L0, ""},
		{"未知等级不做任何推断", ok, evidence.Level("E9"), ""},
		{"空等级不做任何推断", ok, evidence.Level(""), ""},
	}
	// 一对「确实被驳倒」的必败用例：这是 E1 需要的另一半证据。
	refuted := []Refutation{{Assert: "exit == 0", Verdict: VerdictFailed, Held: true}}
	for _, c := range cases {
		got := Elevate(c.j, c.declared, refuted)
		if got.Level != c.want {
			t.Fatalf("%s: want level %q, got %q (%s)", c.name, c.want, got.Level, got.Reason)
		}
		if got.Level == "" && got.Reason == "" {
			t.Fatalf("%s: a step that is not taken must say why", c.name)
		}
		if got.Level != "" && !got.Elevated() {
			t.Fatalf("%s: %q must report as elevated", c.name, got.Level)
		}
	}
}

// ---------- 标准输入：API 输入要规范化，串行步的输入不许碰 ----------

func TestCanonicalInputIsRetypesetAndSaysSo(t *testing.T) {
	p := mustPlan(t, scriptDecl())
	if string(p.Stdin) != `{"a":2,"b":1}`+"\n" {
		t.Fatalf("an API input must reach the child canonicalised, got %q", p.Stdin)
	}
	if !p.StdinCanonical {
		t.Fatal("the plan must disclose that it canonicalised the input")
	}
}

// ★ 这一条是「被截断的观测不许用来判假」在**输入侧**的孪生规则。
//
// json.Decoder 只读第一个 JSON 值就停，剩下的当尾随数据忽略。所以拿规范化器
// 去处理上游进程的原始输出，会把第一个值之后的一切**静默丢掉**——
// 下游于是看到一份缺了尾巴的输入，并给出一个看起来正常的错答案。
//
// 这个测试刻意断言「尾巴确实会消失」：它记的是失效模式本身。
// 哪天换掉规范化器，它会红，那时这条规则就可以重写了。
func TestCanonicalisingRawUpstreamOutputWouldSilentlyDropTheTail(t *testing.T) {
	d := scriptDecl()
	d.Input = []byte(`{"n":1}TAIL`)
	got := mustPlan(t, d)
	if string(got.Stdin) != "{\"n\":1}\n" {
		t.Fatalf("this test documents the failure mode; expected the tail to vanish, got %q", got.Stdin)
	}
	if strings.Contains(string(got.Stdin), "TAIL") {
		t.Fatal("if the tail survived here, the failure mode this rule guards against is gone and the rule should be revisited")
	}
}

func TestARawChainedInputIsPassedThroughByteForByte(t *testing.T) {
	d := scriptDecl()
	d.Input = []byte(`{"n":1}TAIL`)
	d.Raw = true
	got := mustPlan(t, d)
	if string(got.Stdin) != "{\"n\":1}TAIL\n" {
		t.Fatalf("a chained step's input is another process's output and must arrive unchanged, got %q", got.Stdin)
	}
	if got.StdinCanonical {
		t.Fatal("a raw input was not canonicalised and the plan must not claim it was")
	}
}

func TestARawInputIsNotValidatedAsJSON(t *testing.T) {
	d := scriptDecl()
	d.Input = []byte("not json at all\n")
	d.Raw = true
	got := mustPlan(t, d)
	if string(got.Stdin) != "not json at all\n\n" {
		t.Fatalf("raw bytes must not be parsed as JSON, got %q", got.Stdin)
	}
}

// ★ 单边证据不是证据。
//
// 一个断言是 `exit == 0`、载荷永远返回 0 的技能，被证明的是「它通过了」，
// **没有被证明的是「它有能力失败」**。而「不会失败」不是任何人想要的性质。
// 所以必败用例是必需的那一半，不是建议的那一半。
func TestOneSidedEvidenceIsNotEvidence(t *testing.T) {
	ok := Judgement{Verdict: VerdictOK}

	// 没声明必败用例 ⇒ 停在 E0，且理由说清缺的是哪一半。
	got := Elevate(ok, evidence.L0, nil)
	if got.Level != "" {
		t.Fatalf("without a must-fail case it must not reach E1, got %q", got.Level)
	}
	if !strings.Contains(got.Reason, "one-sided") {
		t.Fatalf("the refusal must name the shape of the gap, got: %s", got.Reason)
	}

	// ★ 必过通过、必败**也**通过 ⇒ 这不是验证过的技能。
	// 这条断言是关键的那一条：它区分「正确通过」与「总是通过」。
	held := []Refutation{{Assert: "exit == 0", Verdict: VerdictOK, Held: false}}
	got = Elevate(ok, evidence.L0, held)
	if got.Level != "" {
		t.Fatalf("a must-fail case that passed anyway must block E1, got %q", got.Level)
	}
	if !strings.Contains(got.Reason, "does not discriminate") {
		t.Fatalf("the refusal must say the assertion does not discriminate, got: %s", got.Reason)
	}

	// ★ 而 timeout / refused / unknown **不算**被驳倒。
	// 它们不是「这条断言被证明为假」：超时是关于时间的事实，refused 说明进程
	// 根本没起来，unknown 是判不了。把它们算作驳倒，就是让一个坏掉的技能
	// 靠「跑不起来」拿到双边的证据。
	for _, v := range []Verdict{VerdictTimeout, VerdictRefused, VerdictUnknown} {
		got := Elevate(ok, evidence.L0, []Refutation{{Assert: "exit == 0", Verdict: v, Held: false}})
		if got.Level != "" {
			t.Fatalf("%s is not a refutation, but it elevated to %q", v, got.Level)
		}
	}

	// 拒绝时也要把必败用例的结果报出来 —— 「我们试过驳倒它，没驳倒」
	// 是关于一个技能最有用的那一句话。
	got = Elevate(ok, evidence.L0, held)
	if len(got.Refutations) != 1 {
		t.Fatalf("the refutation outcome must survive the refusal, got %d", len(got.Refutations))
	}
}

// NeedsRefutation 必须与 Elevate 自己的条件顺序一致 ——
// 它存在的唯一理由就是让调用方不必再写一遍那个顺序。
func TestNeedsRefutationAgreesWithElevate(t *testing.T) {
	ok := Judgement{Verdict: VerdictOK}
	refuted := []Refutation{{Assert: "exit == 0", Verdict: VerdictFailed, Held: true}}
	for _, j := range []Judgement{ok, {Verdict: VerdictFailed}, {Verdict: VerdictUnknown}} {
		for _, lv := range []evidence.Level{evidence.L0, evidence.L1, evidence.L4, evidence.Level("E9")} {
			need := NeedsRefutation(j, lv)
			// 若 Elevate 在给了必败用例时会提级，而 NeedsRefutation 说不用跑，
			// 那就是调用方会漏掉一次本该发生的执行。
			got := Elevate(j, lv, refuted)
			if got.Elevated() && !need {
				t.Fatalf("Elevate elevates %s/%s but NeedsRefutation said not to bother", j.Verdict, lv)
			}
			// 反过来：说需要跑，却给了用例也提不了级 ⇒ 白跑一次。
			if need && !got.Elevated() {
				t.Fatalf("NeedsRefutation said to run for %s/%s but it cannot elevate", j.Verdict, lv)
			}
		}
	}
}

// ★ 「探针沉默」和「拿到但是空」是两件事。
//
// 前者是「这个进程根本没有跑起来」，后者是「它跑了，什么都没产出」。两者的
// stdout 都是空字符串 —— 一个只读 stdout 的读者看到的完全一样。分得开它们的
// 只有判定结果本身，所以判定结果必须不同。
func TestASilentProbeIsNotAnEmptyResult(t *testing.T) {
	silent := Judge([]string{`stdout == ""`}, Observed{Refused: true, Note: "no interpreter"})
	empty := Judge([]string{`stdout == ""`}, Observed{Exit: 0, Stdout: ""})

	if silent.Verdict != VerdictRefused {
		t.Fatalf("a process that never started is not an execution result, got %q", silent.Verdict)
	}
	if empty.Verdict != VerdictOK {
		t.Fatalf("「它跑了，什么都没打印」是一个成立的声明，got %q", empty.Verdict)
	}
	if silent.Verdict == empty.Verdict {
		t.Fatal("the two facts must not share an answer: their stdout is identical and the verdict is all a reader has")
	}
	// 退出码也必须不同：沉默是被信号杀掉的 -1，空是 0。
	silentObs := Observed{Refused: true, Exit: -1}
	emptyObs := Observed{Exit: 0}
	if silentObs.Exit != -1 || emptyObs.Exit != 0 {
		t.Fatalf("the exit codes must differ too: silent=%d empty=%d", silentObs.Exit, emptyObs.Exit)
	}
}

// ★ 一条不可能失败的断言不是一条检查。
//
// `stdout contains ""` 在**任何** stdout 上都是 true，包括一个什么都不打印的
// 进程。它把「拿到了但是空的」读成「东西在那里」，而那条技能因此拿到 E1。
// 判不了，不是判真 —— 这是 unknown 那一档存在的同一个理由。
func TestAnAssertionThatCannotFailIsNotACheck(t *testing.T) {
	j := Judge([]string{`stdout contains ""`}, Observed{Exit: 0, Stdout: ""})
	if j.Verdict == VerdictOK {
		t.Fatal("a needle that matches everything must not produce a pass")
	}
	if j.Verdict != VerdictUnknown {
		t.Fatalf("it is unevaluable, not false: got %q", j.Verdict)
	}
	if !strings.Contains(j.Reason, "asserts nothing") {
		t.Fatalf("the reason must say what is wrong with the assertion, got %q", j.Reason)
	}
	// 对照：非空针在一个空的 stdout 上确实是假 —— 那条断言是有内容的。
	if got := Judge([]string{`stdout contains "X"`}, Observed{Exit: 0, Stdout: ""}).Verdict; got != VerdictFailed {
		t.Fatalf("a non-empty needle that is absent is false, got %q", got)
	}
}

// 对照：`== ""` 是合法的 —— 它说的正是「它什么都没打印」。
// 这就是为什么只有 contains 的空针是空洞的。
func TestAPrintedNothingClaimIsLegitimate(t *testing.T) {
	if got := Judge([]string{`stdout == ""`}, Observed{Exit: 0, Stdout: ""}).Verdict; got != VerdictOK {
		t.Fatalf("claiming the output is empty is a real claim, got %q", got)
	}
	if got := Judge([]string{`stdout != ""`}, Observed{Exit: 0, Stdout: ""}).Verdict; got != VerdictFailed {
		t.Fatalf("and it can be false, got %q", got)
	}
}

// 一条空洞的必败用例不是被驳倒 —— 它是判不了。
// `Held` 要求 `VerdictFailed`，所以 `contains ""` 不能给一个技能双边证据。
func TestAnEmptyNeedleRefutationIsNotARefutation(t *testing.T) {
	j := Judge([]string{`stdout contains ""`}, Observed{Exit: 0, Stdout: ""})
	if j.Verdict == VerdictFailed {
		t.Fatal("unevaluable is not refuted")
	}
	e := Elevate(j, evidence.L0, []Refutation{{Assert: `stdout contains ""`, Verdict: j.Verdict, Held: j.Verdict == VerdictFailed}})
	if e.Elevated() {
		t.Fatal("a refutation that cannot fail must not buy two-sided evidence")
	}
	// 它停在**前一道**门上：判不了的运行不产生证据，所以根本走不到「这条必败
	// 用例分不分辨」那一步。理由要说的是这件事，不是另一件。
	if !strings.Contains(e.Reason, "unknown, not ok") {
		t.Fatalf("an unevaluable run must be refused at the verdict gate, got %q", e.Reason)
	}

	// 走到「分不分辨」那一步的是另一种：必败用例的断言**仍然成立**。
	j2 := Judge([]string{`stdout == ""`}, Observed{Exit: 0, Stdout: ""})
	if j2.Verdict != VerdictOK {
		t.Fatalf("the main run should hold, got %q", j2.Verdict)
	}
	e2 := Elevate(j2, evidence.L0, []Refutation{{Assert: `exit == 0`, Verdict: VerdictOK, Held: false}})
	if e2.Elevated() {
		t.Fatal("a refutation that still holds must not buy two-sided evidence")
	}
	if !strings.Contains(e2.Reason, "does not discriminate") {
		t.Fatalf("the reason must say the case does not discriminate, got %q", e2.Reason)
	}
}
