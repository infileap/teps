package executor

import "testing"

// ★ The rule identity answers "were these two runs judged by the same
// standard", and nothing else.
//
// It deliberately ignores order: every assertion must hold, so reordering them
// cannot change a verdict. An identity that tracked the text instead of the
// answer would report every reformat as a new rule set, and the signal it
// exists to give would be drowned in noise.
func TestRuleIDTracksTheAnswerNotTheText(t *testing.T) {
	a := RuleID([]string{"exit == 0"})
	b := RuleID([]string{"exit == 0", `stdout contains "RUNNER-OK"`})
	if a == b {
		t.Fatal("different assertions must give different identities")
	}
	if len(a) != 64 {
		t.Fatalf("the identity is a sha256 hex digest, got %q", a)
	}
	if RuleID([]string{"x", "y"}) != RuleID([]string{"y", "x"}) {
		t.Fatal("order must not matter: every assertion has to hold, so reordering changes nothing")
	}
	// Without the separator, ["ab","c"] and ["a","bc"] would collide — and a
	// collision here means two different rule sets share one posterior, which
	// is the whole defect this identity exists to expose.
	if RuleID([]string{"ab", "c"}) == RuleID([]string{"a", "bc"}) {
		t.Fatal("the separator is load-bearing")
	}
}

func TestRuleIDIsAFunctionOfItsInput(t *testing.T) {
	in := []string{"exit == 0", `stdout contains "X"`}
	if RuleID(in) != RuleID(in) {
		t.Fatal("the same assertions must give the same identity, or every run looks like a new rule set")
	}
	cp := append([]string(nil), in...)
	if RuleID(in) != RuleID(cp) {
		t.Fatal("two equal slices must give equal identities")
	}
	// A single empty assertion is still a rule set, and it must not collide
	// with the empty set: "nothing was asserted" and "an empty assertion was
	// asserted" are different declarations.
	if RuleID([]string{""}) == RuleID(nil) {
		t.Fatal("an empty assertion is not the same as no assertion")
	}
}
