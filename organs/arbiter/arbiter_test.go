package arbiter

import (
	"math"
	"testing"

	"sansheng/evidence"
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
	goodThenBad := Fold([]Observation{{k, true}, {k, true}, {k, true}, {k, false}}, DefaultDecay)
	badThenGood := Fold([]Observation{{k, false}, {k, true}, {k, true}, {k, true}}, DefaultDecay)
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

func TestUpperBoundShrinksWithEvidence(t *testing.T) {
	once := Posterior{Alpha: 2, Beta: 1}.UpperBound()
	many := Posterior{Alpha: 100, Beta: 50}.UpperBound()
	if many >= once {
		t.Fatalf("the bound must tighten as evidence accumulates: once=%.4f many=%.4f", once, many)
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
	if got.Reason != "no local action was offered" {
		t.Fatalf("the reason must name what was missing, got %q", got.Reason)
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
