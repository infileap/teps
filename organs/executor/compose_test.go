package executor

import (
	"strings"
	"testing"
)

func refs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a'+i)) + "-ref"
	}
	return out
}

func TestSerialRunsTheDefaultChain(t *testing.T) {
	g, ref := PlanComposition(Composition{Op: OpSerial, Children: refs(3)})
	if ref != nil {
		t.Fatalf("a plain serial composition must plan, got %v", ref)
	}
	if len(g.Steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(g.Steps))
	}
	want := []string{"in", "prev", "prev"}
	for i, s := range g.Steps {
		if s.StdinFrom != want[i] {
			t.Fatalf("step %d must read from %q, got %q", i, want[i], s.StdinFrom)
		}
		if s.Index != i {
			t.Fatalf("step %d has index %d", i, s.Index)
		}
	}
	if g.OutputFrom != "last" {
		t.Fatalf("a serial composition's output is the last step's, got %q", g.OutputFrom)
	}
}

func TestParallelFansOutAndChainsNothing(t *testing.T) {
	g, ref := PlanComposition(Composition{Op: OpParallel, Children: refs(3)})
	if ref != nil {
		t.Fatalf("a plain parallel composition must plan, got %v", ref)
	}
	for i, s := range g.Steps {
		if s.StdinFrom != "in" {
			t.Fatalf("step %d in a parallel composition must read the composition's input, got %q", i, s.StdinFrom)
		}
	}
	if g.OutputFrom != "all" {
		t.Fatalf("a parallel composition's output is all of them, got %q", g.OutputFrom)
	}
}

// 四种还没实现的算子各自缺的东西不同，所以理由必须不同。
// 合成一句「不支持的算子」会让人以为换个算子名就能过。
func TestEveryUnimplementedOpHasItsOwnReason(t *testing.T) {
	reasons := map[string]string{}
	for _, op := range []string{"select", "conditional", "retry", "loop"} {
		_, ref := PlanComposition(Composition{Op: op, Children: refs(1), MaxAttempts: 1, MaxIterations: 1})
		if ref == nil {
			t.Fatalf("op=%s must be refused until it is implemented", op)
		}
		if ref.Code != "bad_request" {
			t.Fatalf("op=%s got code %s", op, ref.Code)
		}
		reasons[op] = ref.Reason
	}
	seen := map[string]string{}
	for op, r := range reasons {
		if other, dup := seen[r]; dup {
			t.Fatalf("op=%s and op=%s share a refusal reason: %s", op, other, r)
		}
		seen[r] = op
	}
}

func TestEmptyOpAndUnknownOpAreDifferentRefusals(t *testing.T) {
	_, a := PlanComposition(Composition{Op: "", Children: refs(1)})
	_, b := PlanComposition(Composition{Op: "teleport", Children: refs(1)})
	if a == nil || b == nil {
		t.Fatal("both must be refused")
	}
	if a.Reason == b.Reason {
		t.Fatal("a missing op and an unknown op are different facts and must read differently")
	}
}

func TestAnEmptyCompositionIsRefused(t *testing.T) {
	_, ref := PlanComposition(Composition{Op: OpSerial})
	if ref == nil {
		t.Fatal("an empty composition would succeed by having done nothing")
	}
	if !strings.Contains(ref.Reason, "at least one child") {
		t.Fatalf("the reason must say why, got %q", ref.Reason)
	}
}

func TestAnEmptyChildRefIsRefused(t *testing.T) {
	_, ref := PlanComposition(Composition{Op: OpSerial, Children: []string{"a-ref", ""}})
	if ref == nil {
		t.Fatal("an empty ref names nothing")
	}
	if !strings.Contains(ref.Reason, "child 1") {
		t.Fatalf("the reason must name which child, got %q", ref.Reason)
	}
}

// 执行器把 ref 当**不透明字符串**，从不校验哈希格式。
// 哈希格式是技能店的事实；在这里再写一遍就是同一个事实的第二个定义。
func TestThePlannerDoesNotRevalidateHashFormat(t *testing.T) {
	g, ref := PlanComposition(Composition{Op: OpSerial, Children: []string{"not-a-hash-at-all"}})
	if ref != nil {
		t.Fatalf("the executor never turns a ref into a path, so hash format is not its business, got %v", ref)
	}
	if g.Steps[0].Ref != "not-a-hash-at-all" {
		t.Fatal("the ref must pass through unchanged")
	}
}

