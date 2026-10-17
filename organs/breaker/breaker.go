// Package breaker is the circuit breaker on a script host.
//
// The project has had a hard deadline since the executor landed: a process that
// does not finish inside its declared budget is killed and the attempt is
// recorded as a timeout. What it has never had is the other half of supervision
// — a host that keeps failing keeps being spawned. Every attempt burns its
// declared budget, spawns a process, and comes back with the same answer.
//
// Three decisions are worth stating, because each one could reasonably have gone
// the other way.
//
//  1. **Only host failures count.** A skill whose assertion fails is the skill's
//     problem; a host that will not start or will not return is the host's.
//     Folding the two together would trip the breaker on a skill that is
//     correctly reporting that it cannot do the job — which is the system
//     working, not the system breaking.
//
//  2. **A breaker has three states, not two.** Closed, open, and half-open.
//     Without half-open there is no way back except a restart: the host either
//     works or is refused forever. Without open there is no breaker at all.
//
//  3. **Half-open admits exactly one.** If it admitted everything, the state
//     would be a label rather than a decision, and a host that is still broken
//     would take the full traffic of every skill behind it on the first probe.
//
// This package is pure: no clock, no processes. Time is passed in.
package breaker

import (
	"sort"
	"strings"

	"teps/core"
)

// State is what the breaker currently says about one host.
type State string

const (
	// Closed is normal operation: every attempt is allowed through.
	Closed State = "closed"
	// Open refuses attempts without spawning anything.
	Open State = "open"
	// HalfOpen admits exactly one probe.
	HalfOpen State = "half-open"
)

// Defaults. Threshold is deliberately small: three consecutive host failures in
// a row is not bad luck, it is a broken interpreter.
const (
	// Threshold is how many consecutive host failures open the breaker.
	Threshold = 3
	// CooldownMS is how long it stays open before one probe is allowed.
	CooldownMS = 30000
)

// Trip is one recorded attempt against a host.
//
// Only the two host-level verdicts are recorded. A skill-level failure never
// reaches this type at all — that decision is made by the caller, and it is
// stated in the package doc so it cannot be made differently in two places.
type Trip struct {
	// Host is the interpreter, as resolved for the spawn.
	Host string `json:"host"`
	// OK is whether the host came back. Refused and timed out are both not OK.
	OK bool `json:"ok"`
	// At is unix milliseconds.
	At int64 `json:"at"`
}

// Record appends one attempt.
//
// The caller decides whether an attempt was a host failure; this function only
// records the answer. It exists so that the two callers — the live path and the
// replay path — cannot disagree about what a trip looks like.
func Record(trips []Trip, host string, ok bool, now int64) []Trip {
	return append(trips, Trip{Host: host, OK: ok, At: now})
}

// consecutive is how many host failures in a row, counting back from the most
// recent trip for this host.
//
// Counting back from the end rather than over the whole history is the whole
// point: a host that fails three times, works once, then fails three more is
// not the same as one that failed six times, and only the second should be open.
func consecutive(trips []Trip, host string) int {
	n := 0
	for i := len(trips) - 1; i >= 0; i-- {
		if trips[i].Host != host {
			continue
		}
		if trips[i].OK {
			break
		}
		n++
	}
	return n
}

// lastAt is the most recent trip time for this host, or 0.
func lastAt(trips []Trip, host string) int64 {
	for i := len(trips) - 1; i >= 0; i-- {
		if trips[i].Host == host {
			return trips[i].At
		}
	}
	return 0
}

// StateOf reports the breaker's state for one host at a given time.
//
// It is a pure function of the trips and the clock, so the same chain always
// produces the same answer — which is the property every projection in this
// project has to have.
func StateOf(trips []Trip, host string, now int64) State {
	if consecutive(trips, host) < Threshold {
		return Closed
	}
	last := lastAt(trips, host)
	if now-last >= CooldownMS {
		return HalfOpen
	}
	return Open
}

// Allow decides whether an attempt may be spawned, and refuses with the reason
// when it may not.
//
// The refusal is not retryable in the usual sense: the answer is "wait", and the
// wait is named. A retry flag that promised an immediate retry here would be
// promising something the breaker has already decided against.
func Allow(trips []Trip, host string, now int64) *core.Refusal {
	switch StateOf(trips, host, now) {
	case Open:
		last := lastAt(trips, host)
		return core.Refuse(core.CodeCircuitOpen,
			"host "+host+" is refusing: "+itoa(consecutive(trips, host))+
				" attempts in a row did not come back, and the cooldown has "+itoa64(CooldownMS-(now-last))+
				"ms left. This is not the skill failing — it is the thing that runs skills failing, so retrying the skill would only spend its budget again")
	case HalfOpen:
		// One probe is admitted. Which one is the caller's business; what the
		// breaker guarantees is that the state is half-open, not that this
		// particular attempt is the probe.
		return nil
	default:
		return nil
	}
}

// DEAD-API: 半开由 Allow 与 StateOf 判
// HalfOpenProbe reports whether this attempt is the single probe.
//
// The caller needs it because the state alone does not say whether the attempt
// it is about to run is the one that decides the breaker's future. A half-open
// breaker that admitted every attempt would take the full traffic of every skill
// behind it on the first probe.
func HalfOpenProbe(trips []Trip, host string, now int64) bool {
	return StateOf(trips, host, now) == HalfOpen
}

// Hosts lists every host the trips mention, sorted, so a report over the
// breaker does not depend on the order attempts happened to arrive in.
func Hosts(trips []Trip) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(trips))
	for _, t := range trips {
		if seen[t.Host] {
			continue
		}
		seen[t.Host] = true
		out = append(out, t.Host)
	}
	sort.Strings(out)
	return out
}

// IsHostFailure says which verdicts count as the host failing.
//
// It is here, next to the package doc that explains why, rather than in the
// adapter — so that a second caller cannot decide it differently. The two
// verdicts it accepts are the two the executor produces when the process itself
// did not do its job: it never started, or it did not come back in time.
//
// A skill whose assertion failed is deliberately absent. That is the skill
// reporting a true thing about itself.
func IsHostFailure(verdict string) bool {
	switch strings.ToLower(verdict) {
	case "refused", "timeout":
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func itoa64(n int64) string {
	if n <= 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
