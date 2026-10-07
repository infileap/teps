// Package evidence is the trust ladder, defined once.
//
// It lives outside both the skill store and the memory organ because **both**
// must compare levels, and two definitions of one ladder is exactly the drift
// this project has already paid for twice: once between the doc and the kernel
// (tools/check_contract.py), and once between two functions inside the store
// (UnknownLicenseRefusal, after curl caught the same fact getting two codes).
//
// A ladder that says E1 < E2 in one place and E2 < E1 in another is not a bug
// you notice. It is a bug that quietly lets unverified material past a filter
// that was supposed to stop it.
package evidence

// Level is the trust level of a claim, and it travels **with** the claim.
//
// The levels are the whole defence of the固化 loop: generation → verification →
// freeze is a positive feedback loop, and a wrong answer that gets frozen is
// wrong forever, executed N times and recorded as a success each time.
type Level string

const (
	L0 Level = "E0" // model-generated, unverified — never enters core memory
	L1 Level = "E1" // ran once in a sandbox and succeeded
	L2 Level = "E2" // confirmed by a second, independent source
	L3 Level = "E3" // external authority, or a human
	L4 Level = "E4" // succeeded N times with no failure
)

var rank = map[Level]int{L0: 0, L1: 1, L2: 2, L3: 3, L4: 4}

// Rank returns the ladder position and whether the level is on the ladder.
//
// An unknown level is deliberately **not** mapped to zero. Zero is a real
// level (E0), and folding "E9" onto it would turn a typo into a trust decision.
func Rank(l Level) (int, bool) {
	r, ok := rank[l]
	return r, ok
}

// Known reports whether the level is on the ladder.
func Known(l Level) bool {
	_, ok := rank[l]
	return ok
}

// Min returns the weaker of two levels.
//
// Composition may lower trust and may never raise it. Stitching a pile of E1s
// into something that reads as E3 is the laundering route into the固化 loop,
// and this function is where that route is closed.
//
// An unknown level **propagates** rather than being ranked: laundering an
// unrecognised level into a recognised one is the same failure by another door.
func Min(a, b Level) Level {
	ra, oka := rank[a]
	rb, okb := rank[b]
	if !oka {
		return a
	}
	if !okb {
		return b
	}
	if ra <= rb {
		return a
	}
	return b
}

// MinAll folds Min over a set.
//
// It seeds from the first element rather than from E0 — because E0 *is* the
// minimum, and a fold that starts at the identity of `min` can only ever return
// that identity. (That was a real bug, caught by a test: MinEvidenceAll
// returned E0 for every input.) An empty set still means E0: a composition with
// no verified children has no verified claim to make.
func MinAll(ls []Level) Level {
	if len(ls) == 0 {
		return L0
	}
	out := ls[0]
	for _, l := range ls[1:] {
		out = Min(out, l)
	}
	return out
}

// AtLeast reports whether l is at or above floor.
//
// It fails closed on an unknown level on **either** side: an unrecognised floor
// must not admit everything.
func AtLeast(l, floor Level) bool {
	rl, okl := rank[l]
	rf, okf := rank[floor]
	if !okl || !okf {
		return false
	}
	return rl >= rf
}