func TestTheDefaultWiresAreAccepted(t *testing.T) {
	c := Composition{
		Op:       OpSerial,
		Children: refs(3),
		Wires: []Wire{
			{From: "in", To: "0:in"},
			{From: "0:out", To: "1:in"},
			{From: "1:out", To: "2:in"},
			{From: "2:out", To: "out"},
		},
	}
	if _, ref := PlanComposition(c); ref != nil {
		t.Fatalf("the default chain spelled out must be accepted, got %v", ref)
	}
}

// 顺序无关：接线是一张图，不是一段文本。
func TestWiresAreOrderIndependent(t *testing.T) {
	c := Composition{
		Op:       OpSerial,
		Children: refs(2),
		Wires: []Wire{
			{From: "1:out", To: "out"},
			{From: "in", To: "0:in"},
			{From: "0:out", To: "1:in"},
		},
	}
	if _, ref := PlanComposition(c); ref != nil {
		t.Fatalf("wires are a graph and must not depend on order, got %v", ref)
	}
}

// 接受一条自己不去兑现的线，就是静默丢数据。
func TestAWireTheExecutorDoesNotHonourIsRefused(t *testing.T) {
	c := Composition{
		Op:       OpSerial,
		Children: refs(3),
		// 0 的输出同时喂给 1 和 2：这是合法数据流，但不是本执行器实现的形状。
		Wires: []Wire{{From: "0:out", To: "2:in"}},
	}
	_, ref := PlanComposition(c)
	if ref == nil {
		t.Fatal("a wire outside the implemented shape must be refused, not ignored")
	}
	if !strings.Contains(ref.Reason, "dropping data silently") {
		t.Fatalf("the reason must say what the risk is, got %q", ref.Reason)
	}
}

func TestAWireRunningBackwardsIsRefused(t *testing.T) {
	c := Composition{Op: OpSerial, Children: refs(2), Wires: []Wire{{From: "out", To: "0:in"}}}
	_, ref := PlanComposition(c)
	if ref == nil || !strings.Contains(ref.Reason, "backwards") {
		t.Fatalf("a wire running backwards is a loop, and loops need a terminating condition, got %v", ref)
	}
}

func TestADuplicateWireIsRefused(t *testing.T) {
	c := Composition{
		Op:       OpSerial,
		Children: refs(2),
		Wires:    []Wire{{From: "in", To: "0:in"}, {From: "in", To: "0:in"}},
	}
	_, ref := PlanComposition(c)
	if ref == nil || !strings.Contains(ref.Reason, "twice") {
		t.Fatalf("a wire declared twice is a mistake worth naming, got %v", ref)
	}
}

func TestAHalfWrittenWireIsRefused(t *testing.T) {
	c := Composition{Op: OpSerial, Children: refs(1), Wires: []Wire{{From: "in"}}}
	_, ref := PlanComposition(c)
	if ref == nil || !strings.Contains(ref.Reason, "both ends") {
		t.Fatalf("a wire needs both ends, got %v", ref)
	}
}

func TestParallelWiresMustFanOut(t *testing.T) {
	ok := Composition{
		Op: OpParallel, Children: refs(2),
		Wires: []Wire{{From: "in", To: "0:in"}, {From: "in", To: "1:in"}},
	}
	if _, ref := PlanComposition(ok); ref != nil {
		t.Fatalf("a fan-out is the parallel default shape, got %v", ref)
	}
	bad := Composition{
		Op: OpParallel, Children: refs(2),
		Wires: []Wire{{From: "0:out", To: "1:in"}},
	}
	if _, ref := PlanComposition(bad); ref == nil {
		t.Fatal("children of a parallel composition exchange nothing, so this wire is not the implemented shape")
	}
}

func TestPlanningIsDeterministic(t *testing.T) {
	c := Composition{Op: OpSerial, Children: refs(4)}
	a, _ := PlanComposition(c)
	b, _ := PlanComposition(c)
	if len(a.Steps) != len(b.Steps) {
		t.Fatal("step count differs between two identical plans")
	}
	for i := range a.Steps {
		if a.Steps[i] != b.Steps[i] {
			t.Fatalf("step %d differs between two identical plans", i)
		}
	}
	if a.OutputFrom != b.OutputFrom {
		t.Fatal("output source differs between two identical plans")
	}
}

// ---- JudgeComposition ----

