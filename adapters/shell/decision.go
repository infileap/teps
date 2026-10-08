package main

import (
	"encoding/json"
	"math"
	"strings"

	"sansheng/core"
	"sansheng/evidence"
	"sansheng/organs/arbiter"
)

// The arbiter's table is a projection of the chain, like sessions and memory.
//
// It is tempting to keep the posterior in memory and update it in place: it is
// one map, and folding the whole log on every query looks wasteful. But a
// posterior kept in memory is a second source of truth about how often something
// worked, and the first time the process restarts the two disagree. The log
// already records every execution; the table is what you get by reading it.
//
// The practical consequence is that "the system noticed by itself" is auditable:
// replay the chain and you get the decision the live system made.

// round keeps a reported number readable without turning it into a string.
//
// A number rendered as text stops being a number: a client cannot compare it,
// sort it or sum it without parsing it back, and every client would parse it
// slightly differently.
func round(f float64) float64 { return math.Round(f*10000) / 10000 }

// executionRecord is one attempt, as it lands on the chain.
//
// Intent and resolution are two records, not one. An intent written before the
// attempt and a resolution written after are joined by Attempt, and a replay
// that finds an intent with no resolution learns something a single combined
// record cannot express: that the attempt happened and was never concluded.
// Recording that as a failure would be a guess; recording it as a success would
// be a lie.
type executionRecord struct {
	Rec       string  `json:"rec"`
	Attempt   string  `json:"attempt"`
	Skill     string  `json:"skill,omitempty"`
	TaskClass string  `json:"task_class,omitempty"`
	Strategy  string  `json:"strategy,omitempty"`
	OK        *bool   `json:"ok,omitempty"`
	Verified  string  `json:"verified_by,omitempty"`
	Latency   float64 `json:"latency,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
}

type decisionStore struct {
	mu chan struct{}
	lg *core.Log

	// obs is every concluded attempt, in chain order. It is what the table folds.
	obs []arbiter.Observation
	// open holds attempts that were started and not concluded.
	open    map[string]bool
	applied uint64
}

func newDecisionStore(lg *core.Log) (*decisionStore, *core.Refusal) {
	st := &decisionStore{mu: make(chan struct{}, 1), lg: lg, open: map[string]bool{}}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *decisionStore) lock()   { <-st.mu }
func (st *decisionStore) unlock() { st.mu <- struct{}{} }

func (st *decisionStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec executionRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "execution" {
		return nil
	}
	if rec.OK == nil {
		st.open[rec.Attempt] = true
		return nil
	}
	delete(st.open, rec.Attempt)
	st.obs = append(st.obs, arbiter.Observation{
		Key: arbiter.Key{Skill: rec.Skill, TaskClass: rec.TaskClass},
		OK:  *rec.OK,
	})
	return nil
}

func (st *decisionStore) table() arbiter.Table {
	return arbiter.Fold(st.obs, arbiter.DefaultDecay)
}

func (st *decisionStore) append(rec executionRecord, key string) (core.Entry, *core.Refusal) {
	canon, ref := core.Canonical(mustJSON(rec))
	if ref != nil {
		return core.Entry{}, ref
	}
	entry, ref := st.lg.Append(key, canon)
	if ref != nil {
		return core.Entry{}, ref
	}
	if entry.Seq > st.applied {
		st.absorb(entry)
	}
	return entry, nil
}

// handleDecision dispatches arbiter.* frames.
func (s *server) handleDecision(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "arbiter.") {
		return false
	}
	switch f.T {
	case "arbiter.observe":
		s.arbiterObserve(f, out)
	case "arbiter.rank":
		s.arbiterRank(f, out)
	case "arbiter.state":
		s.arbiterState(f, out)
	case "arbiter.fix":
		s.arbiterFix(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

// arbiterObserve records one attempt, and optionally its outcome.
func (s *server) arbiterObserve(f core.Frame, out *core.Frame) {
	var body struct {
		Attempt   string  `json:"attempt"`
		Skill     string  `json:"skill"`
		TaskClass string  `json:"task_class"`
		Strategy  string  `json:"strategy"`
		Outcome   string  `json:"outcome"`
		Verified  string  `json:"verified_by"`
		Latency   float64 `json:"latency"`
		Cost      float64 `json:"cost"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	if body.Attempt == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"an observation needs an attempt id: without it an intent can never be resolved")
		return
	}
	rec := executionRecord{
		Rec: "execution", Attempt: body.Attempt, Skill: body.Skill,
		TaskClass: body.TaskClass, Strategy: body.Strategy,
		Latency: body.Latency, Cost: body.Cost,
	}
	s.decisions.lock()
	_, known := s.decisions.open[body.Attempt]
	switch body.Outcome {
	case "":
		// An intent. It stays open until a resolution lands, and if none ever
		// does, the replay shows that rather than guessing an outcome.
		if known {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest, "attempt already open: "+body.Attempt)
			return
		}
		if body.Skill == "" || body.TaskClass == "" {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest, "an intent needs a skill and a task class")
			return
		}
	case "ok":
		yes := true
		rec.OK = &yes
		rec.Verified = body.Verified
	case "failed", "timeout", "refused", "degraded":
		no := false
		rec.OK = &no
		rec.Verified = body.Verified
	case "unknown":
		// "unknown" is what a reader infers from a missing resolution; it is not
		// a value anyone writes. Writing it would make it terminal — "this
		// attempt is concluded, and the conclusion is that we do not know" —
		// when the honest record is an intent that stays open and can still be
		// resolved by a human, by a second verifier, or by a re-run.
		//
		// A written unknown closes a door. An open intent leaves one open.
		s.decisions.unlock()
		out.Err = core.Refuse(core.CodeBadRequest,
			"unknown is a derived reading, not a written outcome: leave the intent open instead")
		return
	default:
		s.decisions.unlock()
		out.Err = core.Refuse(core.CodeBadRequest, "unknown outcome: "+body.Outcome)
		return
	}
	if rec.OK != nil {
		// The executor does not get to certify itself. A posterior built from
		// unverified outcomes measures optimism, not reliability.
		if rec.Verified == "" {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest,
				"an outcome needs verified_by: an unverified ok is the executor's own opinion")
			return
		}
		// A resolution whose intent was never written is refused. Accepting it
		// would make the chain's account of what happened depend on records that
		// are not in the chain.
		if !known {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest, "no open attempt to resolve: "+body.Attempt)
			return
		}
	}
	entry, ref := s.decisions.append(rec, f.Key)
	open := len(s.decisions.open)
	s.decisions.unlock()
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{"head": entry.Head, "open": open})
}

