package pool

import (
	"testing"

	"teps/core"
)

func res(id string, kind Kind, perMS, perTok float64, cap int) Resource {
	return Resource{ID: id, Kind: kind, MoneyPerMS: perMS, MoneyPerToken: perTok, Capacity: cap}
}

func TestValidateAcceptsTheDesignsOwnResources(t *testing.T) {
	cases := []Resource{
		{ID: "core.0", Kind: KindLocal, Capacity: 4, Note: "this machine"},
		// The thousand-machine premise: idle capacity that costs nothing.
		{ID: "pc.0421", Kind: KindLAN, Capacity: 1, Note: "idle 2% of the time"},
		{ID: "gpu.far", Kind: KindRemote, MoneyPerMS: 0.0001, Capacity: 2},
		{ID: "l3.tokens", Kind: KindTokens, MoneyPerToken: 0.00001, Capacity: 8},
	}
	for _, r := range cases {
		if ref := Validate(r); ref != nil {
			t.Fatalf("%s should be registrable, got %s: %s", r.ID, ref.Code, ref.Reason)
		}
	}
}

func TestValidateRefusesWhatCouldNeverBeSelected(t *testing.T) {
	// Every one of these registers fine if unchecked, and then the registry
	// looks populated while the scheduler behaves as if it were empty.
	cases := []struct {
		name string
		r    Resource
		want string
	}{
		{"no id", res("", KindLocal, 0, 0, 1), core.CodeBadRequest},
		{"id with a slash", res("a/b", KindLocal, 0, 0, 1), core.CodeBadRequest},
		{"id starting with a dot", res(".hidden", KindLocal, 0, 0, 1), core.CodeBadRequest},
		{"unknown kind", res("x", Kind("gpu"), 0, 0, 1), core.CodeBadRequest},
		{"zero capacity", res("x", KindLocal, 0, 0, 0), core.CodeBadRequest},
		{"negative capacity", res("x", KindLocal, 0, 0, -1), core.CodeBadRequest},
		{"negative price", res("x", KindLocal, -0.01, 0, 1), core.CodeBadRequest},
		{"negative token price", res("x", KindTokens, 0, -0.01, 1), core.CodeBadRequest},
	}
	for _, c := range cases {
		ref := Validate(c.r)
		if ref == nil {
			t.Fatalf("%s: should have been refused", c.name)
		}
		if ref.Code != c.want {
			t.Fatalf("%s: want %s, got %s", c.name, c.want, ref.Code)
		}
	}
}

func TestValidateExplainsWhyANegativePriceIsNotADiscount(t *testing.T) {
	ref := Validate(res("x", KindRemote, -0.5, 0, 1))
	if ref == nil {
		t.Fatal("a negative price must be refused")
	}
	// The reason has to say what actually goes wrong, or the next person
	// "fixes" it by clamping to zero and reintroduces the bug.
	if !contains(ref.Reason, "pays the caller") {
		t.Fatalf("the refusal should say a negative price pays the caller, got: %s", ref.Reason)
	}
}

func TestCostIsPerDimension(t *testing.T) {
	work := core.Budget{MS: 1000, Tokens: 500}
	// A GPU sells milliseconds and does not care about tokens.
	gpu := res("gpu", KindRemote, 0.002, 0, 1)
	if c := Cost(gpu, work); c.Money != 2 {
		t.Fatalf("1000ms at 0.002/ms is 2, got %d", c.Money)
	}
	// An external model sells tokens and does not care how long you waited.
	l3 := res("l3", KindTokens, 0, 0.004, 1)
	if c := Cost(l3, work); c.Money != 2 {
		t.Fatalf("500 tokens at 0.004/token is 2, got %d", c.Money)
	}
	// A free resource is a price of zero, not an absent price.
	if c := Cost(res("core", KindLocal, 0, 0, 1), work); c.Money != 0 {
		t.Fatalf("a local core is free, got %d", c.Money)
	}
	// The MS of a cost is zero: a resource consumes milliseconds, it does not
	// add them.
	if c := Cost(gpu, work); c.MS != 0 {
		t.Fatalf("a cost has no milliseconds of its own, got %d", c.MS)
	}
	// A resource that does not price tokens charges none of them. Charging the
	// work's tokens anyway makes every free resource unaffordable to a caller
	// whose token allowance is exhausted — the drive script found exactly this,
	// because `pool.quote` refused everything when the caller passed no tokens.
	if c := Cost(gpu, work); c.Tokens != 0 {
		t.Fatalf("a resource that does not price tokens charges none, got %d", c.Tokens)
	}
	if c := Cost(l3, work); c.Tokens != work.Tokens {
		t.Fatalf("a token-priced resource charges the work's tokens, got %d", c.Tokens)
	}
}

