// Package reflex is the conditioned-reflex layer: a table of
// (stimulus → action) rules whose strength grows with use and fades without it.
//
// It is the one layer that answers **with no context at all**. Everything else
// in this system composes a context — system prompt, skill schemas, live state,
// retrieved material, recent turns — and pays for it in tokens, latency and KV
// cache. A reflex pays nothing: it matches a stimulus it has seen before and
// names the action that worked. That is why it is worth having, and it is the
// only reason.
//
// # Why this is not a lookup table with extra steps
//
// Three properties, all of which a plain map lacks:
//
//   - **Established** — a rule starts weak and gets strong by working.
//   - **Modifiable** — a rule that fails gets weaker faster than a rule that
//     succeeds gets stronger (see Weaken).
//   - **Extinguishable** — a rule that stops being used falls below the fire
//     threshold and stops firing, without anyone deleting it.
//
// # Matching is exact, and that is deliberate
//
// A reflex fires on a stimulus it has seen *before*, normalised — not on one
// that merely resembles it. Near-match firing is a different and much riskier
// thing: the whole value of this layer is that it is cheap, and the whole cost
// of a wrong reflex is that it is **confidently wrong with no context to
// contradict it**. A fuzzy key is exactly where "a plausible wrong answer" comes
// from, so fuzziness is not offered.
//
// # It imports nothing from the other organs
//
// Not the composer, not memory, not the arbiter. A reflex that could consult the
// context pipeline would not be a reflex, and the zero-context claim would stop
// being structural and start being a promise.
package reflex

import (
	"strings"
	"unicode"

	"sansheng/core"
)

// Strength is a value in [0,1]. The constants below are the whole behaviour of
// the layer, so they are named rather than inlined.
const (
	// InitialStrength is where a newly observed rule starts: below the fire
	// threshold, so one observation is never enough to answer with.
	// 0.3, not 0.5, and the difference is a property the layer promises.
	//
	// An observation both creates a rule and applies its outcome, so the
	// starting strength is already one step along: 0.3 + ReinforceStep = 0.5,
	// still below the threshold. **One success is therefore never enough to
	// start answering** — a rule has to be seen working twice. At 0.5 the first
	// success landed on 0.7 and fired, which contradicted the paragraph above
	// while every unit test still passed, because the test had the wrong
	// constant baked into its expectation.
	InitialStrength = 0.3

	// FireThreshold is the strength a rule must reach before it answers.
	FireThreshold = 0.6

	// ReinforceStep is added on a success, saturating at 1.
	ReinforceStep = 0.2

	// WeakenStep is subtracted on a failure, saturating at 0.
	//
	// It is deliberately larger than ReinforceStep. A missed reflex costs one
	// fall-through to the full path, which still works. A **wrong** reflex
	// answers from no context at all and is confidently wrong. So the layer is
	// biased against firing.
	WeakenStep = 0.4

	// DecayPerIdle is subtracted for each idle period a rule goes unused.
	DecayPerIdle = 0.1
)

// Rule is one stimulus → action association.
type Rule struct {
	ID       string
	Pattern  string
	Action   string
	Strength float64
	Fires    int
}

// Normalise reduces a stimulus to the form rules are keyed by.
//
// Lowercase, punctuation dropped, whitespace collapsed. Two stimuli that differ
// only in those ways are the same stimulus; two that differ in any other way are
// not.
func Normalise(text string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(text) {
		// unicode.IsLetter/IsDigit rather than an ASCII range: an ASCII-only
		// rule would collapse every non-English stimulus to the empty string,
		// and a "non-ASCII means keep it" rule would keep full-width
		// punctuation, so "今天几号？" and "今天几号" would be two stimuli.
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// New makes a rule for a stimulus and an action.
func New(id, stimulus, action string) (Rule, *core.Refusal) {
	pattern := Normalise(stimulus)
	if pattern == "" {
		return Rule{}, core.Refuse(core.CodeBadRequest,
			"a stimulus that normalises to nothing would match nothing: an empty pattern is not a general rule, it is a typo")
	}
	if action == "" {
		return Rule{}, core.Refuse(core.CodeBadRequest, "a rule needs an action")
	}
	return Rule{ID: id, Pattern: pattern, Action: action, Strength: InitialStrength}, nil
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

// Reinforce returns the rule after a success.
func Reinforce(r Rule) Rule {
	r.Strength = clamp01(r.Strength + ReinforceStep)
	r.Fires++
	return r
}

// Weaken returns the rule after a failure.
//
// The failure also counts as a firing: the rule did fire, and it was wrong.
// Leaving Fires alone would make the rate look better than it is.
func Weaken(r Rule) Rule {
	r.Strength = clamp01(r.Strength - WeakenStep)
	r.Fires++
	return r
}

// Decay returns the rule after periods of not being used.
//
// This is extinction, and it needs no deletion: a rule that stops being
// reinforced eventually falls below FireThreshold and stops answering. It is
// still there — one success brings it back — which is the difference between
// extinction and forgetting.
func Decay(r Rule, periods int) Rule {
	if periods <= 0 {
		return r
	}
	r.Strength = clamp01(r.Strength - DecayPerIdle*float64(periods))
	return r
}

// Fires reports whether the rule is strong enough to answer.
func Fires(r Rule) bool { return r.Strength >= FireThreshold }

// Match returns the strongest firing rule for a stimulus.
//
// Ties are broken by ID, because map iteration and slice order are not stable
// and a reflex layer that answers differently on identical input cannot be
// reasoned about or tested.
func Match(stimulus string, rules []Rule) (Rule, bool) {
	pattern := Normalise(stimulus)
	if pattern == "" {
		return Rule{}, false
	}
	var best Rule
	found := false
	for _, r := range rules {
		if r.Pattern != pattern || !Fires(r) {
			continue
		}
		if !found || r.Strength > best.Strength || (r.Strength == best.Strength && r.ID < best.ID) {
			best, found = r, true
		}
	}
	return best, found
}

// Rate is the number this layer exists to move: the share of requests answered
// with no context at all.
//
// It is reported rather than inferred. "The reflex layer intercepts most of the
// traffic" is the kind of claim that is true in a design note and false in a
// running system, and the only way to know which is to divide two counters.
func Rate(answered, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(answered) / float64(total)
}
