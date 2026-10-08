// Package planner searches for compositions that satisfy a goal.
//
// Everything up to now ran compositions a person wrote. This package is the
// other half of the 宗旨's fourth acceptance criterion: it proposes compositions
// nobody wrote, out of skills that already exist.
//
// # What "satisfy" means here, and what it does not
//
// A skill declares the types it accepts and the types it produces. A chain is a
// candidate when every step's inputs are among the types the chain has already
// produced, and the goal's types are among them at the end.
//
// This is **type-correctness, not correctness**. Two skills can both be
// text→text and still be useless together. The planner narrows an enormous space
// to a small one; whether a candidate is any good is decided by running it and
// by the verifier, and remembered in the posterior. Saying more than that would
// be claiming the search knows something it does not.
//
// # Why the search is exact
//
// The frontier is deduplicated by the **set of types reachable**, keeping the
// most reliable chain that reaches each set. That is not a heuristic. A chain's
// continuation depends only on what types are available, never on how they got
// there — so for a fixed type set, the more reliable prefix can do anything the
// less reliable one can, and does it more reliably. Dominance holds.
//
// A skill may be used more than once. `first;first` can be legitimate, so the
// state is the type set, not a set of used skills.
package planner

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Skill is one registered skill, as the planner sees it.
//
// Rel is supplied by the caller. The planner is pure and does not read the
// chain: the posterior lives there, and folding it is the arbiter's job.
type Skill struct {
	Ref  string
	Name string
	In   []string
	Out  []string
	Rel  float64
}

// Goal says what we start with, what we want, and how much we are willing to
// spend in reliability and in depth.
type Goal struct {
	From         []string
	To           []string
	MaxDepth     int
	Floor        float64
	MaxProposals int
}

// Proposal is a chain that reaches the goal, and the account of why.
type Proposal struct {
	Children []string
	Names    []string
	Rel      float64
	Depth    int
	Reaches  []string
	Why      string
}

// Dropped is a chain the search threw away, and the reason.
//
// Dropping silently would make the search unauditable: the interesting question
// is usually not "what did it find" but "what did it refuse to consider".
type Dropped struct {
	Children []string
	Reason   string
}

// Unusable is a skill the search could not use at all, and why.
type Unusable struct {
	Ref    string
	Name   string
	Reason string
}

// Result is the whole account.
type Result struct {
	Proposals []Proposal
	Dropped   []Dropped
	Unusable  []Unusable
	States    int
	Truncated bool
}

// maxStates bounds the frontier. A resource account, not a correctness one: the
// type sets are finite but the closure can still be large, and a bounded search
// that says it was bounded beats one that exhausts the machine.
const maxStates = 4096

type state struct {
	children []string
	names    []string
	reach    []string
	set      map[string]bool
	rel      float64
}