func j(v Verdict) Judgement {
	return Judgement{Verdict: v, VerifiedBy: VerifierName, Reason: "because " + string(v)}
}

func TestACompositionWithNoChildrenCannotBeJudged(t *testing.T) {
	got := JudgeComposition(nil)
	if got.Verdict != VerdictUnknown {
		t.Fatalf("a composition with no evidence must not default to ok, got %s", got.Verdict)
	}
	if got.VerifiedBy != VerifierName {
		t.Fatalf("the composition is judged by the verifier, got %q", got.VerifiedBy)
	}
}

func TestAllChildrenOkMakesTheCompositionOk(t *testing.T) {
	got := JudgeComposition([]Judgement{j(VerdictOK), j(VerdictOK), j(VerdictOK)})
	if got.Verdict != VerdictOK {
		t.Fatalf("want ok, got %s", got.Verdict)
	}
	if !strings.Contains(got.Reason, "3 children") {
		t.Fatalf("the reason must say how many, got %q", got.Reason)
	}
}

func TestOneFailedChildFailsTheComposition(t *testing.T) {
	got := JudgeComposition([]Judgement{j(VerdictOK), j(VerdictFailed), j(VerdictOK)})
	if got.Verdict != VerdictFailed {
		t.Fatalf("want failed, got %s", got.Verdict)
	}
	if !strings.Contains(got.Reason, "child 1") {
		t.Fatalf("the reason must name which child failed, got %q", got.Reason)
	}
}

func TestATimeoutAndARefusalPropagateAsThemselves(t *testing.T) {
	if got := JudgeComposition([]Judgement{j(VerdictOK), j(VerdictTimeout)}); got.Verdict != VerdictTimeout {
		t.Fatalf("want timeout, got %s", got.Verdict)
	}
	if got := JudgeComposition([]Judgement{j(VerdictOK), j(VerdictRefused)}); got.Verdict != VerdictRefused {
		t.Fatalf("want refused, got %s", got.Verdict)
	}
}

// 部分判不了，整体就判不了。这是「判不了就不写结论」在组合上的样子。
func TestOneUnjudgeableChildMakesTheCompositionUnjudgeable(t *testing.T) {
	got := JudgeComposition([]Judgement{j(VerdictOK), j(VerdictUnknown)})
	if got.Verdict != VerdictUnknown {
		t.Fatalf("want unknown, got %s", got.Verdict)
	}
	if !strings.Contains(got.Reason, "child 1") {
		t.Fatalf("the reason must name which child, got %q", got.Reason)
	}
}

// ★ 一个确凿的失败优先于一个判不了的孩子。
// 反过来先报 unknown，会把一个已知的失败藏起来。
func TestADefiniteFailureOutranksAnUnjudgeableSibling(t *testing.T) {
	// 判不了的在前面。
	got := JudgeComposition([]Judgement{j(VerdictUnknown), j(VerdictFailed)})
	if got.Verdict != VerdictFailed {
		t.Fatalf("a known failure must not be hidden behind an unjudgeable sibling, got %s", got.Verdict)
	}
	if !strings.Contains(got.Reason, "child 1") {
		t.Fatalf("the reason must name the child that failed, got %q", got.Reason)
	}
}

// 第一个确凿不 ok 的孩子胜出：同一张图重放要给同一个答案。
func TestTheFirstDefiniteFailureWins(t *testing.T) {
	got := JudgeComposition([]Judgement{j(VerdictTimeout), j(VerdictFailed), j(VerdictRefused)})
	if got.Verdict != VerdictTimeout {
		t.Fatalf("want the first one, got %s", got.Verdict)
	}
	if !strings.Contains(got.Reason, "child 0") {
		t.Fatalf("the reason must name child 0, got %q", got.Reason)
	}
}

// ★ 组合判不了时，Outcome() 必须说「不许写」——这是它和 VerdictOK 的全部区别。
func TestAnUnjudgeableCompositionMustNotBeWritten(t *testing.T) {
	got := JudgeComposition([]Judgement{j(VerdictOK), j(VerdictUnknown)})
	if _, write := got.Verdict.Outcome(); write {
		t.Fatal("an unjudgeable composition must leave the intent open, not write a conclusion")
	}
	ok := JudgeComposition([]Judgement{j(VerdictOK)})
	if _, write := ok.Verdict.Outcome(); !write {
		t.Fatal("a judged composition must be written")
	}
}
