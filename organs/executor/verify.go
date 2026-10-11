// Package executor 是「执行」这件事的纯半边。
//
// 它回答两个问题，也只回答这两个：
//
//  1. 一次已注册技能到底要跑什么（Plan）；
//  2. 跑完之后，按 manifest 声明的 verify.assert，这一次算成功还是失败（Judge）。
//
// 这里没有也不许有 I/O：不 spawn 进程、不读文件、不读时钟。
// 「执行者不许自证」在本包里是一条**类型上的**纪律：Judge 只吃两个东西——
// 声明里的断言，和原始观测（退出码、被截断的字节、是否超时）。
// 它拿不到任何「我成功了」的标志位，所以执行器的自评在类型上就进不来。
//
// 它同时只依赖 core 与 evidence，不认识技能店（store）与适配器：
// 器官之间不直接调用，技能店整个换掉，本包一行都不用改。
package executor

import (
	"fmt"
	"strconv"
	"strings"

	"teps/evidence"
)

// Verdict 是一次执行在**核验之后**的定性结论。
//
// 五态里的 unknown 不是一个「还没算出来」的占位，而是一个终态：
// 「判不了」就是判不了，它不是 ok 的弱化版。把它并进 ok 会制造假成功，
// 并进 failed 会冤枉一次可能真的成功的执行——两者都是撒谎。
type Verdict string

const (
	// VerdictOK 是声明里的每一条断言都成立。
	VerdictOK Verdict = "ok"
	// VerdictFailed 是至少有一条断言确凿为假。
	VerdictFailed Verdict = "failed"
	// VerdictTimeout 是进程没在声明的预算内结束，已被杀。
	VerdictTimeout Verdict = "timeout"
	// VerdictRefused 是宿主拒绝启动进程（解释器不存在等），执行根本没发生。
	VerdictRefused Verdict = "refused"
	// VerdictUnknown 判不了：没有断言、断言无法求值、或观测被截断到无法下定论。
	VerdictUnknown Verdict = "unknown"
)

// VerifierName 是核验器在链上的署名。
//
// 执行记录里的 verified_by 必须非空（decision.go 拦得住空署名），
// 而且这个名字必须指的是一个**独立的判定者**，不是执行器自己的马甲。
// 这个名字属于本包的 Judge：它按 manifest 声明的断言判，
// 执行器在它面前没有任何发言权。
const VerifierName = "verify_assert"

// Outcome 把 Verdict 翻译成执行记录里 outcome 字段的取值。
//
// ok=false 只有一个含义：**这个结论不许写成 resolution**。
// 目前只有 unknown 走这条路——契约说「intent 之后没有 resolution ⇒ unknown，
// 必须显式裁决」，所以「判不了」的正确落盘方式就是不落盘那条结论。
func (v Verdict) Outcome() (string, bool) {
	switch v {
	case VerdictOK, VerdictFailed, VerdictTimeout, VerdictRefused:
		return string(v), true
	default:
		return "", false
	}
}

// Observed 是核验器能看到的**原始事实**，不是观点。
//
// 注意它的字段全是别人（宿主、内核、计时器）产生的东西：
// 退出码、是否被时限杀掉、是否根本没启动起来、收到多少字节、花了多久。
// 这里**没有** «ok» 字段，而且以后也不许加——那就是执行者自证。
type Observed struct {
	// Exit 是子进程退出码；-1 表示被信号杀掉（此时 TimedOut 通常为真）。
	Exit int `json:"exit"`
	// TimedOut 是「声明的预算内没结束，宿主把它杀了」这个事实。
	TimedOut bool `json:"timed_out"`
	// Refused 是「宿主拒绝启动」这个事实：解释器不存在、像素不合法等。
	Refused bool `json:"refused"`
	// Note 携带 Refused 的原因原文（来自 exec/os 的错误），供人读。
	Note string `json:"note,omitempty"`
	// Stdout / Stderr 是捕获到的字节，可能已被输出上限截断。
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// StdoutTruncated / StderrTruncated 是「后面还有字节，但被上限截掉了」。
	// 它们存在的唯一理由是：截断过的观测不许被当成完整观测来判假。
	StdoutTruncated bool `json:"stdout_truncated,omitempty"`
	StderrTruncated bool `json:"stderr_truncated,omitempty"`
	// LatencyMS 是墙钟实测，唯一一处用它做判据的地方是断言里的 latency_ms。
	LatencyMS int64 `json:"latency_ms"`
}

