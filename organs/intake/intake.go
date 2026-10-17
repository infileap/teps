// Package intake is the front door: where a task arrives, which layer answers
// it, and what that adds up to.
//
// # Why this package exists
//
// The design note makes one claim that can be falsified with a single number —
// "the reflex layer answers 60–80% of requests with no context" — and until now
// that number was unmeasurable, because **there was no denominator**. The rate
// needs "requests that arrived", and nothing in this system received requests.
//
// Recording arrivals is therefore not bookkeeping. It is the difference between
// a claim and a measurement.
//
// # What it deliberately does not know
//
// intake does not import the reflex organ, or any other. It knows about
// **layers that can answer with no context**, expressed as an interface. The
// alternative — reaching into reflex for its rule type — would make this package
// know one specific implementation, and a second zero-context layer would then
// require changing the router.
//
// The router's whole policy is one sentence: **the cheapest layer that can
// answer, answers.**
package intake

import (
	"math"
	"sort"

	"teps/core"
)

// Layer names who answered. They are the layers of the design note, and they
// are ordered by price rather than by number: L0/L1/L2/L3 let every question
// about cost go unanswered, because a "level" does not name a cost.
type Layer string

const (
	// LayerReflex answers from no context at all: no composer, no retrieval, no
	// memory. It is the only layer whose context cost is structurally zero
	// rather than merely small.
	LayerReflex Layer = "reflex"
	// LayerFull is everything else. It is named for what it costs, not for what
	// it is: it composes a context, and that context is the expensive part.
	LayerFull Layer = "full"
)

// Answerer is a layer that can answer a stimulus with no context at all.
//
// Answer returns the action to take and whether this layer will take it. A
// layer that declines is not a failure; it is the layer saying it would have to
// guess, and guessing from no context is the one thing this whole arrangement
// exists to avoid.
type Answerer interface {
	Name() string
	Answer(stimulus string) (action string, ok bool)
}

// Decision is what the front door decided about one arrival.
type Decision struct {
	Layer Layer `json:"layer"`
	// By names the layer that answered. Empty when nothing did.
	By string `json:"by,omitempty"`
	// Action is what the answering layer will do.
	Action string `json:"action,omitempty"`
	// ContextTokens is what answering cost in context. Zero is a structural
	// fact for the reflex layer, not an estimate.
	ContextTokens int `json:"context_tokens"`
	// FallThrough is true when no zero-context layer would take it, so the
	// expensive path is the only one left.
	FallThrough bool `json:"fall_through"`
	// Reason is why, in words, so a decision can be argued with.
	Reason string `json:"reason"`
}

// Route asks each layer, in the order given, whether it will answer.
//
// Order is the caller's, because price is the caller's business: this package
// knows that the cheap layers go first, not which layer is cheap.
func Route(stimulus string, layers []Answerer) Decision {
	for _, l := range layers {
		if l == nil {
			continue
		}
		action, ok := l.Answer(stimulus)
		if !ok {
			continue
		}
		return Decision{
			Layer:         LayerReflex,
			By:            l.Name(),
			Action:        action,
			ContextTokens: 0,
			Reason:        l.Name() + " answered with no context at all",
		}
	}
	return Decision{
		Layer:       LayerFull,
		FallThrough: true,
		Reason:      "no zero-context layer would take this stimulus: it needs a composed context, and that is where the cost is",
	}
}

// Record is one arrival, as it lands on the chain.
//
// It records the layer that answered rather than only the outcome, because the
// rate this package exists to compute is a statement about layers, and a record
// that kept only "it worked" could not produce it.
type Record struct {
	TaskID   string `json:"task_id"`
	Stimulus string `json:"stimulus"`
	Layer    Layer  `json:"layer"`
	By       string `json:"by,omitempty"`
}

// Stats is what a set of arrivals adds up to.
type Stats struct {
	Total int `json:"total"`
	// ByLayer counts arrivals each layer answered. LayerFull is included: it is
	// the fall-through, and leaving it out would make the counts not add up to
	// Total.
	ByLayer map[Layer]int `json:"by_layer"`
	// InterceptRate is the share answered with no context.
	InterceptRate float64 `json:"intercept_rate"`
	// Scope always travels with the rate. A rate without its denominator is how
	// "60–80%" became a claim nobody could check.
	Scope string `json:"scope"`
}

// ScopeArrived is the denominator, stated in words wherever the rate is
// reported.
const ScopeArrived = "tasks that arrived at this intake"

// Summarise folds arrivals into counts and a rate.
func Summarise(records []Record) Stats {
	s := Stats{ByLayer: map[Layer]int{}, Scope: ScopeArrived}
	for _, r := range records {
		s.Total++
		layer := r.Layer
		if layer == "" {
			// An arrival with no layer recorded is counted under the expensive
			// path rather than dropped: dropping it would raise the rate by
			// forgetting the cases that did not go well.
			layer = LayerFull
		}
		s.ByLayer[layer]++
	}
	if s.Total > 0 {
		s.InterceptRate = round4(float64(s.ByLayer[LayerReflex]) / float64(s.Total))
	}
	return s
}

// DEAD-API: 任务分层还没有消费方
// Layers lists the layers that appear, sorted, so a caller can print a stable
// report rather than whatever order a map happened to iterate in.
func (s Stats) Layers() []Layer {
	out := make([]Layer, 0, len(s.ByLayer))
	for l := range s.ByLayer {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Validate refuses an arrival that could not be counted.
//
// A task with no id cannot be told apart from a retry of itself, and one with no
// stimulus cannot be routed. Both are `bad_request` for the same reason: the
// request is malformed, and no retry of it would be well-formed.
func Validate(r Record) *core.Refusal {
	if r.TaskID == "" {
		return core.Refuse(core.CodeBadRequest, "an arrival needs a task_id: without it a retry cannot be told apart from a new task, and the rate would count it twice")
	}
	if r.Stimulus == "" {
		return core.Refuse(core.CodeBadRequest, "an arrival needs a stimulus: there is nothing to route")
	}
	return nil
}

func round4(f float64) float64 { return math.Round(f*10000) / 10000 }