func TestAFreeResourceIsAffordableToAnExhaustedCaller(t *testing.T) {
	// The caller has money but no token allowance left. A free local core must
	// still be selectable: it takes nothing from either dimension.
	rs := []Resource{res("core.0", KindLocal, 0, 0, 1)}
	got, ref := Cheapest(rs, core.Budget{MS: 1000, Tokens: 500}, core.Budget{Money: 100}, nil, 0)
	if ref != nil {
		t.Fatalf("a free resource costs nothing in either dimension: %s", ref.Reason)
	}
	if got.ID != "core.0" {
		t.Fatalf("want core.0, got %s", got.ID)
	}
	// A token-priced resource is genuinely out of reach in that case, which is
	// what makes the distinction load-bearing rather than a convenience.
	l3 := []Resource{res("l3", KindTokens, 0, 0.001, 1)}
	if _, ref := Cheapest(l3, core.Budget{Tokens: 500}, core.Budget{Money: 100}, nil, 0); ref == nil {
		t.Fatal("a token-priced resource must not be affordable with no token allowance")
	}
}

func TestCostOfNegativeWorkIsZeroNotNegative(t *testing.T) {
	// Negative work would make a paid resource pay the caller, which is the
	// same hazard as a negative price arriving by a different door.
	if c := Cost(res("gpu", KindRemote, 0.002, 0, 1), core.Budget{MS: -1000}); c.Money != 0 {
		t.Fatalf("negative work must not produce negative money, got %d", c.Money)
	}
}

func TestLiveExcludesExpiredAndOtherResources(t *testing.T) {
	ls := []Lease{
		{Resource: "a", Holder: "h1", Expires: 200},
		{Resource: "a", Holder: "h2", Expires: 100},
		{Resource: "b", Holder: "h3", Expires: 200},
	}
	live := Live(ls, "a", 150)
	if len(live) != 1 || live[0].Holder != "h1" {
		t.Fatalf("want only h1 live on a, got %v", live)
	}
	// The boundary matters: a lease that expires exactly now is not live, or
	// capacity would come back one tick late forever.
	if got := Live(ls, "a", 200); len(got) != 0 {
		t.Fatalf("a lease expiring at now is expired, got %v", got)
	}
}

func TestAcquireTakesASlotAndRefusesTheNextOneByName(t *testing.T) {
	r := res("core.0", KindLocal, 0, 0, 1)
	l, ref := Acquire(r, nil, "job.a", 1000, 500)
	if ref != nil {
		t.Fatalf("the first lease should succeed: %s", ref.Reason)
	}
	if l.Expires != 1500 {
		t.Fatalf("expiry is now+ttl, got %d", l.Expires)
	}
	_, ref = Acquire(r, []Lease{l}, "job.b", 1000, 500)
	if ref == nil {
		t.Fatal("a second lease on a capacity-1 resource must be refused")
	}
	if ref.Code != core.CodeLeaseHeld {
		t.Fatalf("want lease_held, got %s", ref.Code)
	}
	// "The pool is full" is not actionable. Naming the holder is the only part
	// of this a human will ever read.
	if !contains(ref.Reason, "job.a") {
		t.Fatalf("the refusal must name who holds the slot, got: %s", ref.Reason)
	}
}

func TestAnExpiredLeaseGivesTheCapacityBack(t *testing.T) {
	// ★ The property that matters: a holder that dies does not release, so
	// without expiry the pool quietly becomes a smaller pool for no stated
	// reason.
	r := res("core.0", KindLocal, 0, 0, 1)
	dead := Lease{Resource: "core.0", Holder: "job.crashed", Expires: 1500}
	if _, ref := Acquire(r, []Lease{dead}, "job.b", 1000, 500); ref == nil {
		t.Fatal("while the lease is live, the slot is taken")
	}
	l, ref := Acquire(r, []Lease{dead}, "job.b", 1500, 500)
	if ref != nil {
		t.Fatalf("after it expires the slot is free again, got %s: %s", ref.Code, ref.Reason)
	}
	if l.Holder != "job.b" {
		t.Fatalf("want the new holder, got %s", l.Holder)
	}
}

func TestAcquireRefusesWhatCouldNotBeReleasedOrWouldNeverEnd(t *testing.T) {
	r := res("core.0", KindLocal, 0, 0, 1)
	if _, ref := Acquire(r, nil, "", 0, 100); ref == nil {
		t.Fatal("an anonymous lease can never be released")
	}
	if _, ref := Acquire(r, nil, "job.a", 0, 0); ref == nil {
		t.Fatal("a lease with no ttl leaks capacity forever")
	}
	if _, ref := Acquire(r, nil, "job.a", 0, -1); ref == nil {
		t.Fatal("a negative ttl is not a shorter lease")
	}
}