// Check 是单条断言的判定结果。
type Check struct {
	Assert string `json:"assert"`
	// Result ∈ true / false / unevaluable。三态而不是布尔：
	// 「读不懂这条断言」和「这条断言不成立」是两件不同的事，
	// 合成布尔就把「读不懂」挤成了 false（或者更糟，挤成 true）。
	Result string `json:"result"`
	// Detail 只在 unevaluable 时有值，说明为什么读不懂。
	Detail string `json:"detail,omitempty"`
}

// 单条断言的三种结果。
const (
	ResultTrue        = "true"
	ResultFalse       = "false"
	ResultUnevaluable = "unevaluable"
)

// Judgement 是核验器的全部输出：一个结论、一个署名、一段理由、逐条明细。
type Judgement struct {
	Verdict    Verdict `json:"verdict"`
	VerifiedBy string  `json:"verified_by"`
	Reason     string  `json:"reason"`
	Checks     []Check `json:"checks"`
}

// Judge 是核验器：**唯一**一个把执行变成结论的地方。
//
// 判据的顺序是有意的，而且不许被「反正结论都是 ok=false」抹平：
//
//	refused  →  执行根本没发生
//	timed_out→  进程被时限杀掉（时序事实压过一切声明）
//	有确凿的假 → failed（被证明的坏）
//	有判不了的 → unknown（没被证明的好，也不许当成功）
//	全真      → ok
//
// 空断言列表 ⇒ unknown，不是 ok：没有声明就没有核验，
// 而「没有核验」在任何情况下都不等于「成功」。
func Judge(asserts []string, obs Observed) Judgement {
	j := Judgement{Verdict: VerdictUnknown, VerifiedBy: VerifierName}

	if obs.Refused {
		j.Verdict = VerdictRefused
		j.Reason = "the host refused to start the process: this is not an execution result, the execution did not happen"
		if obs.Note != "" {
			j.Reason += "（" + obs.Note + "）"
		}
		return j
	}
	if obs.TimedOut {
		j.Verdict = VerdictTimeout
		j.Reason = fmt.Sprintf("the process did not finish within the declared budget and was killed (%dms)", obs.LatencyMS)
		return j
	}
	if len(asserts) == 0 {
		j.Reason = "the manifest declares no verify.assert: with nothing declared there is nothing to verify against"
		return j
	}

	var failed, unable []string
	for _, a := range asserts {
		c := evaluate(a, obs)
		j.Checks = append(j.Checks, c)
		switch c.Result {
		case ResultFalse:
			failed = append(failed, a)
		case ResultUnevaluable:
			unable = append(unable, a+"（"+c.Detail+"）")
		}
	}

	switch {
	case len(failed) > 0:
		j.Verdict = VerdictFailed
		j.Reason = "a declared assertion is false: " + strings.Join(failed, "; ")
	case len(unable) > 0:
		// 关键的一条：判不了就说判不了，绝不回落到 ok。
		j.Verdict = VerdictUnknown
		j.Reason = "cannot be judged: " + strings.Join(unable, "; ")
	default:
		j.Verdict = VerdictOK
		j.Reason = fmt.Sprintf("all %d declared assertions hold (exit=%d, %dms)",
			len(asserts), obs.Exit, obs.LatencyMS)
	}
	return j
}

