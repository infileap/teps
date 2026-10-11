// Package pool is the compute registry: what there is to run on, what it costs,
// and how much of it is busy.
//
// The project's aim is a product of three factors — intelligence × compute ×
// skill. Until now only two of them had any representation: a skill is a
// manifest, intelligence is an arbiter offer, and compute was whatever number
// the caller happened to put in a cost field. This package gives the third
// factor a name, a price and a limit.
//
// Four decisions are worth stating, because each one could reasonably have gone
// the other way.
//
//  1. **A price is in the same units as the budget.** Work is measured in
//     milliseconds and tokens — that is what a skill declares and what the
//     kernel's Account spends — so a resource charges money per millisecond and
//     money per token. Pricing compute in anything else would have the arbiter
//     adding different currencies.
//
//  2. **Capacity is leased, not counted.** A resource that says "two
//     concurrent" is a hope until something refuses the third. The refusal has
//     to be a value, and it has to name who is holding the slot — otherwise the
//     operator is told the pool is full and not who to ask.
//
//  3. **A lease expires.** Without an expiry, a holder that dies leaks capacity
//     forever and the pool quietly becomes a smaller pool for no stated reason.
//     An expired lease is not a lease.
//
//  4. **A free resource is a price, not an absence of one.** The original
//     design's whole premise is that idle capacity on a thousand machines costs
//     nothing. That is a claim about the world, so it is written down as a
//     price of zero with a note — not as a missing field.
//
// This package is pure: no files, no clock, no processes. Time is passed in.
package pool

import (
	"strings"

	"teps/core"
)

// Kind is where a resource lives.
//
// These are named by where the work actually happens, not by a tier number.
// The tier vocabulary (L0/L1/L2/L3) was rejected in the arbiter for the same
// reason: it does not answer "what does it cost and who pays".
type Kind string

const (
	// KindLocal is this machine. It is the only kind that needs no network.
	KindLocal Kind = "local"
	// KindLAN is another machine reachable directly. This is the kind the
	// thousand-machine design is made of: idle capacity, no money.
	KindLAN Kind = "lan"
	// KindRemote is somewhere else, reached through something that can fail.
	KindRemote Kind = "remote"
	// KindTokens is an external model paid per token. It has no milliseconds to
	// sell — its work is measured in tokens.
	KindTokens Kind = "tokens"
)

// AllKinds is every kind, in a fixed order so callers can be deterministic.
var AllKinds = []Kind{KindLocal, KindLAN, KindRemote, KindTokens}

// Resource is one place work can run.
type Resource struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// MoneyPerMS and MoneyPerToken are the price, in money per unit of work.
	// Zero is a price, not a missing value: a local core is genuinely free.
	MoneyPerMS    float64 `json:"money_per_ms"`
	MoneyPerToken float64 `json:"money_per_token"`
	// Capacity is how many leases may be live at once. Below one there is
	// nothing to lease.
	Capacity int `json:"capacity"`
	// Priority breaks ties between resources of equal cost. This is the points
	// system from the original design: a machine that has been idle longest can
	// be preferred without changing what anything costs.
	Priority int `json:"priority"`
	// Note is why this resource is priced the way it is. A free resource with
	// no note is an unpriced one wearing a zero.
	Note string `json:"note,omitempty"`
}