func TestReleaseIsByHolderAcrossResources(t *testing.T) {
	ls := []Lease{
		{Resource: "a", Holder: "job.a", Expires: 200},
		{Resource: "b", Holder: "job.a", Expires: 200},
		{Resource: "a", Holder: "job.b", Expires: 200},
	}
	out := Release(ls, "job.a")
	if len(out) != 1 || out[0].Holder != "job.b" {
		t.Fatalf("releasing job.a must drop both of its leases and nothing else, got %v", out)
	}
	// A finishing job knows who it is, not every slot it took; releasing an
	// unknown holder is a no-op rather than an error, because a retry after a
	// successful release must not fail.
	if got := Release(out, "job.nobody"); len(got) != 1 {
		t.Fatalf("releasing an unknown holder must change nothing, got %v", got)
	}
}

func TestCheapestPicksTheLeastMoneyThatFits(t *testing.T) {
	rs := []Resource{
		res("gpu.far", KindRemote, 0.002, 0, 1),
		res("core.0", KindLocal, 0, 0, 4),
		res("pc.0421", KindLAN, 0, 0, 1),
	}
	work := core.Budget{MS: 1000, Tokens: 100}
	got, ref := Cheapest(rs, work, core.Budget{Money: 100, Tokens: 1000}, nil, 0)
	if ref != nil {
		t.Fatalf("a free resource is available: %s", ref.Reason)
	}
	// Both free ones are equally cheap, so priority decides — and with equal
	// priority the id does, so the answer does not depend on registry order.
	if got.ID != "core.0" {
		t.Fatalf("want core.0 (lowest id among equals), got %s", got.ID)
	}
}

func TestCheapestPrefersPriorityAmongEqualCosts(t *testing.T) {
	a := res("aaa", KindLAN, 0, 0, 1)
	b := res("bbb", KindLAN, 0, 0, 1)
	b.Priority = 5
	got, ref := Cheapest([]Resource{a, b}, core.Budget{MS: 10}, core.Budget{Money: 0}, nil, 0)
	if ref != nil {
		t.Fatalf("unexpected refusal: %s", ref.Reason)
	}
	if got.ID != "bbb" {
		t.Fatalf("priority should break a cost tie, got %s", got.ID)
	}
}

func TestCheapestSkipsResourcesWithNoLiveCapacity(t *testing.T) {
	// A price you cannot pay is not a cheaper price.
	rs := []Resource{res("core.0", KindLocal, 0, 0, 1), res("gpu.far", KindRemote, 0.001, 0, 4)}
	busy := []Lease{{Resource: "core.0", Holder: "job.a", Expires: 9999}}
	got, ref := Cheapest(rs, core.Budget{MS: 1000}, core.Budget{Money: 100}, busy, 0)
	if ref != nil {
		t.Fatalf("the paid one is free right now: %s", ref.Reason)
	}
	if got.ID != "gpu.far" {
		t.Fatalf("the free one is busy, so the paid one is the only candidate, got %s", got.ID)
	}
	// And the moment the free one comes back it wins again.
	got, _ = Cheapest(rs, core.Budget{MS: 1000}, core.Budget{Money: 100}, busy, 9999)
	if got.ID != "core.0" {
		t.Fatalf("after the lease expires the free resource wins again, got %s", got.ID)
	}
}

func TestCheapestRefusesWhenNothingFits(t *testing.T) {
	rs := []Resource{res("gpu.far", KindRemote, 0.002, 0, 1)}
	_, ref := Cheapest(rs, core.Budget{MS: 1000}, core.Budget{Money: 1}, nil, 0)
	if ref == nil {
		t.Fatal("a resource costing more than the budget is not a candidate")
	}
	if ref.Code != core.CodeBudgetExhausted {
		t.Fatalf("want budget_exhausted, got %s", ref.Code)
	}
	if _, ref := Cheapest(nil, core.Budget{MS: 1}, core.Budget{Money: 0}, nil, 0); ref == nil {
		t.Fatal("an empty registry must refuse, not return a zero resource")
	}
}

func TestCheapestIsDeterministicAcrossInputOrder(t *testing.T) {
	a := res("aaa", KindLAN, 0.001, 0, 1)
	b := res("bbb", KindLAN, 0.001, 0, 1)
	c := res("ccc", KindLAN, 0.001, 0, 1)
	budget := core.Budget{Money: 100}
	first, _ := Cheapest([]Resource{a, b, c}, core.Budget{MS: 100}, budget, nil, 0)
	for _, order := range [][]Resource{{c, b, a}, {b, a, c}, {c, a, b}} {
		got, _ := Cheapest(order, core.Budget{MS: 100}, budget, nil, 0)
		if got.ID != first.ID {
			t.Fatalf("the answer must not depend on registry order: %s vs %s", got.ID, first.ID)
		}
	}
	if first.ID != "aaa" {
		t.Fatalf("all equal, so the lowest id wins, got %s", first.ID)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