// evaluate 判定一条断言。任何读不懂的地方都返回 unevaluable，绝不返回 true。
func evaluate(assert string, obs Observed) Check {
	c := Check{Assert: assert, Result: ResultUnevaluable}

	field, op, lit, detail := parseAssert(assert)
	if detail != "" {
		c.Detail = detail
		return c
	}
	left, ok := fieldValue(field, obs)
	if !ok {
		c.Detail = "unknown field " + field + ": the verifier only reads exit / latency_ms / timed_out / stdout / stderr"
		return c
	}

	switch op {
	case "contains":
		// contains 是唯一一种「截断也不怕」的断言：在截下来的前缀里找得到，
		// 在整个输出里就一定找得到。找不到则不能下定论（尾巴可能被截掉了）。
		if left.kind != kindStr || lit.kind != kindStr {
			c.Detail = "contains needs a string on both sides"
			return c
		}
		if strings.Contains(left.str, lit.str) {
			c.Result = ResultTrue
			return c
		}
		if truncatable(field, obs) {
			c.Detail = field + " was cut by the output cap: not finding it is not proof that it is absent"
			return c
		}
		c.Result = ResultFalse
		return c

	case "==", "!=":
		if left.kind != lit.kind {
			c.Detail = "the two sides have different types: " + field + " is " + left.kind + ", the literal is " + lit.kind
			return c
		}
		if truncatable(field, obs) {
			// 截断过的字符串做相等/不等：截下来的前缀相等不代表整体相等，
			// 所以两个方向都不可信 ⇒ 判不了。
			c.Detail = field + " was cut by the output cap: equality across a truncated prefix cannot be trusted"
			return c
		}
		eq := left == lit
		if (op == "==") == eq {
			c.Result = ResultTrue
		} else {
			c.Result = ResultFalse
		}
		return c

	default: // < <= > >=
		if left.kind != kindNum || lit.kind != kindNum {
			c.Detail = op + " only compares numeric fields (exit / latency_ms)"
			return c
		}
		var ok bool
		switch op {
		case "<":
			ok = left.num < lit.num
		case "<=":
			ok = left.num <= lit.num
		case ">":
			ok = left.num > lit.num
		case ">=":
			ok = left.num >= lit.num
		}
		if ok {
			c.Result = ResultTrue
		} else {
			c.Result = ResultFalse
		}
		return c
	}
}

// parseAssert 把一条断言拆成「字段 运算符 字面量」。
//
// 这里的语言是刻意做小的：一条断言就是一次比较，没有 and/or/not，没有括号。
// 语法支持布尔组合就得回答优先级与求值顺序，而判定器一旦复杂到需要被
// 自己核验，它就又变成了一个执行者。宁可少判，不可错判。
//
// 语法（其余一律 unevaluable）：
//
//	<字段> ==|!= <字面量>          exit == 0 / stdout != "" / timed_out == false
//	<字段> <|<=|>|>= <整数>        latency_ms <= 500
//	<字段> contains "字符串"       stdout contains "READY"
//	<字段> ∈ {exit, latency_ms, timed_out, stdout, stderr}
//	<字面量> ∈ 双引号字符串 | 整数 | true | false
func parseAssert(assert string) (field, op string, lit literal, detail string) {
	s := strings.TrimSpace(assert)
	if s == "" {
		return "", "", literal{}, "empty assertion"
	}
	i := 0
	for i < len(s) && isFieldByte(s[i]) {
		i++
	}
	if i == 0 {
		return "", "", literal{}, "an assertion must start with a field name (exit / latency_ms / timed_out / stdout / stderr)"
	}
	field = s[:i]
	rhs := strings.TrimSpace(s[i:])
	if rhs == "" {
		return "", "", literal{}, "the assertion names a field but has no comparison"
	}

	// 先长后短：<= 必须在 < 之前认出来，否则 "<=" 会被读成 "< 字面量 =="。
	switch {
	case strings.HasPrefix(rhs, "=="):
		op, rhs = "==", strings.TrimSpace(rhs[2:])
	case strings.HasPrefix(rhs, "!="):
		op, rhs = "!=", strings.TrimSpace(rhs[2:])
	case strings.HasPrefix(rhs, "<="):
		op, rhs = "<=", strings.TrimSpace(rhs[2:])
	case strings.HasPrefix(rhs, ">="):
		op, rhs = ">=", strings.TrimSpace(rhs[2:])
	case strings.HasPrefix(rhs, "contains") && wordBoundary(rhs[len("contains"):]):
		op, rhs = "contains", strings.TrimSpace(rhs[len("contains"):])
	case strings.HasPrefix(rhs, "<"):
		op, rhs = "<", strings.TrimSpace(rhs[1:])
	case strings.HasPrefix(rhs, ">"):
		op, rhs = ">", strings.TrimSpace(rhs[1:])
	default:
		return "", "", literal{}, "unknown operator (only == != < <= > >= contains)"
	}
	if rhs == "" {
		return "", "", literal{}, "the assertion has no right-hand literal"
	}
	lit, detail = parseLiteral(rhs)
	if detail != "" {
		return "", "", literal{}, detail
	}
	return field, op, lit, ""
}

