// Package distill turns a completed experience into something reusable.
//
// # What it is not
//
// It is not model distillation. Model distillation copies a brain — it moves
// capability from a large model into a small one, and it needs the large model
// to be runnable and its outputs to be capturable. Experience distillation
// tidies notes: it takes something that already happened here and turns it into
// a rule or an artifact that can be used again without redoing the work.
//
// # The one judgement this package makes
//
// **Only a success is distillable, and only a success that cost something.**
// Both halves matter:
//
//   - A failure teaches avoidance, not a rule. Distilling one would make the
//     system confidently repeat something that did not work, and the record
//     would say it had been verified.
//   - An answer that already came from the reflex layer cost nothing. There is
//     nothing left to distil, and distilling it again would count the same
//     success twice — the reflex layer's strength would rise without anything
//     new having happened.
//
// # Why a distilled rule does not fire immediately
//
// A proposal is a **candidate**, and the reflex layer already has the right
// mechanism for candidates: a rule starts below the firing threshold and has to
// be seen working again. So distillation establishes; observation confirms. A
// distilled rule that fired on its first occurrence would be exactly the
// poisoning this project has guarded against since the evidence ladder — one
// success promoted straight to a rule.
package distill

import (
	"strings"

	"teps/core"
)

// LayerReflex is the name of the layer that answers from no context. It is
// spelled here rather than imported so this package depends on no other organ;
// the adapter is where the two are allowed to meet.
const LayerReflex = "reflex"

// Kind is what a proposal turns into.
type Kind string

const (
	// KindReflex is a stimulus-to-action rule: the next time this exact
	// stimulus arrives, no context is composed.
	KindReflex Kind = "reflex"
)

// Trace is one completed experience, as it lands on the chain.
//
// Outcome is a string rather than a bool because "it did not work" and "nobody
// could tell whether it worked" are different, and only one of them is
// distillable. A trace with an unknown outcome is not a success.
type Trace struct {
	TaskID   string `json:"task_id"`
	Stimulus string `json:"stimulus"`
	// Layer is who answered. A trace from the reflex layer is not distillable,
	// because it was already cheap.
	Layer   string `json:"layer"`
	Outcome string `json:"outcome"`
	Action  string `json:"action"`
}

// Proposal is what a trace becomes.
type Proposal struct {
	Kind     Kind   `json:"kind"`
	Stimulus string `json:"stimulus"`
	Action   string `json:"action"`
	// Reason says why this is distillable. A proposal with no reason is a
	// promotion, and promotions are what the evidence ladder exists to stop.
	Reason string `json:"reason"`
}

// Propose decides whether a completed trace may become a rule.
func Propose(t Trace) (Proposal, *core.Refusal) {
	if strings.TrimSpace(t.TaskID) == "" {
		return Proposal{}, core.Refuse(core.CodeBadRequest,
			"a trace needs a task_id: without it there is no way to tell which experience this rule came from")
	}
	if t.Outcome != "ok" {
		return Proposal{}, core.Refuse(core.CodeBadRequest,
			"only a verified success is distillable, and this outcome is "+outcomeName(t.Outcome)+
				": distilling a failure would make the system confidently repeat what did not work, and the rule would claim to have been verified")
	}
	if t.Layer == LayerReflex {
		return Proposal{}, core.Refuse(core.CodeBadRequest,
			"this was answered with no context already, so there is nothing left to distil: distilling it again would count the same success twice and raise a rule's strength without anything new happening")
	}
	if strings.TrimSpace(t.Action) == "" {
		return Proposal{}, core.Refuse(core.CodeBadRequest,
			"there is no action to distil: a rule that fires and does nothing is worse than no rule, because it stops the full path from running")
	}
	if strings.TrimSpace(t.Stimulus) == "" {
		return Proposal{}, core.Refuse(core.CodeBadRequest,
			"there is no stimulus to key the rule on: a rule with no key would match nothing, or everything")
	}
	return Proposal{
		Kind:     KindReflex,
		Stimulus: t.Stimulus,
		Action:   t.Action,
		Reason:   "this stimulus was answered successfully through the full path once, at a cost of composing a context; the next time it arrives, that cost need not be paid again — provided it is seen working a second time before it is allowed to answer",
	}, nil
}

// Distillable is Propose without the explanation, for callers that only branch.
func Distillable(t Trace) bool {
	_, ref := Propose(t)
	return ref == nil
}

// outcomeName renders an outcome for a refusal message. An empty outcome is
// named rather than left blank, because "nobody recorded whether it worked" is
// the case that most needs saying out loud.
func outcomeName(o string) string {
	if o == "" {
		return "missing (nobody recorded whether it worked)"
	}
	return o
}
