package arbiter

import (
	"math"
	"sort"

	"teps/core"
)

// Expectation names which question an offer is answering.
//
// It exists because "what does one attempt cost" and "what does it cost to get
// there" are two different questions, and the arithmetic used to answer one of
// them while looking like it answered the other.
//
// They rank differently, and neither is wrong. They diverge exactly when a
// cheap option is unreliable — which is the case a caller most needs to see:
//
//	A: p=0.99, cost=1    B: p=0.50, cost=0.01    value=10
//	per attempt:  A = 8.90   B = 4.99   -> A wins
//	per success:  A = 8.99   B = 9.98   -> B wins
//
// A caller that cannot say which question it is asking cannot read the answer.
type Expectation string

const (
	// ExpectAttempt prices one attempt. This is the default: it is what the
	// arithmetic has always meant, and changing a default silently would be a
	// different failure than the one this type fixes.
	ExpectAttempt Expectation = "attempt"
	// ExpectSuccess prices the attempt that finally works: cost divided by the
	// probability of success.
	ExpectSuccess Expectation = "success"
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

	// Expect says which question this offer is answering. The empty value means
	// ExpectAttempt, so an offer written before this field existed keeps its
	// meaning.
	Expect Expectation
}

// Utility is U(a) = P(success|a) * value - cost - latency + exploration.
//
// The exploration term is what makes this not a pure exploit: it is the only
// reason a skill that was abandoned can ever be tried again. See Posterior.
func Utility(o Offer, p Posterior) float64 {
	cost, ok := ExpectedCost(o, p)
	if !ok {
		// An action that never succeeds has no cost per success, so it cannot be
		// compared on that basis. Rank keeps it (last, by this number) rather
		// than dropping it: a silently dropped offer cannot be audited, and the
		// adapter reports these in its skipped list before ranking.
		return math.Inf(-1)
	}
	return ProbOf(o, p)*o.Value - cost - o.LatencyWeight*o.Latency
}

// ProbOf is the probability an offer is judged by: the optimistic estimate, or
// the pinned value when the caller supplied one.
//
// It is the upper bound rather than the mean on purpose. Choosing is not
// reporting: an option that has been tried twice and worked twice should be
// explored, and using the mean here would make the system stop trying the
// things it knows least about.
func ProbOf(o Offer, p Posterior) float64 {
	if o.UsePinned {
		return clamp01(o.Pinned)
	}
	return p.UpperBound()
}

// ExpectedCost is what the offer costs on the basis it says it is priced on.
//
// It returns false when a per-success cost is asked for and the probability is
// zero: an action that never succeeds has no price, because it buys nothing.
// Reporting a very large number there would be a lie of a different shape.
func ExpectedCost(o Offer, p Posterior) (float64, bool) {
	if o.Expect != ExpectSuccess {
		return o.Cost, true
	}
	prob := ProbOf(o, p)
	if prob <= 0 {
		return 0, false
	}
	return o.Cost / prob, true
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
		prob := ProbOf(o, p)
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

// DEAD-API: 报价排序走 Rank；Choose 是给「只选一个」的调用方准备的，目前没有
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
	// ★★★ 「本地的」是**不花外面钱**的那些，而 `best` 是**花外面钱**的那一个。
	//
	// 此前这里把 `ActionSkill` 与 `ActionLocal` 一起算进「本地」，而 `best` 取的
	// 是 `ranked[0]`（全体最好）。于是当一个 `skill` 动作就是全体最好时：
	//
	//   local = 那个 skill 动作（因为它也在「本地」集合里）
	//   best  = 同一个 skill 动作
	//   best.Utility <= local.Utility + margin   ->  **恒真**（margin > 0）
	//
	// 实测（`margin: 0.5`，`local/cached` value 1 cost 0 latency 1 与
	// `skill/paid` value 10 cost 1 latency 1）：
	//
	//   chosen = skill/paid  utility 9
	//   local  = skill/paid  utility 9      ← **同一个**
	//   reason = "the paid action does not win by the margin, so the estimate
	//             is not good enough to spend on"
	//
	// **付费动作从来没有被比过。** 那句话不是关于发生的事的 —— 而它正是这个项目
	// 一直在抓的那一类。而 `ShouldEscalate` 的文档逐字写着它做的是什么：
	//
	//   ShouldEscalate compares the best paid action against the best local one.
	//
	// ★ 而那个 `||` 分支**没有被任何测试碰过**：`TestEscalationRequiresAMargin`
	// 只用了 `ActionLocal` 与 `ActionEscalate`。
	//
	// 判据改成按**价钱**分：花外面钱的是 `{escalate, ask_human}`，不花的是其余
	// 四个。这正是这个文件开头那句注释说的（「The set is deliberately about
	// *price*, not about tier.」）。
	paid := Ranked{}
	foundPaid := false
	local := Ranked{}
	foundLocal := false
	for _, r := range ranked {
		if spendsOutsideMoney(r.Offer.Action) {
			if !foundPaid || r.Utility > paid.Utility {
				paid, foundPaid = r, true
			}
			continue
		}
		if !foundLocal || r.Utility > local.Utility {
			local, foundLocal = r, true
		}
	}
	if !foundPaid {
		return Escalation{Chosen: ranked[0], Local: local, Threshold: margin,
			Reason: "no paid action was offered, so there is nothing to compare against the free ones"}, nil
	}
	if !foundLocal {
		return Escalation{Chosen: paid, Local: paid, Threshold: margin,
			Reason: "no free action was offered, so the paid one is the only option and no margin applies"}, nil
	}
	if paid.Utility <= local.Utility+margin {
		return Escalation{
			Chosen: local, Local: local, Threshold: margin,
			Reason: "the paid action does not win by the margin, so the estimate is not good enough to spend on",
		}, nil
	}
	return Escalation{Chosen: paid, Local: local, Threshold: margin, Reason: "the paid action wins by more than the margin"}, nil
}

// spendsOutsideMoney reports whether an action costs money that leaves the system.
//
// ★ 这一条判据的存在，是因为「本地」此前是**按动作名枚举**的（`ActionLocal`
// 或 `ActionSkill`），而那个枚举把「不花外面钱」这件事说成了一半：`shadow` 与
// `degrade` 也不花，而 `skill` 被算进来之后 `best` 与 `local` 可以是同一个。
// 按价钱分之后，「本地的」与「花外面钱的」是两个**互补**的集合。
func spendsOutsideMoney(a Action) bool {
	return a == ActionEscalate || a == ActionAskHuman
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