func wordBoundary(rest string) bool {
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

func isFieldByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

const (
	kindNum  = "num"
	kindStr  = "str"
	kindBool = "bool"
)

type literal struct {
	kind  string
	str   string
	num   int64
	value bool
}

// parseLiteral 返回字面量，或一段「为什么读不懂」。
//
// 字符串用 strconv.Unquote：它同时管住转义与「尾巴上还有多余字符」。
// 自己写一个宽容的引号剥离器，正是把 `"a" true` 读成 a 的那类静默错的来源。
func parseLiteral(s string) (literal, string) {
	switch s {
	case "true":
		return literal{kind: kindBool, value: true}, ""
	case "false":
		return literal{kind: kindBool, value: false}, ""
	}
	if strings.HasPrefix(s, "\"") {
		u, err := strconv.Unquote(s)
		if err != nil {
			return literal{}, "the string literal is malformed (unterminated or bad escape): " + s
		}
		return literal{kind: kindStr, str: u}, ""
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return literal{kind: kindNum, num: n}, ""
	}
	return literal{}, "the literal is neither a quoted string, an integer, nor true/false: " + s
}

func fieldValue(field string, obs Observed) (literal, bool) {
	switch field {
	case "exit":
		return literal{kind: kindNum, num: int64(obs.Exit)}, true
	case "latency_ms":
		return literal{kind: kindNum, num: obs.LatencyMS}, true
	case "timed_out":
		return literal{kind: kindBool, value: obs.TimedOut}, true
	case "stdout":
		return literal{kind: kindStr, str: obs.Stdout}, true
	case "stderr":
		return literal{kind: kindStr, str: obs.Stderr}, true
	}
	return literal{}, false
}

func truncatable(field string, obs Observed) bool {
	switch field {
	case "stdout":
		return obs.StdoutTruncated
	case "stderr":
		return obs.StderrTruncated
	}
	return false
}

// Elevation 是一次执行**被允许**对证据等级做的事。
//
// Level 为空表示这一步不做（没到提级的条件，或者本来就不需要）。
type Elevation struct {
	Level  evidence.Level `json:"level"`
	Reason string         `json:"reason"`
}

// Elevated 报告这次执行是否真的改变了证据等级。
func (e Elevation) Elevated() bool { return e.Level != "" }

// Elevate 是 E0→E1 这一步的**唯一**判据，也是执行能碰到的唯一步。
//
// E1 的定义是「在沙箱里跑过一次且成功」，所以：
//   - 只有核验器按声明的断言判了 ok，才算跑过且成功；
//   - 声明已经是 E1 或更高 ⇒ 什么都不做（执行不会让证据 +1，那是刷分）；
//   - 声明的等级未知 ⇒ 什么都不做（不认识的东西不许当成 E0 处理）。
//
// 它**绝不**降级，也**绝不**一次跳到 E2 以上：E2 要的是第二个独立来源，
// E3/E4 要的是复核与生产运行，一次本地执行提供不了其中任何一样。
func Elevate(j Judgement, declared evidence.Level) Elevation {
	if !evidence.Known(declared) {
		return Elevation{Reason: "the declared evidence level is unknown (" + string(declared) + "): nothing is inferred from it"}
	}
	if j.Verdict != VerdictOK {
		return Elevation{Reason: "the verdict is " + string(j.Verdict) + ", not ok: no evidence is derived from an execution that did not verify"}
	}
	if evidence.AtLeast(declared, evidence.L1) {
		return Elevation{Reason: "the declaration is already " + string(declared) + ": one execution does not add a level"}
	}
	return Elevation{
		Level:  evidence.L1,
		Reason: "it really ran once in the sandbox and the verifier judged ok against the declared verify.assert",
	}
}
