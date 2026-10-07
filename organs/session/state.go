// Package session is the session state machine and its stall guards.
//
// It is an organ, not the kernel. The kernel has exactly four concepts
// (Frame, Log, Budget, Refusal) and knows nothing about sessions.
//
// Everything here is a pure function of explicit inputs: no clock, no I/O,
// no randomness, no hidden state. That is what makes it testable — and it is
// also the reason it does not belong in the kernel. A thing that is a pure
// function needs neither unique authority, nor atomicity, nor to be on every
// path.
package session

// State is one node of the session state machine.
type State string

const (
	Ready         State = "ready"
	Routing       State = "routing"
	Planning      State = "planning"
	Executing     State = "executing"
	Verifying     State = "verifying"
	Retrying      State = "retrying"
	Escalating    State = "escalating"
	Waiting       State = "waiting"
	Rearbitrating State = "rearbitrating"
	Done          State = "done"
	Degraded      State = "degraded"
)

// AllStates lists every state. The exhaustiveness test walks this list, so a
// new state cannot be added without the table being revisited.
var AllStates = []State{
	Ready, Routing, Planning, Executing, Verifying, Retrying,
	Escalating, Waiting, Rearbitrating, Done, Degraded,
}

// Event is one edge of the session state machine.
type Event string

const (
	Open     Event = "open"
	Route    Event = "route"
	Plan     Event = "plan"
	Planned  Event = "planned"
	Finish   Event = "finish"
	Pass     Event = "pass"
	Retry    Event = "retry"
	Escalate Event = "escalate"
	Quoted   Event = "quoted"
	Grant    Event = "grant"
	Timeout  Event = "timeout"
	Deny     Event = "deny"
	Replan   Event = "replan"
	Abandon  Event = "abandon"
	Rerun    Event = "rerun"
)

// AllEvents lists every event, for the same reason as AllStates.
var AllEvents = []Event{
	Open, Route, Plan, Planned, Finish, Pass, Retry, Escalate,
	Quoted, Grant, Timeout, Deny, Replan, Abandon, Rerun,
}

// transitions is the machine, written as data.
//
// A table rather than a switch, on purpose: a table can be diffed, printed
// into a report, and read by someone who is not a Go programmer. Rust would
// refuse to compile a non-exhaustive match; Go will not, so the substitute is
// TestEveryPairIsAccountedFor, which enumerates all states × events and
// compares the allowed edges against a list written out by hand.
//
// A missing entry means "not allowed" — never "forgot".
var transitions = map[State]map[Event]State{
	Ready:         {Open: Routing},
	Routing:       {Route: Executing, Plan: Planning},
	Planning:      {Planned: Executing},
	Executing:     {Finish: Verifying},
	Verifying:     {Pass: Done, Retry: Retrying, Escalate: Escalating},
	Retrying:      {Rerun: Executing},
	Escalating:    {Quoted: Waiting},
	Waiting:       {Grant: Executing, Timeout: Degraded, Deny: Rearbitrating},
	Rearbitrating: {Replan: Planning, Abandon: Degraded},
	Done:          {},
	Degraded:      {},
}

// Next applies one event. ok is false when the edge does not exist.
//
// An unknown state, an unknown event and a known-but-forbidden pair are all
// the same answer here: no move. The caller does not get to guess.
func Next(s State, e Event) (State, bool) {
	if byEvent, known := transitions[s]; known {
		if next, ok := byEvent[e]; ok {
			return next, true
		}
	}
	return s, false
}

// Known reports whether s is a state of this machine.
func Known(s State) bool {
	_, ok := transitions[s]
	return ok
}

// Terminal reports whether s is a known state with no outgoing edge.
// Done and Degraded are the two ways a session stops.
func Terminal(s State) bool {
	byEvent, ok := transitions[s]
	return ok && len(byEvent) == 0
}

// Edges returns every allowed edge as "state+event->next", in the order the
// exhaustiveness test expects.
func Edges() []string {
	var out []string
	for _, s := range AllStates {
		for _, e := range AllEvents {
			if next, ok := Next(s, e); ok {
				out = append(out, string(s)+"+"+string(e)+"->"+string(next))
			}
		}
	}
	return out
}
