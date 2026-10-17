package arbiter

import (
	"teps/evidence"
)

// Fixing is the decision to turn a repeated LLM path into a stored skill.
//
// # Why the opex-only utility is not enough
//
// U(a) prices one call. Choosing by U alone therefore always prefers the paid
// action, because it is better *this time* — and a system that always prefers
// the paid action never accumulates anything. It stays permanently temporary.
// Fixing has a cost now and a payoff later, so it needs its own arithmetic.
//
// # Why the poison term is not decoration
//
// Fixing a wrong skill is worse than fixing nothing. A wrong skill is not merely
// a wasted write: it gets executed N times, and every one of those executions is
// recorded as a success, which feeds the posterior that decides it should keep
// being executed. The error is not stored, it is *institutionalized*. So the
// value of fixing carries a negative term proportional to how likely the skill
// is to be wrong.
type Fixing struct {
	// Calls is how many times this task is expected to recur.
	Calls float64
	// OpexNow is the per-call cost of the current path, in value units.
	OpexNow float64
	// OpexAfter is the per-call cost once the skill exists. It is not zero:
	// a skill still costs compute and latency, just much less.
	OpexAfter float64
	// Capex is the one-time cost of producing and validating the skill.
	Capex float64
	// ExpectedCalls is how many times the skill is expected to run while wrong,
	// if it is wrong. It is separate from Calls because a poisoned skill may be
	// caught early — or may not.
	ExpectedCalls float64
	// Loss is what one poisoned execution costs. Usually far larger than the
	// per-call saving, which is the whole reason the term exists.
	Loss float64
	// At is the evidence level the skill would be fixed at.
	At evidence.Level
}

// PoisonProbability maps an evidence level to the chance a skill fixed at that
// level is wrong.
//
// These are judgements, not measurements, and they are written as a table so
// that changing one is a visible edit rather than a buried constant. The shape
// is what matters: E0 is a model's guess and is near-coin-flip; E4 has survived
// repeated use in production and is nearly free of poison.
//
// E4 is also the one level that cannot exist before fixing — it is produced by
// observing the skill after it is fixed. That is a legitimate feedback loop, and
// it is the opposite of whitewashing: evidence only rises by being used, never
// by being composed.
func PoisonProbability(l evidence.Level) (float64, bool) {
	switch l {
	case evidence.L0:
		return 0.5, true
	case evidence.L1:
		return 0.25, true
	case evidence.L2:
		return 0.10, true
	case evidence.L3:
		return 0.03, true
	case evidence.L4:
		return 0.005, true
	}
	return 0, false
}

// FixValue is the net value of fixing a path into a skill.
//
//	V_fix = Calls * (OpexNow - OpexAfter) - Capex - ExpectedCalls * P(poison) * Loss
//
// It returns the value and whether the evidence level was known. An unknown
// level is not treated as zero risk: a level nobody recognises is a level nobody
// has calibrated, so the caller must not get a number back at all.
func FixValue(f Fixing) (float64, bool) {
	p, ok := PoisonProbability(f.At)
	if !ok {
		return 0, false
	}
	saving := f.Calls * (f.OpexNow - f.OpexAfter)
	risk := f.ExpectedCalls * p * f.Loss
	return saving - f.Capex - risk, true
}

// ShouldFix answers the question the value function poses.
//
// The comparison is strict. Breaking even is not a reason to accumulate
// something: a skill that costs exactly what it saves adds a moving part, a
// licence obligation and a poison surface for no return.
func ShouldFix(f Fixing) (bool, float64, bool) {
	v, ok := FixValue(f)
	if !ok {
		return false, 0, false
	}
	return v > 0, v, true
}

// Promotion is the two-level ladder a captured path climbs.
type Promotion string

const (
	// PromotionScript is a captured path running under an interpreter.
	//
	// It is the first form because most captured code is a script, and because
	// the cost of being wrong is bounded: an interpreter that is restarted is
	// just a restart.
	PromotionScript Promotion = "script"
	// PromotionCompiled is the same path rebuilt as a compiled artifact.
	//
	// It is a promotion, not a rewrite: it happens only after the script has
	// proven itself, and the criteria are measurements rather than opinions.
	PromotionCompiled Promotion = "compiled"
)

// CompileCriteria is what a script must show before it is worth compiling.
type CompileCriteria struct {
	// MinCalls is the call count below which compilation is premature.
	MinCalls int
	// MinSuccess is the posterior mean required.
	MinSuccess float64
	// MinStartupShare is the fraction of wall-clock time spent starting the
	// interpreter. Below this the compiled form buys almost nothing: if a skill
	// does 1ms of work behind a 40ms startup, compiling removes 97.5% of the
	// cost; if startup is 5% of the time, it removes 5%.
	MinStartupShare float64
}

// DEAD-API: 编译升级那一级还没有接上：script → compiled 的判据有了，消费方没有
// DefaultCompileCriteria is the threshold set used when none is given.
func DefaultCompileCriteria() CompileCriteria {
	return CompileCriteria{MinCalls: 50, MinSuccess: 0.99, MinStartupShare: 0.5}
}

// DEAD-API: 同上：编译升级那一级还没有接上
// ShouldCompile applies the criteria to a script's track record.
func ShouldCompile(calls int, p Posterior, startupShare float64, c CompileCriteria) bool {
	if calls < c.MinCalls {
		return false
	}
	if p.Mean() < c.MinSuccess {
		return false
	}
	return startupShare >= c.MinStartupShare
}