// Search runs the type-directed search.
//
// It returns candidates ordered by reliability (most reliable first, and shorter
// first among equals), plus everything it discarded and why.
func Search(skills []Skill, g Goal) Result {
	var res Result

	usable, unusable := split(skills, g)
	res.Unusable = unusable

	to := sorted(set(g.To))
	start := state{reach: sorted(set(g.From)), set: set(g.From), rel: 1.0}
	res.States = 1

	// best[key] is the most reliable chain seen for a reachable-type set.
	best := map[string]float64{key(start.reach): 1.0}
	frontier := []state{start}
	seenChains := map[string]bool{}

	depth := 0
	for len(frontier) > 0 && depth < g.MaxDepth {
		depth++
		var next []state
		for _, cur := range frontier {
			for _, sk := range usable {
				if !containsAll(cur.set, sk.In) {
					continue
				}
				rel := cur.rel * sk.Rel
				children := append(append([]string(nil), cur.children...), sk.Ref)
				names := append(append([]string(nil), cur.names...), sk.Name)
				if rel < g.Floor {
					// Reliability pruning. A composition of ten 0.99 skills is
					// 0.90; the product is what makes long chains unattractive
					// without anyone having to say "prefer short chains".
					res.Dropped = append(res.Dropped, Dropped{
						Children: children,
						Reason: "reliability " + trim(rel) + " is below the floor " + trim(g.Floor) +
							": a chain of good steps is not a good chain",
					})
					continue
				}
				reach := union(cur.reach, sk.Out)
				k := key(reach)
				if prev, ok := best[k]; ok && prev >= rel {
					res.Dropped = append(res.Dropped, Dropped{
						Children: children,
						Reason: "reaches the same types by a less reliable route (" + trim(rel) +
							" < " + trim(prev) + "): the more reliable prefix dominates",
					})
					continue
				}
				best[k] = rel
				st := state{children: children, names: names, reach: reach, set: set(reach), rel: rel}
				next = append(next, st)
				res.States++

				if containsAll(st.set, to) {
					ck := strings.Join(children, "|")
					if !seenChains[ck] {
						seenChains[ck] = true
						res.Proposals = append(res.Proposals, Proposal{
							Children: children,
							Names:    names,
							Rel:      rel,
							Depth:    len(children),
							Reaches:  reach,
							Why: "every step's inputs are among the types produced so far, and the goal's " +
								strings.Join(to, ",") + " are among " + strings.Join(reach, ","),
						})
					}
				}
			}
		}
		if res.States >= maxStates {
			res.Truncated = true
			break
		}
		frontier = next
	}

	sort.SliceStable(res.Proposals, func(i, j int) bool {
		if res.Proposals[i].Rel != res.Proposals[j].Rel {
			return res.Proposals[i].Rel > res.Proposals[j].Rel
		}
		if len(res.Proposals[i].Children) != len(res.Proposals[j].Children) {
			return len(res.Proposals[i].Children) < len(res.Proposals[j].Children)
		}
		// Ties broken by the chain itself, so the same input gives the same
		// order. Map iteration is random, and a search that answers differently
		// each run cannot be reasoned about.
		return strings.Join(res.Proposals[i].Children, "|") < strings.Join(res.Proposals[j].Children, "|")
	})
	sort.SliceStable(res.Dropped, func(i, j int) bool {
		return strings.Join(res.Dropped[i].Children, "|") < strings.Join(res.Dropped[j].Children, "|")
	})
	if g.MaxProposals > 0 && len(res.Proposals) > g.MaxProposals {
		res.Proposals = res.Proposals[:g.MaxProposals]
	}
	return res
}

// split separates the skills the search can reason about from the ones it cannot.
//
// A skill that declares no types is not "always applicable" — it is a skill
// whose contract with the planner is missing. Composing on undeclared types
// would be guessing, and a guess dressed as a plan is the worst kind of answer.
func split(skills []Skill, g Goal) ([]Skill, []Unusable) {
	usable := make([]Skill, 0, len(skills))
	var unusable []Unusable
	for _, sk := range skills {
		switch {
		case len(sk.Out) == 0:
			unusable = append(unusable, Unusable{Ref: sk.Ref, Name: sk.Name,
				Reason: "declares no output types: nothing downstream could ever depend on it"})
		case sk.Rel <= 0:
			unusable = append(unusable, Unusable{Ref: sk.Ref, Name: sk.Name,
				Reason: "has no reliability evidence (0), and a step with no evidence cannot be planned around"})
		default:
			usable = append(usable, sk)
		}
	}
	// Deterministic order. The frontier is a set of type sets, and the expansion
	// order must not depend on how the catalog happened to arrive.
	sort.SliceStable(usable, func(i, j int) bool { return usable[i].Ref < usable[j].Ref })
	sort.SliceStable(unusable, func(i, j int) bool { return unusable[i].Ref < unusable[j].Ref })
	_ = g
	return usable, unusable
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func union(a []string, b []string) []string {
	m := make(map[string]bool, len(a)+len(b))
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		m[x] = true
	}
	return sorted(m)
}

func containsAll(have map[string]bool, want []string) bool {
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func key(reach []string) string { return strings.Join(reach, "\x00") }

// trim renders a number for the reason text without trailing noise, so that a
// refusal a person reads says 0.95 rather than 0.9500000000000001.
func trim(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	s := strconv.FormatFloat(f, 'f', 4, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
