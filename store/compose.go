package store

import (
	"math"

	"teps/core"
)

// Op is a composition operator.
//
// Skills are not islands. Ten isolated skills are ten capabilities — that is
// "three begets ten", not "three begets everything". A composition's result is
// **itself a skill**: it enters the store, and it can be composed again. That
// closure is what makes the count grow without anyone writing the results by
// hand.
type Op string

const (
	OpSerial      Op = "serial"      // A;B
	OpParallel    Op = "parallel"    // A‖B
	OpSelect      Op = "select"      // A|B — try one, fall back
	OpRetry       Op = "retry"       // A×n — bounded, and the bound is the point
	OpConditional Op = "conditional" // if P then A else B
	OpLoop        Op = "loop"        // while P: A
)

var ops = map[Op]bool{
	OpSerial: true, OpParallel: true, OpSelect: true,
	OpRetry: true, OpConditional: true, OpLoop: true,
}

// Wire connects an output port to an input port.
//
// "in" and "out" name the composition's own boundary, so a wire is what turns a
// list of children into a **data flow**. Without wiring, a composition is
// juxtaposition — the children run near each other and exchange nothing.
type Wire struct {
	From string `json:"from"` // "in" or "<child-index>:<port>"
	To   string `json:"to"`   // "out" or "<child-index>:<port>"
}

// Composition is a composed skill.
type Composition struct {
	Op            Op       `json:"op"`
	Children      []string `json:"children"` // content hashes; a name would not be immutable
	Wires         []Wire   `json:"wires,omitempty"`
	Predicate     string   `json:"predicate,omitempty"`
	MaxAttempts   int      `json:"max_attempts,omitempty"`
	MaxIterations int      `json:"max_iterations,omitempty"`
}

func (c *Composition) validate() *core.Refusal {
	if !ops[c.Op] {
		return bad("unknown composition op: " + string(c.Op))
	}
	if len(c.Children) == 0 {
		return bad("a composition needs at least one child")
	}
	for _, ch := range c.Children {
		// Hashes, not names. A composition that refers to "the current version
		// of X" is not immutable, and the whole store rests on immutability.
		if !ValidHash(ch) {
			return bad("composition children must be content hashes: " + ch)
		}
	}
	switch c.Op {
	case OpRetry:
		if c.MaxAttempts < 1 {
			return bad("retry requires max_attempts >= 1")
		}
	case OpLoop:
		if c.MaxIterations < 1 {
			return bad("loop requires max_iterations >= 1")
		}
	case OpSelect, OpConditional:
		if c.Predicate == "" {
			return bad(string(c.Op) + " requires a predicate")
		}
	default:
		if c.MaxAttempts != 0 || c.MaxIterations != 0 {
			return bad(string(c.Op) + " must not carry retry/loop bounds")
		}
	}
	for _, w := range c.Wires {
		if w.From == "" || w.To == "" {
			return bad("a wire needs both ends")
		}
		if w.From == "out" || w.To == "in" {
			return bad("a wire may not run backwards across the boundary: " + w.From + " -> " + w.To)
		}
	}
	return nil
}

func clamp01(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func minOf(xs []float64) float64 {
	out := 1.0
	for _, x := range xs {
		if clamp01(x) < out {
			out = clamp01(x)
		}
	}
	return out
}

// DEAD-API: 组合的可靠性没有消费方
// ReliabilityOf returns the probability that a composition succeeds, given each
// child's measured success rate.
//
// **Serial multiplies, and that is the whole problem.** Ten skills at 0.99 are
// 0.90; twenty are 0.82. A planner that ignores this generates large numbers of
// plans that look runnable and succeed a third of the time — so composition
// search has to be pruned by reliability, not by plausibility.
//
// `select` is the one operator that *improves* reliability, because it is the
// only one that gets to try again with a different skill.
func ReliabilityOf(c *Composition, child []float64) float64 {
	prod := 1.0
	for _, p := range child {
		prod *= clamp01(p)
	}
	switch c.Op {
	case OpSelect:
		// The one operator that improves reliability, because it is the only
		// one that gets to try again with a *different* skill. What matters is
		// the chance that every child fails — not the chance that they all
		// succeed.
		fail := 1.0
		for _, p := range child {
			fail *= 1 - clamp01(p)
		}
		return 1 - fail
	case OpRetry:
		n := c.MaxAttempts
		if n < 1 {
			n = 1
		}
		return 1 - math.Pow(prod, float64(n))
	case OpConditional:
		// Both branches exist and only one runs; which one is the predicate's
		// business. Without a measured predicate the honest answer is the
		// weaker branch, not the average.
		if len(child) == 0 {
			return 0
		}
		return minOf(child)
	case OpLoop:
		// A loop is the worst case for reliability and the most likely place to
		// hang. Deliberately conservative: every iteration must land, and the
		// bound is what keeps this finite.
		n := c.MaxIterations
		if n < 1 {
			n = 1
		}
		return math.Pow(prod, float64(n))
	}
	return prod // serial, parallel: every child must land
}

// maxDepthCeiling bounds MaxDepth so that a perfectly reliable skill does not
// turn a search into an infinite loop.
const maxDepthCeiling = 64

// MaxDepth is how deep a chain of equally reliable skills may go before it falls
// below the floor. P̄=0.99 with a floor of 0.9 gives 10; P̄=0.95 gives 2.
//
// This is the number that decides how much of "everything" a planner may reach
// for. It is derived, not chosen — change the floor and the depth limit moves
// with it.
func MaxDepth(pbar, floor float64) int {
	pbar = clamp01(pbar)
	if pbar >= 1 {
		return maxDepthCeiling
	}
	if pbar <= 0 || floor <= 0 {
		return 0
	}
	d, p := 0, 1.0
	for d < maxDepthCeiling {
		next := p * pbar
		if next < floor {
			break
		}
		p, d = next, d+1
	}
	return d
}

// Candidate is a proposed composition with what is known about it.
type Candidate struct {
	Op       Op       `json:"op"`
	Children []string `json:"children"`
	Rel      float64  `json:"reliability"`
	Evidence Evidence `json:"evidence"`
	Depth    int      `json:"depth"`
}

// DEAD-API: 组合的修剪没有调用点
// Prune drops candidates that fall below the reliability floor or exceed the
// depth the floor implies, and **returns what it dropped**.
//
// A pruner that silently discards is a pruner whose effect cannot be reviewed.
// The dropped list is the evidence that the pruning did something.
func Prune(cands []Candidate, floor, pbar float64) (kept, dropped []Candidate) {
	dmax := MaxDepth(pbar, floor)
	for _, c := range cands {
		if c.Rel < floor || c.Depth > dmax {
			dropped = append(dropped, c)
			continue
		}
		kept = append(kept, c)
	}
	return kept, dropped
}
