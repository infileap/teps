package main

import (
	"sort"
	"strconv"
	"strings"

	"sansheng/core"
	"sansheng/organs/arbiter"
	"sansheng/organs/intake"
	"sansheng/store"
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

	accuracy := s.accuracy(tbl)

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
	case !accuracy["defined"].(bool):
		capability["reason"] = "accuracy is undefined"
	case !reliability["defined"].(bool):
		capability["reason"] = "reliability is undefined"
	default:
		capability["defined"] = true
		capability["value"] = round(coverage["intercept_rate"].(float64) *
			accuracy["value"].(float64) * reliability["mean"].(float64))
	}

	out.Body = mustJSON(map[string]any{
		"coverage":    coverage,
		"accuracy":    accuracy,
		"reliability": reliability,
		"capability":  capability,
	})
}

// plannerSource is the provenance value that marks a chain as the planner's
// output rather than a person's.
const plannerSource = "planner"

// accuracy reports how the chains the planner proposed actually did.
//
// ★ The link between a proposal and a run is provenance.source == "planner".
// It has to be something on the chain, because a proposal is not: the search is
// pure and writes nothing. A published chain's *shape* is recoverable from its
// own manifest, but whether a person or the planner produced it is not — that
// is what the source field records, and it is the whole ABI this factor needed.
//
// The denominator is therefore "compositions that say the planner made them",
// not "chains the planner proposed". A proposal nobody published leaves no
// trace, and reporting a rate over proposals would be inventing a denominator.
func (s *server) accuracy(tbl arbiter.Table) map[string]any {
	out := map[string]any{"source": "store + arbiter", "provenance": plannerSource}
	catalog, ref := s.skills.Catalog()
	if ref != nil {
		out["defined"] = false
		out["reason"] = "the catalogue could not be read: " + ref.Reason
		return out
	}
	traced, observed, ok := 0, 0, 0
	var chains []string
	byChain := map[string][]string{}
	for _, e := range catalog {
		m := e.Manifest
		if m == nil || m.Kind != store.KindCompose || m.Compose == nil {
			continue
		}
		if m.Provenance.Source != plannerSource {
			continue
		}
		traced++
		chains = append(chains, e.Name)
		// ★ The posterior is keyed by (skill, task class), and an observation
		// is written under the task class the invocation declared. Looking this
		// chain up with an empty task class silently returns the *prior* — the
		// same zero-means-two-things failure this op exists to report, this
		// time inside the op itself. So the evidence is summed over every task
		// class the chain has been run under, and the classes are reported.
		//
		// Count() is Alpha+Beta and the prior is Beta(1,1), so a posterior
		// still at 2.0 has never been observed. "Never run" is not a failure
		// and must not be averaged in as one.
		var p arbiter.Posterior
		var classes []string
		for k, v := range tbl {
			if k.Skill != e.Name {
				continue
			}
			p.Alpha += v.Alpha
			p.Beta += v.Beta
			classes = append(classes, k.TaskClass)
		}
		if p.Count() <= 2.0+1e-9 {
			continue
		}
		sort.Strings(classes)
		byChain[e.Name] = classes
		observed++
		if p.Mean() >= 0.5 {
			ok++
		}
	}
	sort.Strings(chains)
	out["traced"] = traced
	out["observed"] = observed
	out["unrun"] = traced - observed
	out["ok"] = ok
	out["chains"] = chains
	out["task_classes"] = byChain
	if observed == 0 {
		out["defined"] = false
		out["reason"] = "no chain the planner proposed has ever been run: " + strconv.Itoa(traced) +
			" are in the shop and every one of them is still at the prior. A matching rate over proposals nobody ran would be a rate over zero"
		return out
	}
	out["defined"] = true
	out["value"] = round(float64(ok) / float64(observed))
	return out
}
