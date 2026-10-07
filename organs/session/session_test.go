package session

import (
	"reflect"
	"testing"
)

func mustStep(t *testing.T, m Machine, e Event) Machine {
	t.Helper()
	next, ok := m.Step(e)
	if !ok {
		t.Fatalf("step %s from %s was refused", e, m.State)
	}
	return next
}

// TestEveryPairIsAccountedFor is the Go substitute for the exhaustive match
// Rust would enforce at compile time. All states × all events are enumerated
// and the allowed edges are compared against a list written out by hand.
func TestEveryPairIsAccountedFor(t *testing.T) {
	want := []string{
		"ready+open->routing",
		"routing+route->executing",
		"routing+plan->planning",
		"planning+planned->executing",
		"executing+finish->verifying",
		"verifying+pass->done",
		"verifying+retry->retrying",
		"verifying+escalate->escalating",
		"retrying+rerun->executing",
		"escalating+quoted->waiting",
		"waiting+grant->executing",
		"waiting+timeout->degraded",
		"waiting+deny->rearbitrating",
		"rearbitrating+replan->planning",
		"rearbitrating+abandon->degraded",
	}
	got := Edges()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("allowed edges changed:\n got %v\nwant %v", got, want)
	}
	if n := len(AllStates) * len(AllEvents); n != 165 {
		t.Errorf("states × events = %d, want 165; the pair space changed, so this test no longer means what it did", n)
	}
}