// ValidID reports whether an id is usable as an identifier.
//
// Same character set as a ref name in the store, for the same reason: this
// string ends up in files and in refusals, and an id with a slash in it is a
// path waiting to happen.
func ValidID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
		if i == 0 && (r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// KnownKind reports whether a kind is one this package understands.
func KnownKind(k Kind) bool {
	for _, x := range AllKinds {
		if x == k {
			return true
		}
	}
	return false
}

// Validate refuses a resource that could not be used.
//
// Every rule here is a way a resource could be registered and then never
// usefully selected, which is the failure mode that costs the most to find
// later: the registry looks populated and the scheduler behaves as if it were
// empty.
func Validate(r Resource) *core.Refusal {
	if !ValidID(r.ID) {
		return core.Refuse(core.CodeBadRequest, "bad resource id: "+r.ID)
	}
	if !KnownKind(r.Kind) {
		return core.Refuse(core.CodeBadRequest, "unknown resource kind: "+string(r.Kind))
	}
	if r.Capacity < 1 {
		return core.Refuse(core.CodeBadRequest,
			"capacity must be at least 1 (it is "+itoa(r.Capacity)+"): a resource with no capacity is one you cannot use, and registering it would make the registry look bigger than it is")
	}
	// A negative price is not a discount. It pays the caller to use the
	// resource, so any scheduler that minimises cost will send it everything.
	if r.MoneyPerMS < 0 || r.MoneyPerToken < 0 {
		return core.Refuse(core.CodeBadRequest,
			"a price may be zero but not negative: a negative price pays the caller to use the resource, which is a typo with a budget attached")
	}
	return nil
}

// Cost is what running work costs on r.
//
// Per-dimension, because the two dimensions have genuinely different prices:
// a remote GPU sells milliseconds and does not care how many tokens you feed
// it, and an external model sells tokens and does not care how long you waited.
func Cost(r Resource, work core.Budget) core.Budget {
	ms := float64(work.MS)
	if ms < 0 {
		ms = 0
	}
	tok := float64(work.Tokens)
	if tok < 0 {
		tok = 0
	}
	return core.Budget{
		// A resource does not add milliseconds: it consumes them. The MS of a
		// cost is zero, and saying so is clearer than leaving it unset.
		MS: 0,
		// Tokens are charged only by a resource that prices them.
		//
		// A cost says what the resource takes from the caller, not how big the
		// work is. A local core does not price tokens, so it takes none of the
		// caller's token allowance — charging them anyway would make every free
		// resource unaffordable to a caller whose token allowance happens to be
		// exhausted, which is the opposite of what a free resource is for.
		// (The drive script caught exactly this: `pool.quote` refused
		// everything because the caller passed no token allowance.)
		Tokens: chargedTokens(r, work),
		Money:  int64(ms*r.MoneyPerMS + tok*r.MoneyPerToken),
	}
}

// chargedTokens is what the resource bills in the caller's token allowance.
func chargedTokens(r Resource, work core.Budget) int64 {
	if r.MoneyPerToken <= 0 {
		return 0
	}
	return work.Tokens
}

// Live returns the leases on a resource that have not expired.
//
// Expiry is the whole point: a holder that dies does not release, and a pool
// that counts dead leases shrinks for no stated reason.
func Live(leases []Lease, resource string, now int64) []Lease {
	out := make([]Lease, 0, len(leases))
	for _, l := range leases {
		if l.Resource != resource {
			continue
		}
		if l.Expires <= now {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Acquire takes a slot on a resource, or refuses and names who is holding it.
//
// The refusal names the holder because "the pool is full" is not actionable.
// It is also the only part of this that a human will ever read.
func Acquire(r Resource, leases []Lease, holder string, now, ttl int64) (Lease, *core.Refusal) {
	if holder == "" {
		return Lease{}, core.Refuse(core.CodeBadRequest, "a lease needs a holder: an anonymous lease can never be released")
	}
	if ttl <= 0 {
		return Lease{}, core.Refuse(core.CodeBadRequest,
			"a lease needs a positive ttl: a lease that never expires leaks capacity forever when its holder dies")
	}
	live := Live(leases, r.ID, now)
	if len(live) >= r.Capacity {
		return Lease{}, core.Refuse(core.CodeLeaseHeld,
			"resource "+r.ID+" is at capacity ("+itoa(len(live))+"/"+itoa(r.Capacity)+"); held by "+strings.Join(holders(live), ", "))
	}
	return Lease{Resource: r.ID, Holder: holder, Expires: now + ttl}, nil
}

// Release drops every lease held by holder, on any resource.
//
// By holder rather than by resource, because that is what a finishing job
// knows: it knows who it is, and it should not have to remember every slot it
// took.
func Release(leases []Lease, holder string) []Lease {
	out := make([]Lease, 0, len(leases))
	for _, l := range leases {
		if l.Holder == holder {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Cheapest picks the resource that can do the work for the least money.
//
// Only resources that fit the available budget are considered, and a resource
// with no live capacity is not a candidate — a price you cannot pay is not a
// cheaper price. Ties break by priority, then by id, so the answer does not
// depend on the order the registry happened to be read in.
func Cheapest(rs []Resource, work, available core.Budget, leases []Lease, now int64) (Resource, *core.Refusal) {
	var best Resource
	var bestCost core.Budget
	found := false
	for _, r := range rs {
		if len(Live(leases, r.ID, now)) >= r.Capacity {
			continue
		}
		c := Cost(r, work)
		if c.Money > available.Money || c.Tokens > available.Tokens {
			continue
		}
		if !found || better(r, c, best, bestCost) {
			best, bestCost, found = r, c, true
		}
	}
	if !found {
		return Resource{}, core.Refuse(core.CodeBudgetExhausted,
			"no resource can do this work within the budget: every one is either at capacity or costs more than "+itoa64(available.Money)+" money / "+itoa64(available.Tokens)+" tokens")
	}
	return best, nil
}

func better(r Resource, c core.Budget, best Resource, bestCost core.Budget) bool {
	if c.Money != bestCost.Money {
		return c.Money < bestCost.Money
	}
	if c.Tokens != bestCost.Tokens {
		return c.Tokens < bestCost.Tokens
	}
	if r.Priority != best.Priority {
		return r.Priority > best.Priority
	}
	return r.ID < best.ID
}

func holders(leases []Lease) []string {
	out := make([]string, 0, len(leases))
	for _, l := range leases {
		out = append(out, l.Holder)
	}
	return out
}

// Lease is one held slot.
type Lease struct {
	Resource string `json:"resource"`
	Holder   string `json:"holder"`
	// Expires is unix milliseconds. A lease is live while Expires > now.
	Expires int64 `json:"expires"`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
