package evidence

import "testing"

func TestEveryLevelHasARank(t *testing.T) {
	for i, l := range []Level{L0, L1, L2, L3, L4} {
		got, ok := Rank(l)
		if !ok {
			t.Fatalf("%s must be known", l)
		}
		if got != i {
			t.Fatalf("%s: want rank %d, got %d", l, i, got)
		}
	}
}

// An unknown level must not be folded into rank 0. Rank 0 is a real level (E0,
// "generated and unverified"), so mapping "E9" onto it would turn a typo into a
// trust decision — and the wrong direction of trust, since E0 is the bottom.
func TestUnknownLevelHasNoRankAndIsNotZero(t *testing.T) {
	for _, bad := range []Level{"E9", "", "e1", "E1 ", "verified"} {
		got, ok := Rank(bad)
		if ok {
			t.Fatalf("%q must not be known", bad)
		}
		if got != 0 {
			t.Fatalf("%q: want rank 0 with ok=false, got %d", bad, got)
		}
	}
}

func TestKnownAgreesWithRank(t *testing.T) {
	for _, l := range []Level{L0, L1, L2, L3, L4, "E9", ""} {
		_, ok := Rank(l)
		if Known(l) != ok {
			t.Fatalf("%q: Known and Rank disagree", l)
		}
	}
}

// The unknown level must propagate, not be sorted away. Washing an
// unrecognised level into a recognised one is the same failure as folding it
// into rank 0, just through a different door.
func TestMinPropagatesUnknown(t *testing.T) {
	if got := Min("E9", L4); Known(got) {
		t.Fatalf("min of an unknown level must stay unknown, got %q", got)
	}
	if got := Min(L0, "E9"); Known(got) {
		t.Fatalf("min of an unknown level must stay unknown, got %q", got)
	}
}

func TestMinIsTheLowerRank(t *testing.T) {
	if got := Min(L4, L1); got != L1 {
		t.Fatalf("want E1, got %q", got)
	}
	if got := Min(L1, L4); got != L1 {
		t.Fatalf("min must not depend on argument order, got %q", got)
	}
	if got := Min(L2, L2); got != L2 {
		t.Fatalf("min must be idempotent, got %q", got)
	}
}

// The P2 bug, pinned: a fold that starts from min's identity element can only
// return the identity element. E0 IS the identity for min, so starting there
// made MinAll always return E0.
func TestMinAllStartsFromTheFirstElementNotTheIdentity(t *testing.T) {
	if got := MinAll([]Level{L4, L4, L4}); got != L4 {
		t.Fatalf("all-E4 must stay E4, got %q (the identity-element bug)", got)
	}
	if got := MinAll([]Level{L3, L4, L2}); got != L2 {
		t.Fatalf("want E2, got %q", got)
	}
	if got := MinAll([]Level{L4}); got != L4 {
		t.Fatalf("a single element must survive, got %q", got)
	}
}

func TestMinAllEmptyIsE0(t *testing.T) {
	// The empty case is the one place the identity element is the right answer:
	// there is no evidence at all.
	if got := MinAll(nil); got != L0 {
		t.Fatalf("empty must be E0, got %q", got)
	}
}

func TestMinAllPropagatesUnknown(t *testing.T) {
	if got := MinAll([]Level{L4, "E9"}); Known(got) {
		t.Fatalf("an unknown member must make the whole result unknown, got %q", got)
	}
}

// Both sides fail closed. An unrecognised floor must not let everything
// through, and an unrecognised value must not be admitted by a recognised floor.
func TestAtLeastFailsClosedOnEitherUnknown(t *testing.T) {
	if AtLeast(L4, "E9") {
		t.Fatal("an unknown floor must not admit anything")
	}
	if AtLeast("E9", L0) {
		t.Fatal("an unknown value must not be admitted by any floor")
	}
	if AtLeast("E9", "E9") {
		t.Fatal("two unknowns are still not a comparison")
	}
}

func TestAtLeastIsInclusive(t *testing.T) {
	if !AtLeast(L1, L1) {
		t.Fatal("a level must be at least itself")
	}
	if !AtLeast(L4, L1) {
		t.Fatal("E4 is at least E1")
	}
	if AtLeast(L0, L1) {
		t.Fatal("E0 is not at least E1")
	}
}