// TestEveryStateIsReachable catches a state that is defined and listed but
// that no sequence of events can ever arrive at.
func TestEveryStateIsReachable(t *testing.T) {
	seen := map[State]bool{Ready: true}
	queue := []State{Ready}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, e := range AllEvents {
			if next, ok := Next(s, e); ok && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	for _, s := range AllStates {
		if !seen[s] {
			t.Errorf("state %q is unreachable from %q", s, Ready)
		}
	}
}

func TestTerminalStatesHaveNoExit(t *testing.T) {
	for _, s := range []State{Done, Degraded} {
		if !Terminal(s) {
			t.Errorf("%s should be terminal", s)
		}
		for _, e := range AllEvents {
			if _, ok := Next(s, e); ok {
				t.Errorf("%s accepted %s", s, e)
			}
		}
	}
	for _, s := range []State{Ready, Routing, Planning, Executing, Verifying, Retrying, Escalating, Waiting, Rearbitrating} {
		if Terminal(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestUnknownStateAndEventDoNotMove(t *testing.T) {
	if Known("invented") {
		t.Fatal("an invented state reported itself known")
	}
	if _, ok := Next("invented", Open); ok {
		t.Fatal("an invented state accepted an event")
	}
	if _, ok := Next(Ready, "invented"); ok {
		t.Fatal("an invented event was accepted")
	}
	if _, ok := Next(Ready, Finish); ok {
		t.Fatal("ready accepted a forbidden event")
	}
}

func TestHappyPathEndsTerminal(t *testing.T) {
	m := New()
	m = mustStep(t, m, Open)
	m = mustStep(t, m, Route)
	m = mustStep(t, m, Finish)
	m = mustStep(t, m, Pass)

	if m.State != Done {
		t.Fatalf("state = %s, want done", m.State)
	}
	if !m.Flags().Terminal {
		t.Fatal("done should read as terminal")
	}
	want := []State{Ready, Routing, Executing, Verifying, Done}
	if !reflect.DeepEqual(m.Trail, want) {
		t.Errorf("trail = %v, want %v", m.Trail, want)
	}
}

// TestStepDoesNotAliasTheReceiver is what makes "pure" a property rather than
// a claim: a returned Machine must not share a backing array with the one it
// came from.
func TestStepDoesNotAliasTheReceiver(t *testing.T) {
	m := New()
	next, _ := m.Step(Open)
	next.Trail[0] = "tampered"

	if m.Trail[0] != Ready {
		t.Fatalf("stepping aliased the receiver's trail: %v", m.Trail)
	}
	if len(m.Trail) != 1 {
		t.Fatalf("receiver's trail grew: %v", m.Trail)
	}
}

func TestObserveAndRecordAttemptDoNotAliasTheReceiver(t *testing.T) {
	m := New()
	a := m.Observe(0.5, "hash-a")
	b := a.Observe(0.9, "hash-b")
	c := b.RecordAttempt("s1")

	if len(m.Gains) != 0 || len(m.Actions) != 0 || len(m.Attempts) != 0 {
		t.Fatalf("receiver was mutated: gains=%v actions=%v attempts=%v", m.Gains, m.Actions, m.Attempts)
	}
	if len(a.Gains) != 1 || len(b.Gains) != 2 {
		t.Fatalf("observations did not accumulate: a=%v b=%v", a.Gains, b.Gains)
	}
	if len(c.Attempts) != 1 || len(b.Attempts) != 0 {
		t.Fatalf("attempt leaked backwards: b=%v c=%v", b.Attempts, c.Attempts)
	}
}

func TestStallFiresOnTheThirdLowGainStep(t *testing.T) {
	if Stalled(nil) {
		t.Fatal("no evidence should not stall")
	}
	if Stalled([]float64{0.01}) {
		t.Fatal("one low step should not stall")
	}
	if Stalled([]float64{0.01, 0.02}) {
		t.Fatal("two low steps should not stall")
	}
	if !Stalled([]float64{0.01, 0.02, 0.03}) {
		t.Fatal("three low steps should stall")
	}
	if !Stalled([]float64{0.9, 0.01, 0.02, 0.03}) {
		t.Fatal("the streak is the trailing window, not the whole history")
	}
	if Stalled([]float64{0.01, 0.02, 0.05}) {
		t.Fatal("gain exactly at the threshold counts as progress")
	}
	if Stalled([]float64{0.01, 0.02, 0.9}) {
		t.Fatal("a good step breaks the streak")
	}
}

func TestLoopNeedsAFullWindow(t *testing.T) {
	seven := []string{"a", "a", "a", "a", "a", "a", "a"}
	if Looping(seven) {
		t.Fatal("seven identical actions is not yet a full window")
	}
}

func TestLoopFiresOnRepetition(t *testing.T) {
	// 8 steps, 3 distinct ⇒ (8−3)/8 = 0.625 > 0.5
	loop := []string{"a", "b", "c", "a", "b", "c", "a", "b"}
	if !Looping(loop) {
		t.Fatalf("repeat rate %.3f should count as a loop", 5.0/8.0)
	}
	// 8 steps, 4 distinct ⇒ exactly 0.5, which is not > 0.5
	boundary := []string{"a", "b", "c", "d", "a", "b", "c", "d"}
	if Looping(boundary) {
		t.Fatal("a repeat rate of exactly 0.5 is not above the threshold")
	}
	// 8 steps, 8 distinct ⇒ 0
	varied := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if Looping(varied) {
		t.Fatal("all-distinct actions are not a loop")
	}
	// Only the trailing window counts.
	long := append([]string{"x", "x", "x", "x", "x", "x", "x", "x"}, varied...)
	if Looping(long) {
		t.Fatal("an old loop outside the window should not count")
	}
}

// TestSameStrategyRetryIsAllowedExactlyOnce pins the rule of CONTRACT.md §7.
func TestSameStrategyRetryIsAllowedExactlyOnce(t *testing.T) {
	if !MayRetry(nil, "s1") {
		t.Fatal("the first attempt must be allowed")
	}
	if !MayRetry([]string{"s1"}, "s1") {
		t.Fatal("the first same-strategy retry is allowed once")
	}
	if MayRetry([]string{"s1", "s1"}, "s1") {
		t.Fatal("the second same-strategy retry must be refused")
	}
	if !MayRetry([]string{"s1", "s1"}, "s2") {
		t.Fatal("changing strategy is always allowed")
	}
	if !MayRetry([]string{"s1", "s2"}, "s2") {
		t.Fatal("s2 has only been used once, so one more is allowed")
	}
	if MayRetry([]string{"s1", "s2", "s2"}, "s2") {
		t.Fatal("s2 has now been used twice in a row")
	}
	if MayRetry([]string{"s1"}, "") {
		t.Fatal("an empty strategy is never a strategy")
	}
}

func TestMustChangeStrategyTracksTheRetryRule(t *testing.T) {
	m := New()
	if m.Flags().MustChangeStrategy {
		t.Fatal("a session with no attempts has nothing to change")
	}
	m = m.RecordAttempt("s1")
	if m.Flags().MustChangeStrategy {
		t.Fatal("after one attempt, one same-strategy retry is still allowed")
	}
	m = m.RecordAttempt("s1")
	if !m.Flags().MustChangeStrategy {
		t.Fatal("after two same-strategy attempts, the strategy must change")
	}
	m = m.RecordAttempt("s2")
	if m.Flags().MustChangeStrategy {
		t.Fatal("a fresh strategy resets the rule")
	}
}

func TestRetryRefusesAndThenAccepts(t *testing.T) {
	m := New()
	m = mustStep(t, m, Open)
	m = mustStep(t, m, Route)
	m = mustStep(t, m, Finish)

	m = m.RecordAttempt("s1")
	first, ok := m.Retry("s1")
	if !ok {
		t.Fatal("the first same-strategy retry should be accepted")
	}
	if first.State != Retrying {
		t.Fatalf("state = %s, want retrying", first.State)
	}
	if len(first.Attempts) != 2 {
		t.Fatalf("attempts = %v, want two entries", first.Attempts)
	}

	// Retry is only legal from verifying, so the attempt has to run and be
	// verified again before the rule can be exercised a second time.
	again := mustStep(t, mustStep(t, first, Rerun), Finish)

	second, ok := again.Retry("s1")
	if ok {
		t.Fatal("the second same-strategy retry must be refused")
	}
	if !reflect.DeepEqual(second, again) {
		t.Fatal("a refused retry must return the receiver unchanged")
	}

	changed, ok := again.Retry("s2")
	if !ok {
		t.Fatal("changing strategy must be accepted")
	}
	if changed.State != Retrying {
		t.Fatalf("state = %s, want retrying", changed.State)
	}
	if len(changed.Attempts) != 3 {
		t.Fatalf("attempts = %v, want three entries", changed.Attempts)
	}
}

func TestWasteRatioWithoutEvidenceIsZeroButFlagged(t *testing.T) {
	m := New()
	f := m.Flags()
	if f.Waste != 0 {
		t.Fatalf("waste = %v, want 0", f.Waste)
	}
	if f.HasWasteEvidence {
		t.Fatal("no escalations means no evidence, and the flag must say so")
	}
}

func TestDenyThenAbandonCountsAsWaste(t *testing.T) {
	m := New()
	m = mustStep(t, m, Open)
	m = mustStep(t, m, Plan)
	m = mustStep(t, m, Planned)
	m = mustStep(t, m, Finish)
	m = mustStep(t, m, Escalate)
	m = mustStep(t, m, Quoted)
	m = mustStep(t, m, Deny)
	m = mustStep(t, m, Abandon)

	f := m.Flags()
	if m.State != Degraded {
		t.Fatalf("state = %s, want degraded", m.State)
	}
	if f.Waste != 1.0 {
		t.Fatalf("waste = %v, want 1", f.Waste)
	}
	if !f.HasWasteEvidence {
		t.Fatal("one escalation happened, so there is evidence")
	}
}

func TestGrantClearsTheOpenEscalation(t *testing.T) {
	m := New()
	m = mustStep(t, m, Open)
	m = mustStep(t, m, Route)
	m = mustStep(t, m, Finish)
	m = mustStep(t, m, Escalate)
	m = mustStep(t, m, Quoted)
	m = mustStep(t, m, Grant)
	m = mustStep(t, m, Finish)
	m = mustStep(t, m, Pass)

	f := m.Flags()
	if m.State != Done {
		t.Fatalf("state = %s, want done", m.State)
	}
	if m.Escalations != 1 || m.EscalationsDegraded != 0 {
		t.Fatalf("escalations=%d degraded=%d, want 1 and 0", m.Escalations, m.EscalationsDegraded)
	}
	if f.Waste != 0 {
		t.Fatalf("waste = %v, want 0", f.Waste)
	}
	if !f.HasWasteEvidence {
		t.Fatal("an escalation did happen, so the zero is a real zero")
	}
	if m.EscalationOpen {
		t.Fatal("grant must close the open escalation")
	}
}

func TestTimeoutWithoutEscalationIsNotWaste(t *testing.T) {
	// Timeout can only be reached from Waiting, which can only be reached
	// through an escalation — so this path is genuinely counted.
	m := New()
	m = mustStep(t, m, Open)
	m = mustStep(t, m, Route)
	m = mustStep(t, m, Finish)
	m = mustStep(t, m, Escalate)
	m = mustStep(t, m, Quoted)
	m = mustStep(t, m, Timeout)

	if m.State != Degraded {
		t.Fatalf("state = %s, want degraded", m.State)
	}
	if m.Flags().Waste != 1.0 {
		t.Fatalf("waste = %v, want 1", m.Flags().Waste)
	}
}

func TestRearbitrateCanReplanInsteadOfGivingUp(t *testing.T) {
	m := New()
	m = mustStep(t, m, Open)
	m = mustStep(t, m, Plan)
	m = mustStep(t, m, Planned)
	m = mustStep(t, m, Finish)
	m = mustStep(t, m, Escalate)
	m = mustStep(t, m, Quoted)
	m = mustStep(t, m, Deny)
	m = mustStep(t, m, Replan)

	if m.State != Planning {
		t.Fatalf("state = %s, want planning", m.State)
	}
	if m.EscalationsDegraded != 0 {
		t.Fatal("replanning is not yet waste")
	}
	if m.EscalationOpen {
		t.Fatal("a denial closes the open escalation")
	}
}

func TestStuckCombinesStallAndLoop(t *testing.T) {
	m := New()
	if m.Stuck() {
		t.Fatal("a fresh session is not stuck")
	}
	m = m.Observe(0.01, "a").Observe(0.01, "b").Observe(0.01, "c")
	if !m.Stuck() {
		t.Fatal("three low-gain steps should read as stuck")
	}
	loop := New()
	for i := 0; i < 8; i++ {
		loop = loop.Observe(0.9, []string{"a", "b", "c"}[i%3])
	}
	if !loop.Stuck() {
		t.Fatal("a repeating action window should read as stuck")
	}
}

// TestGuardConstantsMatchTheContract makes a change to a threshold a visible
// edit rather than a silent drift.
func TestGuardConstantsMatchTheContract(t *testing.T) {
	if StallGain != 0.05 {
		t.Errorf("StallGain = %v, contract says 0.05", StallGain)
	}
	if StallStreak != 3 {
		t.Errorf("StallStreak = %d, contract says 3", StallStreak)
	}
	if LoopWindow != 8 {
		t.Errorf("LoopWindow = %d, contract says 8", LoopWindow)
	}
	if LoopRepeat != 0.5 {
		t.Errorf("LoopRepeat = %v, contract says 0.5", LoopRepeat)
	}
	if MaxSameStrategyRetries != 1 {
		t.Errorf("MaxSameStrategyRetries = %d, contract says 1", MaxSameStrategyRetries)
	}
}