func (s *server) arbiterRank(f core.Frame, out *core.Frame) {
	var body struct {
		Margin float64     `json:"margin"`
		Offers []offerJSON `json:"offers"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	offers := make([]arbiter.Offer, 0, len(body.Offers))
	for _, o := range body.Offers {
		offers = append(offers, arbiter.Offer{
			Action: arbiter.Action(o.Action), Skill: o.Skill, TaskClass: o.TaskClass,
			Value: o.Value, Cost: o.Cost, Latency: o.Latency, LatencyWeight: o.LatencyWeight,
			Pinned: o.Pinned, UsePinned: o.UsePinned,
		})
	}
	s.decisions.lock()
	tbl := s.decisions.table()
	s.decisions.unlock()
	if body.Margin > 0 {
		esc, ref := arbiter.ShouldEscalate(offers, tbl, body.Margin)
		if ref != nil {
			out.Err = ref
			return
		}
		out.Body = mustJSON(map[string]any{
			"chosen": esc.Chosen.Offer.Action, "utility": round(esc.Chosen.Utility),
			"reason": esc.Reason, "threshold": body.Margin,
		})
		return
	}
	ranked := arbiter.Rank(offers, tbl)
	rows := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		rows = append(rows, map[string]any{
			"action": r.Offer.Action, "skill": r.Offer.Skill,
			"utility": round(r.Utility), "p": round(r.Prob),
		})
	}
	out.Body = mustJSON(map[string]any{"ranked": rows})
}

func (s *server) arbiterState(f core.Frame, out *core.Frame) {
	var body struct {
		Skill     string `json:"skill"`
		TaskClass string `json:"task_class"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	s.decisions.lock()
	tbl := s.decisions.table()
	open := len(s.decisions.open)
	n := len(s.decisions.obs)
	s.decisions.unlock()
	if body.Skill != "" && body.TaskClass != "" {
		p := tbl.Get(arbiter.Key{Skill: body.Skill, TaskClass: body.TaskClass})
		out.Body = mustJSON(map[string]any{
			"skill": body.Skill, "task_class": body.TaskClass,
			"alpha": round(p.Alpha), "beta": round(p.Beta),
			"mean": round(p.Mean()), "upper": round(p.UpperBound()),
			"observations": n, "open": open,
		})
		return
	}
	rows := make([]map[string]any, 0, len(tbl))
	for k, p := range tbl {
		rows = append(rows, map[string]any{
			"skill": k.Skill, "task_class": k.TaskClass,
			"mean": round(p.Mean()), "upper": round(p.UpperBound()),
		})
	}
	out.Body = mustJSON(map[string]any{"posteriors": rows, "observations": n, "open": open})
}

func (s *server) arbiterFix(f core.Frame, out *core.Frame) {
	var body struct {
		Calls         float64 `json:"calls"`
		OpexNow       float64 `json:"opex_now"`
		OpexAfter     float64 `json:"opex_after"`
		Capex         float64 `json:"capex"`
		ExpectedCalls float64 `json:"expected_calls"`
		Loss          float64 `json:"loss"`
		At            string  `json:"at"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	v, ok := arbiter.FixValue(arbiter.Fixing{
		Calls: body.Calls, OpexNow: body.OpexNow, OpexAfter: body.OpexAfter,
		Capex: body.Capex, ExpectedCalls: body.ExpectedCalls, Loss: body.Loss,
		At: evidence.Level(body.At),
	})
	if !ok {
		out.Err = core.Refuse(core.CodeBadRequest, "unknown evidence level: "+body.At)
		return
	}
	out.Body = mustJSON(map[string]any{"value": round(v), "fix": v > 0})
}

type offerJSON struct {
	Action        string  `json:"action"`
	Skill         string  `json:"skill"`
	TaskClass     string  `json:"task_class"`
	Value         float64 `json:"value"`
	Cost          float64 `json:"cost"`
	Latency       float64 `json:"latency"`
	LatencyWeight float64 `json:"latency_weight"`
	Pinned        float64 `json:"pinned"`
	UsePinned     bool    `json:"use_pinned"`
}
