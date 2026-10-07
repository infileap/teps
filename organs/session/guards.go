package session

// The guards are pure counters, deliberately not model judgements.
//
// A guard that asks a model "are you stuck?" can be talked out of it, its
// answer is not reproducible, and it costs a forward pass to ask. A counter
// cannot be persuaded. This is the whole reason the contract says the guards
// are counters.

const (
	// StallGain is the information gain at or above which a step counts as
	// progress. Below it, the step bought nothing.
	StallGain = 0.05

	// StallStreak is how many such steps in a row constitute a stall.
	StallStreak = 3

	// LoopWindow is the sliding window over which action hashes are compared.
	LoopWindow = 8

	// LoopRepeat is the repeat rate above which the window counts as a loop.
	LoopRepeat = 0.5

	// MaxSameStrategyRetries is how many times a retry may reuse the previous
	// strategy before it is forced to change. It is one: the first
	// same-strategy retry is allowed, the second is not.
	MaxSameStrategyRetries = 1
)

// Stalled reports whether the trailing gains are all below StallGain.
//
// Fewer than StallStreak observations is not a stall — it is not yet evidence
// of anything, and a guard that fires on insufficient evidence is worse than
// no guard, because it teaches the caller to ignore it.
func Stalled(gains []float64) bool {
	if len(gains) < StallStreak {
		return false
	}
	for _, g := range gains[len(gains)-StallStreak:] {
		if g >= StallGain {
			return false
		}
	}
	return true
}

// Looping reports whether the repeat rate of action hashes over the last
// LoopWindow steps exceeds LoopRepeat.
//
// Repeat rate is (steps − distinct) / steps. A full window of 8 therefore
// needs at most 3 distinct actions to be judged a loop: 8 steps with 3
// distinct actions gives 5/8 = 0.625 > 0.5.
//
// Fewer than LoopWindow observations reports false, for the same reason as
// Stalled.
func Looping(actions []string) bool {
	if len(actions) < LoopWindow {
		return false
	}
	w := actions[len(actions)-LoopWindow:]
	seen := make(map[string]bool, len(w))
	for _, a := range w {
		seen[a] = true
	}
	repeat := float64(len(w)-len(seen)) / float64(len(w))
	return repeat > LoopRepeat
}

// WasteRatio is the fraction of escalations that still ended degraded.
//
// Zero escalations reports 0. The caller must read that as "no evidence",
// not as "no waste" — the number is 0 either way, and confusing the two is
// how a metric starts lying.
func WasteRatio(escalations, degraded int) float64 {
	if escalations <= 0 {
		return 0
	}
	return float64(degraded) / float64(escalations)
}

// MayRetry reports whether a retry may use next, given the strategies of the
// attempts already made.
//
// This is the one rule of CONTRACT.md §7 that turns "don't get stuck" from a
// hope into an edge of the machine: a retry that keeps the same strategy is
// allowed at most MaxSameStrategyRetries times. Changing strategy is always
// allowed — the rule restricts repetition, never exploration.
func MayRetry(used []string, next string) bool {
	if next == "" {
		return false
	}
	if len(used) == 0 {
		return true
	}
	if used[len(used)-1] != next {
		return true
	}
	n := 0
	for i := len(used) - 1; i >= 0 && used[i] == next; i-- {
		n++
	}
	return n <= MaxSameStrategyRetries
}
