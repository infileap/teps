package arbiter

import (
	"math"
	"strings"
	"testing"

	"teps/evidence"
)

func close(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---------- the posterior ----------

func TestPriorIsUniformNotCertain(t *testing.T) {
	p := Prior()
	if !close(p.Mean(), 0.5, 1e-9) {
		t.Fatalf("an unobserved thing must not look either good or bad, got %.4f", p.Mean())
	}
	if p.Alpha <= 0 || p.Beta <= 0 {
		t.Fatal("zero pseudo-counts make the mean undefined and one failure into certainty")
	}
}

func TestObserveAccumulates(t *testing.T) {
	p := Prior()
	for i := 0; i < 9; i++ {
		p = p.Observe(true, 1.0) // no forgetting
	}
	// Beta(10, 1) -> 10/11
	if !close(p.Mean(), 10.0/11.0, 1e-9) {
		t.Fatalf("want 10/11, got %.6f", p.Mean())
	}
}

func TestDecayIsWhatBoundsTheMemory(t *testing.T) {
	// Without decay the count grows without bound, which is the reason a plain
	// Beta posterior cannot notice rot.
	plain := Prior()
	decayed := Prior()
	for i := 0; i < 500; i++ {
		plain = plain.Observe(true, 1.0)
		decayed = decayed.Observe(true, DefaultDecay)
	}
	if plain.Count() < 400 {
		t.Fatalf("the no-decay count should be large, got %.1f", plain.Count())
	}
	ceiling := 1/(1-DefaultDecay) + 2
	if decayed.Count() > ceiling {
		t.Fatalf("decay must bound the effective count near %.0f, got %.1f", ceiling, decayed.Count())
	}
	t.Logf("after 500 observations: no-decay count=%.0f, decayed count=%.1f (ceiling %.0f)",
		plain.Count(), decayed.Count(), ceiling)
}

func TestFoldIsOrderDependent(t *testing.T) {
	k := Key{Skill: "s", TaskClass: "t"}
	goodThenBad := Fold([]Observation{{Key: k, OK: true}, {Key: k, OK: true}, {Key: k, OK: true}, {Key: k, OK: false}}, DefaultDecay)
	badThenGood := Fold([]Observation{{Key: k, OK: false}, {Key: k, OK: true}, {Key: k, OK: true}, {Key: k, OK: true}}, DefaultDecay)
	if close(goodThenBad[k].Mean(), badThenGood[k].Mean(), 1e-9) {
		t.Fatal("forgetting makes the order matter; a fold that ignores it disagrees with the live system")
	}
	if goodThenBad[k].Mean() >= badThenGood[k].Mean() {
		t.Fatalf("the recent failure must count for more: %.4f vs %.4f",
			goodThenBad[k].Mean(), badThenGood[k].Mean())
	}
}

func TestUnknownKeyGetsThePrior(t *testing.T) {
	tbl := Table{}
	if !close(tbl.Get(Key{Skill: "never", TaskClass: "seen"}).Mean(), 0.5, 1e-9) {
		t.Fatal("a missing key must fall back to the prior, not to zero")
	}
}

// ---------- the falsification point ----------

func TestARottenSkillIsAbandonedWithoutAnyoneNoticing(t *testing.T) {
	// This is the test the whole package exists for. A skill builds a long, good
	// record, then starts failing. Nobody tells the arbiter: it sees only
	// results. The question is whether the choice changes by itself, and how
	// soon.
	key := Key{Skill: "read.file", TaskClass: "task_exec"}
	tbl := Table{}
	for i := 0; i < 200; i++ {
		tbl[key] = tbl.Get(key).Observe(true, DefaultDecay)
	}
	good := tbl.Get(key).Mean()
	if good < 0.9 {
		t.Fatalf("the fixture did not build a good record, got %.4f", good)
	}

	offers := []Offer{
		{Action: ActionSkill, Skill: "read.file", TaskClass: "task_exec", Value: 10, Cost: 0.01, Latency: 0.05, LatencyWeight: 0.1},
		{Action: ActionEscalate, Skill: "l3", TaskClass: "task_exec", Value: 10, Cost: 1.0, Latency: 2.0, LatencyWeight: 0.1, Pinned: 0.9, UsePinned: true},
	}
	chosen, ref := Choose(offers, tbl)
	if ref != nil {
		t.Fatalf("refused: %s", ref.Reason)
	}
	if chosen.Offer.Action != ActionSkill {
		t.Fatalf("the good skill should be chosen while it is good, got %s", chosen.Offer.Action)
	}

	switchedAt := -1
	for i := 1; i <= 500; i++ {
		tbl[key] = tbl.Get(key).Observe(false, DefaultDecay)
		c, _ := Choose(offers, tbl)
		if c.Offer.Action != ActionSkill {
			switchedAt = i
			break
		}
	}
	if switchedAt < 0 {
		t.Fatal("the skill rotted and the arbiter never stopped choosing it")
	}
	// The bound is not decoration: without decay a plain Beta posterior needs on
	// the order of a hundred failures here, and the test would fail.
	if switchedAt > 25 {
		t.Fatalf("rot must be noticed in a bounded number of calls, took %d", switchedAt)
	}
	t.Logf("a skill that was %.4f good was abandoned after %d consecutive failures (bound 25)", good, switchedAt)
}

func TestAPlainPosteriorWouldMissTheRot(t *testing.T) {
	// The same fixture with no forgetting. This is the negative control: it
	// shows the bound above is a property of the decay, not of the arithmetic.
	key := Key{Skill: "read.file", TaskClass: "task_exec"}
	tbl := Table{}
	for i := 0; i < 200; i++ {
		tbl[key] = tbl.Get(key).Observe(true, 1.0)
	}
	for i := 0; i < 25; i++ {
		tbl[key] = tbl.Get(key).Observe(false, 1.0)
	}
	plain := tbl.Get(key).Mean()
	decayed := Table{}
	for i := 0; i < 200; i++ {
		decayed[key] = decayed.Get(key).Observe(true, DefaultDecay)
	}
	for i := 0; i < 25; i++ {
		decayed[key] = decayed.Get(key).Observe(false, DefaultDecay)
	}
	t.Logf("after 25 failures: no-decay mean=%.4f, decayed mean=%.4f", plain, decayed.Get(key).Mean())
	if plain <= decayed.Get(key).Mean() {
		t.Fatal("the no-decay posterior should still look better than the decayed one")
	}
}

func TestAnAbandonedSkillCanComeBack(t *testing.T) {
	// Exploration is the only thing that makes a decision reversible. Without it
	// one bad patch kills a good skill permanently.
	unseen := Prior().UpperBound()
	if unseen < 0.9 {
		t.Fatalf("an untried action must look promising rather than unknown: %.4f", unseen)
	}
	bad := Posterior{Alpha: 1, Beta: 20}
	if bad.UpperBound() <= bad.Mean() {
		t.Fatalf("the upper bound must exceed the mean, or nothing is ever retried: %.4f vs %.4f",
			bad.UpperBound(), bad.Mean())
	}

	key := Key{Skill: "s", TaskClass: "t"}
	tbl := Table{}
	for i := 0; i < 30; i++ {
		tbl[key] = tbl.Get(key).Observe(false, DefaultDecay)
	}
	low := tbl.Get(key).Mean()
	for i := 0; i < 30; i++ {
		tbl[key] = tbl.Get(key).Observe(true, DefaultDecay)
	}
	high := tbl.Get(key).Mean()
	if high <= low+0.5 {
		t.Fatalf("a recovered skill must climb back: %.4f -> %.4f", low, high)
	}
	t.Logf("a skill that failed 30 times recovered from %.3f to %.3f in 30 successes", low, high)
}

// ★★★ 界必须随证据收紧 —— 而**两边的点都必须在公式里面**。
//
// 此前这一条拿 `{2,1}` 当「一次」那一点，而 `{2,1}` 的 `n = 1` 那时被
// `if n <= 1 { return 1.0 }` 接走了：**那一点根本没进公式**。所以它比的是
// 「守卫的 1.0」与「`{100,50}` 的 0.7126」，而不是在比公式 ——
// **一条读作「界随证据收紧」的测试，它的一半在函数之外。**
//
// 正确的比法是**固定均值、只加证据**：`{10,5}` 与 `{100,50}` 的均值都是
// 0.6667，而 n 从 13 涨到 148。那才是「同一个成功率、更多证据」。
func TestUpperBoundShrinksWithEvidence(t *testing.T) {
	few := Posterior{Alpha: 10, Beta: 5}
	many := Posterior{Alpha: 100, Beta: 50}
	if few.Mean() != many.Mean() {
		t.Fatalf("this test is only about evidence: the two means must be equal, got %.4f vs %.4f",
			few.Mean(), many.Mean())
	}
	lo, hi := few.UpperBound(), many.UpperBound()
	if hi >= lo {
		t.Fatalf("the bound must tighten as evidence accumulates at a fixed mean: few=%.4f many=%.4f", lo, hi)
	}
	// ★ 而两边都必须真的**进了公式** —— 否则这一条又会退化成比较两个守卫。
	if lo == UnobservedUpperBound {
		t.Fatalf("the few-evidence point must be computed by the formula, not answered by the unobserved case: %.4f", lo)
	}
	if hi == UnobservedUpperBound {
		t.Fatalf("the many-evidence point must be computed by the formula: %.4f", hi)
	}
}

// ★★★ 零观测与一次观测是两档，不是一档。
//
// 它们此前共用 `if n <= 1 { return 1.0 }`，于是**一个跑过一次的技能与一个从没
// 跑过的技能报价一模一样**。而 `n = 1` 时公式是有定义的：`log(2)/1 = 0.693`。
func TestOneObservationIsNotTheSameAsNone(t *testing.T) {
	none := Prior().UpperBound()
	once := Posterior{Alpha: 2, Beta: 1}.UpperBound()
	if none != UnobservedUpperBound {
		t.Fatalf("zero observations are the named choice: got %.4f", none)
	}
	if once == UnobservedUpperBound {
		t.Fatalf("one observation must be computed, not answered by the zero-observation case: %.4f", once)
	}
	// ★ 而它必须是那个公式给的数：mean + Exploration * sqrt(log(2)/1)。
	want := Posterior{Alpha: 2, Beta: 1}.Mean() + Exploration*math.Sqrt(math.Log(2))
	if math.Abs(once-want) > 1e-12 {
		t.Fatalf("one observation should be %.6f, got %.6f", want, once)
	}
	// ★ 而且它必须仍然**高于均值** —— 否则「没试过的要显得有希望」那一半就没了。
	// ★ 复合字面量出现在 if 的条件里必须加括号：不加的话 `{` 与块的开头
	// 分不开（Go 的解析器就是这么规定的）。
	if once <= (Posterior{Alpha: 2, Beta: 1}).Mean() {
		t.Fatalf("the bound must exceed the mean: %.4f", once)
	}
}

// ---------- ranking ----------

func TestRankIsDeterministicOnTies(t *testing.T) {
	offers := []Offer{
		{Action: ActionSkill, Skill: "b", TaskClass: "t", Value: 1},
		{Action: ActionLocal, Skill: "a", TaskClass: "t", Value: 1},
	}
	first := Rank(offers, Table{})
	for i := 0; i < 20; i++ {
		again := Rank(offers, Table{})
		for j := range first {
			if first[j].Offer.Action != again[j].Offer.Action {
				t.Fatalf("ties must break the same way every time: %v vs %v", first[j].Offer.Action, again[j].Offer.Action)
			}
		}
	}
	if first[0].Offer.Action != ActionLocal {
		t.Fatalf("the tie-break is the action name, so local sorts first, got %s", first[0].Offer.Action)
	}
}

func TestChooseRefusesAnEmptySet(t *testing.T) {
	_, ref := Choose(nil, Table{})
	if ref == nil || ref.Code != "bad_request" {
		t.Fatalf("an empty offer set is a refusal, not a default action, got %v", ref)
	}
}

func TestUtilityChargesLatencySeparatelyFromCost(t *testing.T) {
	cheapButSlow := Offer{Action: ActionAskHuman, Value: 10, Cost: 0, Latency: 3600, LatencyWeight: 0.01}
	expensiveButFast := Offer{Action: ActionEscalate, Value: 10, Cost: 10, Latency: 1, LatencyWeight: 0.01}
	p := Posterior{Alpha: 100, Beta: 1}
	if Utility(cheapButSlow, p) >= Utility(expensiveButFast, p) {
		t.Fatal("a human is cheap in money and ruinous in time; collapsing the two would hide that")
	}
}

// ---------- escalation ----------

func TestEscalationRequiresAMargin(t *testing.T) {
	// local:     0.80 * 10          = 8.00
	// escalate:  0.86 * 10 - 0.5    = 8.10   -> the paid action gains exactly 0.10
	offers := []Offer{
		{Action: ActionLocal, Skill: "l", TaskClass: "t", Value: 10, Pinned: 0.80, UsePinned: true},
		{Action: ActionEscalate, Skill: "e", TaskClass: "t", Value: 10, Cost: 0.5, Pinned: 0.86, UsePinned: true},
	}
	// A gain of exactly the margin is not a gain. The comparison is strict, and
	// this pins it: an off-by-one here would let every coin flip through.
	at, _ := ShouldEscalate(offers, Table{}, 0.10)
	if at.Chosen.Offer.Action != ActionLocal {
		t.Fatalf("gaining exactly the margin must not buy the spend, chose %s", at.Chosen.Offer.Action)
	}
	below, _ := ShouldEscalate(offers, Table{}, 0.05)
	if below.Chosen.Offer.Action != ActionEscalate {
		t.Fatalf("0.10 clears a 0.05 margin, chose %s", below.Chosen.Offer.Action)
	}
	above, _ := ShouldEscalate(offers, Table{}, 0.20)
	if above.Chosen.Offer.Action != ActionLocal {
		t.Fatalf("0.10 does not clear a 0.20 margin, chose %s", above.Chosen.Offer.Action)
	}
}

func TestEscalationWithoutALocalOptionSaysSo(t *testing.T) {
	offers := []Offer{{Action: ActionEscalate, Skill: "e", TaskClass: "t", Value: 10, Cost: 1, Pinned: 0.9, UsePinned: true}}
	got, _ := ShouldEscalate(offers, Table{}, 0.2)
	// ★ 措辞从「no local action was offered」改成「no free action was offered」：
	// 判据从**按动作名枚举**（`ActionLocal` 或 `ActionSkill`）改成**按价钱分**
	// （花外面钱的只有 `escalate` 与 `ask_human`），所以缺的那一类叫「不花钱的」。
	if got.Reason != "no free action was offered, so the paid one is the only option and no margin applies" {
		t.Fatalf("the reason must name what was missing, got %q", got.Reason)
	}
	// ★ 而这一支也必须把 `Local` 报成那一条 —— 只有一个 offer 时它自己就是基线。
	if got.Chosen.Offer.Action != ActionEscalate || got.Local.Offer.Action != ActionEscalate {
		t.Fatalf("with one offer, chosen and local are both it: %v / %v",
			got.Chosen.Offer.Action, got.Local.Offer.Action)
	}
}

// ★★★ 「本地的」是**不花外面钱**的那些，而 `best` 是**花外面钱**的那一个。
//
// 此前这里把 `ActionSkill` 与 `ActionLocal` 一起算进「本地」，而 `best` 取的是
// `ranked[0]`（全体最好）。于是当一个 `skill` 动作就是全体最好时：
//
//	local = 那个 skill 动作（因为它也在「本地」集合里）
//	best  = 同一个 skill 动作
//	best.Utility <= local.Utility + margin   ->  **恒真**（margin > 0）
//
// 实测（margin 0.5，`local/cached` value 1 cost 0 latency 1 与
// `skill/paid` value 10 cost 1 latency 1）：chosen = skill/paid utility 9、
// local = skill/paid utility 9（**同一个**），而 reason 是
// 「the paid action does not win by the margin」—— **付费动作从来没被比过。**
//
// ★ 而那个 `||` 分支**没有被任何测试碰过**。
func TestASkillActionIsNotTheLocalBaseline(t *testing.T) {
	offers := []Offer{
		{Action: ActionLocal, Skill: "cached", TaskClass: "t", Value: 1, Cost: 0, Pinned: 1, UsePinned: true},
		{Action: ActionSkill, Skill: "paid", TaskClass: "t", Value: 10, Cost: 1, Pinned: 1, UsePinned: true},
	}
	got, ref := ShouldEscalate(offers, Table{}, 0.5)
	if ref != nil {
		t.Fatalf("refused: %s", ref.Reason)
	}
	// ★ 没有付费动作 ⇒ 那一支必须说「没有可比的付费动作」，而不是编一个比较。
	if got.Chosen.Offer.Action != ActionSkill {
		t.Fatalf("the best overall is the skill action, got %s", got.Chosen.Offer.Action)
	}
	if !strings.Contains(got.Reason, "no paid action was offered") {
		t.Fatalf("the reason must say there was nothing paid to compare, got %q", got.Reason)
	}
	// ★ 而**基线**必须是**不花外面钱**的那一个。这里两个都不花钱（`local` 与
	// `skill` 都不在 `{escalate, ask_human}` 里），所以基线是其中效用更高的
	// 那一个 —— 也就是 `skill/paid`。
	//
	// ★ 这正是这一条要钉住的：基线**不是**「被选中的那一个」的同义词，
	// 而是「花外面钱的集合之外最好的那一个」。此前那个 `||` 把它写成了
	// 一个按名字枚举的集合，于是当 `skill` 就是全体最好时，基线与赢家是同一个，
	// 比较恒真、而理由在说一件没发生的事。
	if spendsOutsideMoney(got.Local.Offer.Action) {
		t.Fatalf("the baseline must never be a paid action, got %s", got.Local.Offer.Action)
	}
	if got.Local.Offer.Action != ActionSkill {
		t.Fatalf("the best free action here is the skill one, got %s", got.Local.Offer.Action)
	}
	// ★ 而「没有付费动作可比」这件事必须**说清楚**，而不是编一个比较出来。
	if got.Chosen.Utility != got.Local.Utility {
		t.Fatalf("with no paid action there is nothing to beat: %v vs %v",
			got.Chosen.Utility, got.Local.Utility)
	}
}

// ★ 而真正该被比的那一对仍然要比：一个付费动作 vs 一个不花钱的。
func TestThePaidActionIsComparedAgainstTheFreeOne(t *testing.T) {
	offers := []Offer{
		{Action: ActionLocal, Skill: "cached", TaskClass: "t", Value: 1, Cost: 0, Pinned: 1, UsePinned: true},
		{Action: ActionSkill, Skill: "stored", TaskClass: "t", Value: 5, Cost: 0, Pinned: 1, UsePinned: true},
		{Action: ActionEscalate, Skill: "expert", TaskClass: "t", Value: 10, Cost: 1, Pinned: 1, UsePinned: true},
	}
	// escalate 的效用 = 10 - 1 = 9；不花钱的最好是 stored = 5。差 4 > 0.5 ⇒ 花钱。
	got, ref := ShouldEscalate(offers, Table{}, 0.5)
	if ref != nil {
		t.Fatalf("refused: %s", ref.Reason)
	}
	if got.Chosen.Offer.Action != ActionEscalate {
		t.Fatalf("9 beats 5 by more than 0.5, chose %s", got.Chosen.Offer.Action)
	}
	if got.Local.Offer.Action != ActionSkill {
		t.Fatalf("the baseline is the best free one (stored), got %s", got.Local.Offer.Action)
	}
	if got.Chosen.Utility <= got.Local.Utility {
		t.Fatalf("the paid one must actually be better: %v vs %v", got.Chosen.Utility, got.Local.Utility)
	}
}

// ---------- capex ----------

func TestThePoisonTermCanFlipTheDecision(t *testing.T) {
	// The same task, the same saving. The only difference is what evidence the
	// skill would be fixed at, and that is enough to change the answer.
	base := Fixing{
		Calls: 10000, OpexNow: 0.01, OpexAfter: 0.001,
		Capex: 5, ExpectedCalls: 200, Loss: 50,
	}
	base.At = evidence.L1
	risky, ok := FixValue(base)
	if !ok {
		t.Fatal("E1 is a known level")
	}
	base.At = evidence.L4
	safe, _ := FixValue(base)
	if risky >= safe {
		t.Fatalf("a better-evidenced skill must be worth more: E1=%.2f E4=%.2f", risky, safe)
	}
	if risky >= 0 {
		t.Fatalf("fixing at E1 should not pay here, got %.2f", risky)
	}
	if safe <= 0 {
		t.Fatalf("fixing at E4 should pay here, got %.2f", safe)
	}
	t.Logf("same saving, different evidence: E1=%.2f (refuse) vs E4=%.2f (fix)", risky, safe)
}

func TestBreakingEvenIsNotAReasonToFix(t *testing.T) {
	f := Fixing{Calls: 100, OpexNow: 0.02, OpexAfter: 0.0, Capex: 2.0, ExpectedCalls: 0, At: evidence.L3}
	v, ok := FixValue(f)
	if !ok || !close(v, 0, 1e-9) {
		t.Fatalf("the fixture should break even exactly, got %.6f", v)
	}
	if yes, _, _ := ShouldFix(f); yes {
		t.Fatal("a skill that costs exactly what it saves adds a moving part for no return")
	}
}

func TestUnknownEvidenceGivesNoNumber(t *testing.T) {
	_, ok := FixValue(Fixing{At: evidence.Level("E9"), Calls: 1000})
	if ok {
		t.Fatal("an unrecognised level is an uncalibrated level; the caller must not get a number")
	}
	if _, _, ok := ShouldFix(Fixing{At: evidence.Level("E9")}); ok {
		t.Fatal("ShouldFix must report that it could not answer")
	}
}

func TestCompileCriteriaGateOnMeasurements(t *testing.T) {
	c := DefaultCompileCriteria()
	good := Posterior{Alpha: 200, Beta: 1}
	if ShouldCompile(10, good, 0.9, c) {
		t.Fatal("ten calls is premature")
	}
	if ShouldCompile(100, good, 0.1, c) {
		t.Fatal("compiling when startup is 10% of the cost buys almost nothing")
	}
	if !ShouldCompile(100, good, 0.9, c) {
		t.Fatal("a well-used script behind a slow interpreter is exactly what should be compiled")
	}
	bad := Posterior{Alpha: 1, Beta: 5}
	if ShouldCompile(100, bad, 0.9, c) {
		t.Fatal("a script that fails must not be compiled into something harder to change")
	}
}

// ★ The two expectations rank differently, and neither is wrong.
//
//	A: p=0.99, cost=0.9    B: p=0.50, cost=0.1    value=1.5
//	per attempt: A = 0.585   B = 0.650  -> B wins
//	per success: A = 0.576   B = 0.550  -> A wins
//
// This is the whole reason the expectation is an explicit field: the same two
// offers swap places depending on which question was asked, and a caller that
// cannot say which one it meant cannot read the answer.
func TestTheTwoExpectationsRankDifferently(t *testing.T) {
	steady := Offer{Action: ActionSkill, Value: 1.5, Cost: 0.9, Pinned: 0.99, UsePinned: true}
	cheap := Offer{Action: ActionShadow, Value: 1.5, Cost: 0.1, Pinned: 0.5, UsePinned: true}

	byAttempt := Rank([]Offer{steady, cheap}, Table{})
	if byAttempt[0].Offer.Action != ActionShadow {
		t.Fatalf("priced per attempt, the cheap one should win: got %s", byAttempt[0].Offer.Action)
	}

	steady.Expect, cheap.Expect = ExpectSuccess, ExpectSuccess
	bySuccess := Rank([]Offer{steady, cheap}, Table{})
	if bySuccess[0].Offer.Action != ActionSkill {
		t.Fatalf("priced per success, the reliable one should win: got %s", bySuccess[0].Offer.Action)
	}
}

func TestAnEmptyExpectationMeansOneAttempt(t *testing.T) {
	o := Offer{Value: 10, Cost: 2}
	got, ok := ExpectedCost(o, Posterior{Alpha: 1, Beta: 1})
	if !ok || got != 2 {
		t.Fatalf("an offer written before this field existed must keep its meaning: got %v ok=%v", got, ok)
	}
}

func TestCostPerSuccessDividesByTheProbability(t *testing.T) {
	o := Offer{Value: 10, Cost: 1, Pinned: 0.5, UsePinned: true, Expect: ExpectSuccess}
	got, ok := ExpectedCost(o, Posterior{Alpha: 1, Beta: 1})
	if !ok {
		t.Fatal("a pinned probability of 0.5 is defined")
	}
	if got != 2 {
		t.Fatalf("cost per success with p=0.5 is 1/0.5 = 2, got %v", got)
	}
}

// ★ An action that never succeeds has no cost per success, because it buys
// nothing. Reporting a very large number there would be a lie of a different
// shape: it would look like a price.
func TestAnActionThatNeverSucceedsHasNoPrice(t *testing.T) {
	o := Offer{Value: 10, Cost: 1, Pinned: 0, UsePinned: true, Expect: ExpectSuccess}
	if _, ok := ExpectedCost(o, Posterior{Alpha: 1, Beta: 1}); ok {
		t.Fatal("p=0 has no cost per success and must say so rather than divide")
	}
}

func TestProbOfPrefersTheUpperBoundAndHonoursPinning(t *testing.T) {
	post := Posterior{Alpha: 3, Beta: 1}
	if got := ProbOf(Offer{}, post); got != post.UpperBound() {
		t.Fatalf("an unpinned offer is judged by the upper bound: got %v", got)
	}
	if got := ProbOf(Offer{Pinned: 0.4, UsePinned: true}, post); got != 0.4 {
		t.Fatalf("a pinned offer is judged by its pinned value: got %v", got)
	}
	// The upper bound is above the mean on purpose: choosing is not reporting,
	// and using the mean would make the system stop trying what it knows least
	// about.
	if post.UpperBound() <= post.Mean() {
		t.Fatalf("the upper bound must exceed the mean: %v <= %v", post.UpperBound(), post.Mean())
	}
}

// ★ RulesOf reports which judging standards a posterior was folded from.
//
// It exists because a posterior keyed by skill_id alone will happily average
// runs judged by two different assertion sets, and 0.5 can then mean either
// "one program that works half the time" or "two programs, one that always
// works and one that never does". Those are two facts and one number.
func TestRulesOfReportsEveryDistinctRuleSet(t *testing.T) {
	k := Key{Skill: "s", TaskClass: "task_exec"}
	other := Key{Skill: "t", TaskClass: "task_exec"}
	obs := []Observation{
		{Key: k, OK: true, Rules: "b"},
		{Key: k, OK: true, Rules: "a"},
		{Key: k, OK: false, Rules: "a"}, // a duplicate, not a third rule set
		{Key: other, OK: true, Rules: "c"},
	}
	got := RulesOf(obs)
	if len(got[k]) != 2 {
		t.Fatalf("want two distinct rule sets under k, got %v", got[k])
	}
	// Sorted, so two reads of the same chain render identically.
	if got[k][0] != "a" || got[k][1] != "b" {
		t.Fatalf("the identities must be sorted, got %v", got[k])
	}
	if len(got[other]) != 1 || got[other][0] != "c" {
		t.Fatalf("keys must not bleed into each other, got %v", got[other])
	}
}

// ★ A run recorded before the field existed is a third thing: not one rule set,
// not two, but unknown. It has to be visible as such — folding it in silently
// would either hide a real mixing or invent one.
func TestAnUnrecordedRuleSetIsItsOwnAnswer(t *testing.T) {
	k := Key{Skill: "s", TaskClass: "task_exec"}
	got := RulesOf([]Observation{{Key: k, OK: true}})
	if len(got[k]) != 1 || got[k][0] != "(unrecorded)" {
		t.Fatalf("an empty rules field must read as unrecorded, got %v", got[k])
	}
	// And it is genuinely distinct from a recorded one: an old run plus a new
	// one under a known rule set IS a mixture, and must be reported as one.
	mixed := RulesOf([]Observation{{Key: k, OK: true}, {Key: k, OK: true, Rules: "a"}})
	if len(mixed[k]) != 2 {
		t.Fatalf("an unrecorded run beside a recorded one is a mixture, got %v", mixed[k])
	}
}

func TestRulesOfAgreesWithTheTableItWasFoldedFrom(t *testing.T) {
	// Both projections come from the same slice, so every key in the table must
	// have a rule list — an empty one would mean a posterior from nowhere.
	obs := []Observation{
		{Key: Key{Skill: "a", TaskClass: "task_exec"}, OK: true, Rules: "r1"},
		{Key: Key{Skill: "b", TaskClass: "task_exec"}, OK: false, Rules: "r1"},
	}
	tbl := Fold(obs, DefaultDecay)
	rules := RulesOf(obs)
	for k := range tbl {
		if len(rules[k]) == 0 {
			t.Fatalf("key %v has a posterior but no rule identity", k)
		}
	}
	if len(rules) != len(tbl) {
		t.Fatalf("the two projections must cover the same keys: %d vs %d", len(rules), len(tbl))
	}
}
