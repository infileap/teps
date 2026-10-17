package session

// Machine is one session's state.
//
// It is a value type, and every method is pure: Step, Observe, RecordAttempt
// and Retry all return a new Machine and never touch the receiver. That makes
// a Machine safe to snapshot, diff and replay — which is what lets session
// state be reconstructed from the log instead of being migrated across an
// upgrade.
type Machine struct {
	State State

	// Attempts is the strategy used by each attempt, in order.
	Attempts []string

	// Gains is the information gain reported by each completed step.
	Gains []float64

	// Actions is the action hash of each completed step, for loop detection.
	Actions []string

	// Escalations counts entries into Waiting. EscalationsDegraded is how many
	// of them were followed by the session still ending degraded — which, for a
	// session that reaches Degraded at all, is all of them, because the count
	// is settled once at that moment (see Step).
	//
	// ★ 这条注释此前写着「the subset that ended in Degraded without a Grant in
	// between」——**它说的与代码和契约都不一样**。`Step` 在到达 `Degraded` 时
	// 写的是 `out.Escalations`（全部），而 `docs/CONTRACT.md:278` 写的是
	// 「升级之后仍然 degraded 的比例」。一次被 grant 过的升级，如果会话最后仍然
	// degraded，按契约它就是「升级之后仍然 degraded」的一次。
	Escalations         int
	EscalationsDegraded int

	// EscalationOpen is true between entering Waiting and either being
	// granted or giving up. It is what makes the waste ratio countable.
	EscalationOpen bool

	// Trail is every state this session has been in, in order.
	Trail []State
}

// New returns a session at the start of the machine.
func New() Machine {
	return Machine{State: Ready, Trail: []State{Ready}}
}

// clone copies every slice, so no returned Machine shares an array with the
// receiver. Without this, "pure" would be a claim rather than a property.
func (m Machine) clone() Machine {
	out := m
	out.Attempts = append([]string(nil), m.Attempts...)
	out.Gains = append([]float64(nil), m.Gains...)
	out.Actions = append([]string(nil), m.Actions...)
	out.Trail = append([]State(nil), m.Trail...)
	return out
}

// Step applies one event. A forbidden edge returns the receiver unchanged
// and ok = false.
func (m Machine) Step(e Event) (Machine, bool) {
	next, ok := Next(m.State, e)
	if !ok {
		return m, false
	}
	out := m.clone()
	out.State = next
	out.Trail = append(out.Trail, next)

	// Escalation bookkeeping lives here rather than in the caller, because
	// the waste ratio is only meaningful if it is derived from the same
	// transitions everything else is.
	//
	// WASTE is defined by the contract as the share of escalations after
	// which the session *still ended degraded*. That is a statement about the
	// session's outcome, not about how the escalation was answered:
	//
	//   - a denial that leads to a replan is not yet resolved, and if the
	//     replanned session succeeds, the escalation paid off;
	//   - a grant that is followed by another failure does not retroactively
	//     make the escalation a success.
	//
	// So the count is settled once, when the session reaches Degraded.
	if next == Waiting {
		out.Escalations++
		out.EscalationOpen = true
	} else if m.State == Waiting {
		out.EscalationOpen = false
	}
	if next == Degraded {
		out.EscalationsDegraded = out.Escalations
	}
	return out, true
}

// Observe records the gain and action hash of a completed step.
func (m Machine) Observe(gain float64, action string) Machine {
	out := m.clone()
	out.Gains = append(out.Gains, gain)
	out.Actions = append(out.Actions, action)
	return out
}

// RecordAttempt records the strategy an attempt is about to use.
func (m Machine) RecordAttempt(strategy string) Machine {
	out := m.clone()
	out.Attempts = append(out.Attempts, strategy)
	return out
}

// Retry applies a retry with the given strategy.
//
// It refuses a retry that would repeat the previous strategy more often than
// the contract allows. The refusal is a value (ok = false), not a panic and
// not a silent substitution: the caller must decide whether to change
// strategy or to escalate, and that decision is exactly the one we refuse to
// let the machine make on its own.
func (m Machine) Retry(strategy string) (Machine, bool) {
	if !MayRetry(m.Attempts, strategy) {
		return m, false
	}
	next, ok := m.Step(Retry)
	if !ok {
		return m, false
	}
	return next.RecordAttempt(strategy), true
}

// Flags is the guard reading for a session at this moment.
type Flags struct {
	// Stalled is true when the last StallStreak steps all gained less than
	// StallGain.
	Stalled bool

	// Looping is true when action hashes repeat too much over LoopWindow.
	Looping bool

	// Waste is the fraction of escalations that still ended degraded.
	//
	// ★ 它是一个**比例**，所以它必须与它的两个计数一起报：`0` 可以是
	// 「1 次升级、0 次以 degraded 收尾」，也可以是「50 次升级、0 次」。
	// 报一个比例而不报分母，读的人只能猜。
	Waste float64

	// Escalations and EscalationsDegraded are Waste's numerator and
	// denominator. They travel with it so the ratio can be read rather than
	// guessed.
	Escalations         int
	EscalationsDegraded int

	// HasWasteEvidence distinguishes "no escalations yet" from "escalations
	// that all succeeded". Both report Waste = 0.
	HasWasteEvidence bool

	// MustChangeStrategy is true when a further retry may not reuse the
	// strategy of the previous attempt.
	MustChangeStrategy bool

	// Terminal is true when the session has stopped: done or degraded.
	Terminal bool
}

// Flags reads the guards. It is a pure function of the recorded history.
func (m Machine) Flags() Flags {
	f := Flags{
		Stalled:          Stalled(m.Gains),
		Looping:          Looping(m.Actions),
		Waste:            WasteRatio(m.Escalations, m.EscalationsDegraded),
		HasWasteEvidence: m.Escalations > 0,
		// ★ 比例与它的分母一起走，同一次读取。分开取会让它们有机会说不同的年份。
		Escalations:         m.Escalations,
		EscalationsDegraded: m.EscalationsDegraded,
		Terminal:            Terminal(m.State),
	}
	if n := len(m.Attempts); n > 0 {
		f.MustChangeStrategy = !MayRetry(m.Attempts, m.Attempts[n-1])
	}
	return f
}

// DEAD-API: session.status 直接报 flags，没有走 Stuck 的入口
// Stuck reports whether the session should abandon its current approach.
//
// A stall or a loop is a reason to change strategy. It is not by itself a
// reason to give up: giving up is Degraded, and something has to decide that
// escalation is worth its price. That decision belongs to the arbiter.
func (m Machine) Stuck() bool {
	f := m.Flags()
	return f.Stalled || f.Looping
}
