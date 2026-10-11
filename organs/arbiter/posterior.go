// Package arbiter decides which action to take next, and whether a skill that
// used to work still does.
//
// # Why a plain Beta posterior is not enough
//
// The contract says P(success) ~ Beta(alpha + ok, beta + failures). Taken
// literally, that is a running average whose memory grows without bound: a skill
// that succeeded 1000 times and then rots needs roughly that many bad calls
// before its mean falls anywhere interesting. The scheduler would keep choosing
// a dead skill for a long time, and "the system notices by itself" would be a
// claim rather than a property.
//
// So the posterior here is a **fold with exponential forgetting**: before each
// observation, both counts are scaled by a decay factor slightly below 1. That
// keeps the estimator's memory bounded, so evidence from long ago cannot outvote
// evidence from now. The decay is an explicit parameter rather than a hidden
// constant, because it *is* the sensitivity/specificity tradeoff: lower means
// faster detection of rot and noisier decisions.
//
// # Why exploration is not optional
//
// An arbiter that always picks the current best makes every choice final. One
// bad patch would kill a good skill permanently, because nothing would ever call
// it again and its posterior could never recover. So utility carries an
// upper-confidence term: a rarely-tried action keeps a small standing chance.
// Without it the system can only ever get worse at what it already does.
package arbiter

import (
	"math"
	"sort"
)

// Key identifies what a posterior is about.
//
// Grouping by (skill, task class) rather than by skill alone is what keeps a
// skill from being condemned for failing at something it was never meant to do.
// One posterior per skill would average "read a file" together with "parse a
// PDF" and produce a number that describes neither.
type Key struct {
	Skill     string
	TaskClass string
}

// Posterior is a Beta distribution, kept as its two shape parameters.
type Posterior struct {
	Alpha float64
	Beta  float64
}

// Prior is the posterior of something never observed.
//
// Beta(1,1) is uniform on [0,1]: "no idea". A prior of zero would make the mean
// undefined and would let a single failure produce certainty, which is exactly
// the mistake this package exists to avoid.
func Prior() Posterior { return Posterior{Alpha: 1, Beta: 1} }

// Mean is the expected success probability.
func (p Posterior) Mean() float64 {
	d := p.Alpha + p.Beta
	if d <= 0 {
		return 0.5
	}
	return p.Alpha / d
}

// Count is the effective number of observations, after forgetting.
//
// It is not the number of calls ever made: decay is what makes old evidence
// count for less, and reporting the raw total here would hide that.
func (p Posterior) Count() float64 { return p.Alpha + p.Beta }

// Observe folds in one execution result.
//
// ok is the verifier's verdict, not the executor's claim. A skill that reports
// its own success has a posterior that measures its optimism.
func (p Posterior) Observe(ok bool, decay float64) Posterior {
	if decay <= 0 || decay > 1 {
		decay = 1
	}
	out := Posterior{Alpha: p.Alpha * decay, Beta: p.Beta * decay}
	if ok {
		out.Alpha++
	} else {
		out.Beta++
	}
	return out
}

// Table is the set of posteriors an arbiter has.
type Table map[Key]Posterior

// Get returns the posterior for a key, or the prior if it has never been seen.
func (t Table) Get(k Key) Posterior {
	if p, ok := t[k]; ok {
		return p
	}
	return Prior()
}

// Observation is one execution result, as it arrives from the record stream.
type Observation struct {
	Key Key
	OK  bool
	// Rules is the identity of the assertions this run was judged against.
	//
	// The chain already records *which verifier* passed a run. It did not
	// record *which assertions* it was judged against, so a skill republished
	// with different assertions kept folding new runs into the same key — and
	// the number that came out was an average of two different questions.
	//
	// The empty value means "the run predates this field", which is a third
	// thing: not one rule set, not two, but unknown.
	Rules string
}

// Fold replays observations in order into a table.
//
// Order matters and that is the point: forgetting makes the estimator care about
// *when* something happened. A replay that dropped the order would produce a
// different answer than the live system, which is the failure mode the record
// stream exists to prevent.
func Fold(obs []Observation, decay float64) Table {
	t := Table{}
	for _, o := range obs {
		t[o.Key] = t.Get(o.Key).Observe(o.OK, decay)
	}
	return t
}

// DefaultDecay is the forgetting factor used when a caller does not choose one.
//
// 0.98 gives the estimator an effective memory of roughly 1/(1-0.98) = 50
// observations. That is the number that decides how fast rot is noticed, and it
// is deliberately a parameter rather than a law.
const DefaultDecay = 0.98

// Exploration is the width of the upper-confidence term, in the same units as
// the utility it is added to.
//
// It is not a small epsilon for tie-breaking. It is the only thing that lets a
// skill ever come back, so setting it to zero makes every decision permanent.
const Exploration = 0.25

// UpperBound returns a high-probability estimate of the success rate.
//
// This is the Wilson-style width sqrt(ln(N)/n) in its simplest form: it shrinks
// as evidence accumulates, and it stays large for something tried once. The name
// matters — it is an upper bound, so an unobserved action is treated as
// promising rather than as unknown-and-therefore-avoided.
func (p Posterior) UpperBound() float64 {
	n := p.Count() - 2 // subtract the prior's two pseudo-counts
	if n <= 1 {
		return 1.0
	}
	width := Exploration * math.Sqrt(math.Log(1+n)/n)
	m := p.Mean() + width
	if m > 1 {
		return 1
	}
	return m
}

// RulesOf reports, per key, the distinct judging-rule identities behind its
// observations.
//
// ★ A posterior folded across two judging rules is not one posterior.
//
// The same argument as everywhere else in this project: one number with two
// sources is not one number. A mean of 0.5 can mean "one program that works
// half the time" or "two programs, one always works and one never does" — and
// those are two facts.
//
// It takes the same []Observation as Fold, so the two projections cannot be
// derived from different walks over different data.
func RulesOf(obs []Observation) map[Key][]string {
	seen := map[Key]map[string]bool{}
	out := map[Key][]string{}
	for _, o := range obs {
		id := o.Rules
		if id == "" {
			id = "(unrecorded)"
		}
		if seen[o.Key] == nil {
			seen[o.Key] = map[string]bool{}
		}
		if !seen[o.Key][id] {
			seen[o.Key][id] = true
			out[o.Key] = append(out[o.Key], id)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}
