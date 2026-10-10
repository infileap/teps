package main

import (
	"strings"

	"sansheng/core"
	"sansheng/organs/intake"
)

// metrics.get is the one read that reports whether the numbers are defined.
//
// Every organ already reports its own numbers and each of those reports is
// correct on its own. What none of them says is whether the numbers compose
// into anything. The capability formula is a product:
//
//	capability = coverage × accuracy × reliability
//
// and a product with one undefined factor is undefined. Today that has to be
// discovered by asking four ops and noticing that one of them returned a zero
// which means "we have never seen a task" rather than "we answered none of
// them" — the same number standing for two different facts.
//
// So this op adds no facts of its own. It reports each factor from the organ
// that owns it, and it says out loud whether the factor is defined. That is the
// whole contribution: a zero with no denominator is not a small number, it is
// an absent one.
func (s *server) handleMetrics(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "metrics.") {
		return false
	}
	switch f.T {
	case "metrics.get":
		s.metricsGet(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) metricsGet(f core.Frame, out *core.Frame) {
	// A read takes no body; see audit.tail for why an all-optional op must not
	// demand one.
	if len(f.Body) > 0 {
		var body struct{}
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return
		}
	}

	s.tasks.lock()
	stats := s.tasks.summarise()
	s.tasks.unlock()

	s.decisions.lock()
	tbl := s.decisions.table()
	observations := len(s.decisions.obs)
	s.decisions.unlock()

	coverage := map[string]any{
		"tasks":                    stats.Total,
		"answered_without_context": stats.ByLayer[intake.LayerReflex],
		"intercept_rate":           round(stats.InterceptRate),
		"source":                   "task.stats",
	}
	coverage["defined"] = stats.Total > 0
	if stats.Total == 0 {
		coverage["reason"] = "no task has ever been taken in, so the denominator does not exist: a rate over zero arrivals is not zero, it is undefined"
	}

	// Accuracy is the factor nobody can compute yet, and saying so is the point.
	// The planner proposes chains; a proposal that gets published becomes a
	// skill with its own ref, and the runs land in the posterior keyed by
	// skill_id. Nothing carries the proposal's identity across that gap, so
	// there is no way to ask "of the chains we proposed, how many worked".
	accuracy := map[string]any{
		"defined": false,
		"source":  "planner",
		"reason":  "nothing links a proposal to the runs of the skill it became: the posterior is keyed by skill_id and a proposal has no id that survives publication",
	}

	reliability := map[string]any{
		"observations": observations,
		"skills":       len(tbl),
		"source":       "arbiter.state",
	}
	if len(tbl) == 0 {
		reliability["defined"] = false
		reliability["reason"] = "no execution has ever been concluded, so every skill is still at the prior"
	} else {
		sum, weakest := 0.0, 1.0
		for _, p := range tbl {
			m := p.Mean()
			sum += m
			if m < weakest {
				weakest = m
			}
		}
		reliability["defined"] = true
		reliability["mean"] = round(sum / float64(len(tbl)))
		// The weakest link is the number that matters for a product: a chain is
		// as reliable as its worst step, and an average hides exactly that.
		reliability["weakest"] = round(weakest)
	}

	capability := map[string]any{"defined": false}
	switch {
	case !coverage["defined"].(bool):
		capability["reason"] = "coverage is undefined"
	case !reliability["defined"].(bool):
		capability["reason"] = "reliability is undefined"
	default:
		capability["reason"] = "accuracy is undefined: a product with one undefined factor is undefined"
	}

	out.Body = mustJSON(map[string]any{
		"coverage":    coverage,
		"accuracy":    accuracy,
		"reliability": reliability,
		"capability":  capability,
	})
}
