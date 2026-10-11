package executor

import (
	"fmt"
	"strings"

	"teps/core"
)

// 组合执行：把一个 Composition 算成一张**执行图**。
//
// 这是「三生万物」里唯一能被证伪的那一条落地的地方。宗旨说
// `万物 = AI × 算力 × Skill`，而「生」这个字此前一直没做：技能是孤立的，
// 十个技能就是十种能力——那是三生十，不是三生万物。
// 组合让结果**本身成为一个技能**，于是它可以再被组合。
//
// 本文件是纯的：同样的 Composition 永远给同样的 Graph。
// 它不 spawn 任何东西，也不读任何字节。

// Composition 是 store.Composition 的**投影**。
//
// 投影而不是直接吃它，理由与 Declaration 相同：器官不互相 import，
// 而且这个结构体本身就是一份声明——凡是这里没出现的字段，
// 执行器的行为就不许依赖它。
//
// 特别注意 Children 里的 ref 在这里是**不透明字符串**，不是路径。
// 执行器从不把 ref 变成路径或命令的一部分——它只把 ref 还给适配器，
// 由适配器去技能店换回载荷。所以这里**不校验哈希格式**：
// 哈希格式是技能店的事实，在这里再写一遍就是同一个事实的第二个定义，
// 而本项目已经为「一个事实两个定义」付过三次学费。
// 执行器只要求 ref 非空，因为空 ref 连"哪一个"都答不上来。
type Composition struct {
	Op            string `json:"op"`
	Children      []string
	Wires         []Wire
	MaxAttempts   int
	MaxIterations int
}

// Wire 把一端的输出接到另一端的输入。
//
// "in" 与 "out" 是组合自己的边界。没有接线，组合就只是**并列**——
// 孩子们在彼此旁边跑，什么也不交换。
type Wire struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Step 是执行图里的一步。
type Step struct {
	Index int    `json:"index"`
	Ref   string `json:"ref"`
	// StdinFrom 说这一步的标准输入从哪来：
	//   "in"   —— 组合自己的输入
	//   "prev" —— 上一步的标准输出
	StdinFrom string `json:"stdin_from"`
}

// Graph 是「组合怎么跑」的全部答案。
type Graph struct {
	Op    string `json:"op"`
	Steps []Step `json:"steps"`
	// OutputFrom 说组合的输出取自哪里："last"（最后一步）或 "all"（全部，按序拼接）。
	OutputFrom string `json:"output_from"`
}

// 执行图支持的算子。**只有两个**，其余四个各自的拒绝理由单独写。
//
// 合成一个「不支持的算子」是错的：那句话会让人以为换个算子名就能过。
// 而实际上它们各自缺的是不同的东西。
const (
	OpSerial   = "serial"
	OpParallel = "parallel"
)

// PlanComposition 把组合算成一张执行图，或者拒绝。
//
// 这里的拒绝全部发生在**执行之前**，所以不写任何链上记录：
// 没有发生的事不该留下执行史。
func PlanComposition(c Composition) (Graph, *core.Refusal) {
	g := Graph{Op: c.Op}

	switch c.Op {
	case OpSerial, OpParallel:
	case "select":
		return g, core.Refuse(core.CodeBadRequest,
			"op=select needs a fallback decision, and deciding needs a judgement: this planner runs before anything has run, so it has nothing to decide on")
	case "conditional":
		return g, core.Refuse(core.CodeBadRequest,
			"op=conditional needs a predicate evaluated at run time: the planner is pure, so it cannot evaluate one, and a predicate it cannot evaluate would be silently ignored")
	case "retry":
		return g, core.Refuse(core.CodeBadRequest,
			"op=retry needs one chain record per attempt: this executor writes one intent per invoke, so a retry would either lose attempts or record them as a single one")
	case "loop":
		return g, core.Refuse(core.CodeBadRequest,
			"op=loop needs a terminating condition evaluated between iterations: without a predicate the bound would be the only stop, and a bound is not a condition")
	case "":
		return g, core.Refuse(core.CodeBadRequest,
			"the composition has no op: with no operator there is no shape to run")
	default:
		return g, core.Refuse(core.CodeBadRequest,
			"unknown composition op "+c.Op+": this planner does not guess how to compose it")
	}

	if len(c.Children) == 0 {
		return g, core.Refuse(core.CodeBadRequest,
			"a composition needs at least one child: an empty composition would succeed by having done nothing")
	}
	for i, ch := range c.Children {
		if ch == "" {
			return g, core.Refuse(core.CodeBadRequest,
				fmt.Sprintf("child %d has an empty ref: an empty ref names nothing", i))
		}
	}

	// 接线。默认形状是**唯一被实现的形状**，显式接线必须与它逐字相符。
	//
	// 这里刻意不"尽力而为"地解释接线：接受一条自己不去兑现的线，
	// 就是静默丢数据，而静默丢数据的组合会给出一个看起来正常的错答案。
	if refusal := checkWires(c); refusal != nil {
		return g, refusal
	}

	switch c.Op {
	case OpSerial:
		// A;B;C —— 第一步吃组合的输入，之后每一步吃上一步的输出。
		for i, ch := range c.Children {
			from := "prev"
			if i == 0 {
				from = "in"
			}
			g.Steps = append(g.Steps, Step{Index: i, Ref: ch, StdinFrom: from})
		}
		g.OutputFrom = "last"
	case OpParallel:
		// A‖B —— 每一步都吃组合的输入，孩子之间不交换任何东西。
		for i, ch := range c.Children {
			g.Steps = append(g.Steps, Step{Index: i, Ref: ch, StdinFrom: "in"})
		}
		g.OutputFrom = "all"
	}
	return g, nil
}

