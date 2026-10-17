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

// ★★★ 三个事实，三个答案。
//
// 此前无论为什么找不到候选都回同一个 budget_exhausted，理由是一句
// 「either at capacity or costs more than X」。实测（一个容量 1 的资源，
// 预算给足 1000000）它回的是
//
//	{"code":"budget_exhausted","retryable":false,
//	 "reason":"...every one is either at capacity or costs more than 1000000 money..."}
//
// 而那句话**是假的**：那个资源要 0 money / 0 tokens，调用方刚刚证明了它有
// 1000000。挡住它的是**容量**，而容量是暂时的 —— core/refusal.go:50 早就为
// 这件事准备了 CodeLeaseHeld: true（可重试），旁边还写着「the condition is
// temporary and the reason string names the wait」。
//
// **一个把容量说成预算的拒绝，把一个会自己变好的条件报成了不会。**
func TestCheapestSaysCapacityWhenCapacityIsWhatBlocks(t *testing.T) {
	rs := []Resource{res("core.0", KindLocal, 0, 0, 1)}
	busy := []Lease{{Resource: "core.0", Holder: "job.a", Expires: 5000}}
	_, ref := Cheapest(rs, core.Budget{MS: 1000}, core.Budget{Money: 1000000}, busy, 1000)
	if ref == nil {
		t.Fatal("the only resource is busy, so this cannot be priced")
	}
	if ref.Code != core.CodeLeaseHeld {
		t.Fatalf("want lease_held (the condition is temporary), got %s: %s", ref.Code, ref.Reason)
	}
	if !ref.Retryable {
		t.Fatal("a lease frees itself, so this must be retryable — a retry flag must never " +
			"promise a retry that can never succeed, and it must never deny one that can")
	}
	// 理由必须**点明等多久**：没有数字的「等一下」不可执行。
	if !contains(ref.Reason, "4000ms") {
		t.Fatalf("the reason must name the wait, got %q", ref.Reason)
	}
	// 而且它不能说这是预算问题。
	if contains(ref.Reason, "budget") && !contains(ref.Reason, "not a budget") {
		t.Fatalf("a capacity refusal must not blame the budget, got %q", ref.Reason)
	}
}

func TestCheapestSaysBudgetWhenBudgetIsWhatBlocks(t *testing.T) {
	rs := []Resource{res("gpu.far", KindRemote, 0.002, 0, 4)}
	_, ref := Cheapest(rs, core.Budget{MS: 1000}, core.Budget{Money: 1}, nil, 0)
	if ref == nil {
		t.Fatal("the resource costs more than the budget")
	}
	if ref.Code != core.CodeBudgetExhausted {
		t.Fatalf("want budget_exhausted, got %s", ref.Code)
	}
	if ref.Retryable {
		t.Fatal("waiting does not grow a budget, so this must not be retryable")
	}
	// 理由必须报出**最便宜的那一个要多少**，以及调用方有多少 ——
	// 一个不说差多少的「不够」不是一个可执行的拒绝。
	if !contains(ref.Reason, "2 money") || !contains(ref.Reason, "1 money") {
		t.Fatalf("the reason must name both sides of the gap, got %q", ref.Reason)
	}
}

// 一个资源都没登记是**第四个事实**：不是预算问题，也不是容量问题。
func TestCheapestSaysNothingIsRegisteredWhenNothingIs(t *testing.T) {
	_, ref := Cheapest(nil, core.Budget{MS: 1}, core.Budget{Money: 100}, nil, 0)
	if ref == nil {
		t.Fatal("an empty registry must refuse")
	}
	if ref.Code != core.CodeBadRequest {
		t.Fatalf("an empty registry is not a budget problem, got %s: %s", ref.Code, ref.Reason)
	}
	if !contains(ref.Reason, "no resource is registered") {
		t.Fatalf("the reason must say the registry is empty, got %q", ref.Reason)
	}
}

// 两个都挡着时：码取预算那一个（容量会自己好，预算不会），但理由必须把
// **两个**都说出来 —— 否则读的人只会去修其中一个。
func TestCheapestSaysBothWhenBothBlock(t *testing.T) {
	rs := []Resource{
		res("core.0", KindLocal, 0, 0, 1),
		res("gpu.far", KindRemote, 0.002, 0, 4),
	}
	busy := []Lease{{Resource: "core.0", Holder: "job.a", Expires: 5000}}
	_, ref := Cheapest(rs, core.Budget{MS: 1000}, core.Budget{Money: 1}, busy, 0)
	if ref == nil {
		t.Fatal("one is busy and the other costs too much")
	}
	if ref.Code != core.CodeBudgetExhausted {
		t.Fatalf("waiting frees the capacity but not the budget, so the code is budget_exhausted, got %s", ref.Code)
	}
	if ref.Retryable {
		t.Fatal("a retry cannot fix the budget")
	}
	if !contains(ref.Reason, "at capacity") || !contains(ref.Reason, "more than the budget") {
		t.Fatalf("both reasons must be in the answer, got %q", ref.Reason)
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
