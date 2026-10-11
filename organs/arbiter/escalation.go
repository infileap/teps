package arbiter

import (
	"sort"

	"sansheng/core"
)

// Action names what the arbiter is choosing between.
//
// The set is deliberately about *price*, not about tier. An earlier design
// called these L0/L1/L2/L3, and every question about them — which model, where
// it runs, who pays — went unanswered because "level" does not name a cost. Each
// action below has a price that can be measured, which is the only property the
// decision actually needs.
type Action string

const (
	// ActionLocal answers from what is already known. Nearly free, low ceiling.
	ActionLocal Action = "local"
	// ActionSkill runs a stored skill. Deterministic when it works at all.
	ActionSkill Action = "skill"
	// ActionShadow explores on a fork: real compute, no side effects.
	ActionShadow Action = "shadow"
	// ActionEscalate asks an outside expert. Expensive in money and latency.
	ActionEscalate Action = "escalate"
	// ActionAskHuman is the most expensive thing available, in wall-clock time.
	ActionAskHuman Action = "ask_human"
	// ActionDegrade answers honestly with less. Its cost is near zero and so is
	// its value, and having it in the set is what stops the arbiter from having
	// to choose between an overpriced action and nothing.
	ActionDegrade Action = "degrade"
)

// AllActions is every action the arbiter may choose.
var AllActions = []Action{
	ActionLocal, ActionSkill, ActionShadow, ActionEscalate, ActionAskHuman, ActionDegrade,
}

// Offer is one available action, priced.
type Offer struct {
	Action    Action
	Skill     string
	TaskClass string

	// Value is what a success is worth, in the caller's units. The arbiter does
	// not invent it; a caller that cannot price a task cannot expect a priced
	// decision.
	Value float64
	// Cost is money or compute, in the same units as Value.
	Cost float64
	// Latency is wall-clock seconds. It is separate from Cost because they trade
	// against each other: a human is cheap in money and ruinous in time.
	Latency float64

	// LatencyWeight converts seconds into the caller's value units.
	LatencyWeight float64

	// Pinned, when set, is the success probability to use instead of the
	// posterior. It exists for actions with no track record to learn from —
	// notably asking a human, whose reliability is not the system's to estimate.
	Pinned    float64
	UsePinned bool

	// Tag is an opaque label the caller attaches to an offer so it can be
	// matched back to its input after ranking. The arbiter never reads it.
	//
	// It exists because two offers can carry the same action name — two
	// different skills are both "skill" actions — and a response that keyed
	// its per-offer notes by action would then attribute one offer's cost to
	// the other. That is the same failure this project keeps paying for: one
	// fact with two copies, and only one of them updated.
	Tag string
}

// Utility is U(a) = P(success|a) * value - cost - latency + exploration.
//
// The exploration term is what makes this not a pure exploit: it is the only
// reason a skill that was abandoned can ever be tried again. See Posterior.
func Utility(o Offer, p Posterior) float64 {
	prob := p.UpperBound()
	if o.UsePinned {
		prob = clamp01(o.Pinned)
	}
	return prob*o.Value - o.Cost - o.LatencyWeight*o.Latency
}

// Ranked is an offer with the numbers that produced its place.
type Ranked struct {
	Offer   Offer
	Utility float64
	Prob    float64
	Post    Posterior
}

// Rank prices every offer and returns them best first.
//
// The sort is stable and the tie-break is the action name, so two runs over the
// same table give the same order. A ranking that reorders equal offers by map
// iteration would make replay non-deterministic, and the record stream is
// supposed to be the truth.
func Rank(offers []Offer, t Table) []Ranked {
	out := make([]Ranked, 0, len(offers))
	for _, o := range offers {
		key := Key{Skill: o.Skill, TaskClass: o.TaskClass}
		p := t.Get(key)
		prob := p.UpperBound()
		if o.UsePinned {
			prob = clamp01(o.Pinned)
		}
		out = append(out, Ranked{
			Offer:   o,
			Utility: Utility(o, p),
			Prob:    prob,
			Post:    p,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Utility != out[j].Utility {
			return out[i].Utility > out[j].Utility
		}
		return out[i].Offer.Action < out[j].Offer.Action
	})
	return out
}

// Choose returns the best offer, or a refusal when there is nothing to choose.
//
// An empty offer list is a refusal rather than a default action: choosing
// "degrade" for a caller who never offered it would be the arbiter inventing an
// option, and the refusal is what tells the caller to price something.
func Choose(offers []Offer, t Table) (Ranked, *core.Refusal) {
	if len(offers) == 0 {
		return Ranked{}, core.Refuse(core.CodeBadRequest, "no offers to arbitrate")
	}
	return Rank(offers, t)[0], nil
}

// Escalation is the decision to spend outside money, with its justification.
type Escalation struct {
	Chosen    Ranked
	Local     Ranked
	Reason    string
	Threshold float64
}

// ShouldEscalate compares the best paid action against the best local one.
//
// It returns false unless escalation wins by a margin, because the two utilities
// are estimates and an estimate that wins by a hair is a coin flip with a
// receipt. The margin is the caller's, not the arbiter's: the arbiter has no
// basis for deciding how much better is worth the money.
func ShouldEscalate(offers []Offer, t Table, margin float64) (Escalation, *core.Refusal) {
	ranked := Rank(offers, t)
	if len(ranked) == 0 {
		return Escalation{}, core.Refuse(core.CodeBadRequest, "no offers to arbitrate")
	}
	var local Ranked
	found := false
	for _, r := range ranked {
		if r.Offer.Action == ActionLocal || r.Offer.Action == ActionSkill {
			if !found || r.Utility > local.Utility {
				local, found = r, true
			}
		}
	}
	best := ranked[0]
	if !found {
		return Escalation{Chosen: best, Reason: "no local action was offered", Threshold: margin}, nil
	}
	if best.Utility <= local.Utility+margin {
		return Escalation{
			Chosen: local, Local: local, Threshold: margin,
			Reason: "the paid action does not win by the margin, so the estimate is not good enough to spend on",
		}, nil
	}
	return Escalation{Chosen: best, Local: local, Threshold: margin, Reason: "the paid action wins by more than the margin"}, nil
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