// checkWires 只接受与默认形状逐字相符的接线。
//
// serial 的默认形状是 in → 0:in，i:out → i+1:in，n-1:out → out。
// parallel 的默认形状是 in → 每个孩子，孩子之间没有线。
//
// 顺序无关：接线是一张图，不是一段文本。
func checkWires(c Composition) *core.Refusal {
	want := map[string]bool{}
	switch c.Op {
	case OpSerial:
		if len(c.Children) > 0 {
			want["in->0:in"] = true
		}
		for i := 0; i+1 < len(c.Children); i++ {
			want[fmt.Sprintf("%d:out->%d:in", i, i+1)] = true
		}
		if len(c.Children) > 0 {
			want[fmt.Sprintf("%d:out->out", len(c.Children)-1)] = true
		}
	case OpParallel:
		for i := range c.Children {
			want[fmt.Sprintf("in->%d:in", i)] = true
		}
	}

	seen := map[string]bool{}
	for _, w := range c.Wires {
		if w.From == "" || w.To == "" {
			return core.Refuse(core.CodeBadRequest, "a wire needs both ends")
		}
		// 反着穿边界是循环的定义，而循环需要终止条件，这一版没有。
		if w.From == "out" || w.To == "in" {
			return core.Refuse(core.CodeBadRequest,
				"a wire may not run backwards across the boundary: "+w.From+" -> "+w.To)
		}
		key := w.From + "->" + w.To
		if seen[key] {
			return core.Refuse(core.CodeBadRequest, "the same wire is declared twice: "+key)
		}
		seen[key] = true
		if !want[key] {
			return core.Refuse(core.CodeBadRequest,
				"wire "+key+" is not the shape this executor implements: op="+c.Op+
					" runs the default chain, and honouring an arbitrary wire would mean dropping data silently")
		}
	}
	return nil
}

// JudgeComposition 从**孩子们的判定**算出组合的判定。
//
// 它不重新求值任何断言：组合的核验不是"把孩子的输出拼起来再套一遍断言"，
// 而是"每个孩子各自被独立判定过了，组合怎么读这些判定"。
// 后者才是真的，前者只是看起来更整齐。
//
// 规则，按顺序：
//  1. 没有孩子 ⇒ unknown。一个没有证据的组合不许默认成功。
//  2. 任何孩子**确凿**不 ok（failed / timeout / refused）⇒ 组合就是那个判定，
//     取第一个，理由是确定性的：同一张图重放要给同一个答案。
//  3. 否则任何孩子 unknown ⇒ 组合 unknown。**部分判不了，整体就判不了。**
//  4. 否则全部 ok ⇒ ok。
//
// 注意第 2 条优先于第 3 条：一个孩子失败了，组合就已经确定不 ok 了，
// 不需要等另一个判不了的孩子。反过来先报 unknown 会把一个已知的失败藏起来。
func JudgeComposition(js []Judgement) Judgement {
	if len(js) == 0 {
		return Judgement{
			Verdict:    VerdictUnknown,
			VerifiedBy: VerifierName,
			Reason:     "a composition with no children has no evidence at all",
		}
	}

	for i, j := range js {
		switch j.Verdict {
		case VerdictFailed, VerdictTimeout, VerdictRefused:
			return Judgement{
				Verdict:    j.Verdict,
				VerifiedBy: VerifierName,
				Reason:     fmt.Sprintf("child %d is %s: %s", i, j.Verdict, j.Reason),
			}
		}
	}

	var unable []string
	for i, j := range js {
		if j.Verdict != VerdictOK {
			unable = append(unable, fmt.Sprintf("child %d", i))
		}
	}
	if len(unable) > 0 {
		return Judgement{
			Verdict:    VerdictUnknown,
			VerifiedBy: VerifierName,
			Reason: "cannot be judged: " + strings.Join(unable, " ") +
				" could not be judged, and a composition is only as judgeable as its least judgeable part",
		}
	}

	return Judgement{
		Verdict:    VerdictOK,
		VerifiedBy: VerifierName,
		Reason:     fmt.Sprintf("all %d children were judged ok", len(js)),
	}
}
