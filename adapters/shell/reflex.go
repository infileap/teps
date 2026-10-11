package main

import (
	"encoding/json"
	"strings"

	"teps/core"
	"teps/organs/reflex"
)

// The reflex table is a projection of the chain, like sessions, memory and the
// posterior.
//
// Every rule's strength is a function of the observations recorded about it, in
// order — which is the only way "established / modified / extinguished" can be
// audited. A table kept in memory and updated in place would be a second source
// of truth about how often something worked, and the first restart would make
// the two disagree.
//
// # Why there is no intercept rate here
//
// The design note claims the reflex layer answers 60–80% of requests with no
// context. That number is not reported, and its absence is deliberate: the rate
// needs a denominator, the denominator is "requests that arrived", and this
// system has no request intake. Dividing by the number of reflex.match calls
// would produce a number that looks like the claim and measures nothing — the
// same mistake as reading a formula with an undefined factor as a conclusion.
//
// What is reported is the table itself: how many rules exist, how many fire, how
// many have been extinguished. Those are counts of things that actually
// happened.

type reflexRecord struct {
	Rec      string `json:"rec"`
	ID       string `json:"rule_id"`
	Stimulus string `json:"stimulus,omitempty"`
	Action   string `json:"action,omitempty"`
	Outcome  string `json:"outcome"`
}

type reflexStore struct {
	mu      chan struct{}
	log     *core.Log
	rules   []reflex.Rule
	applied uint64
}

func newReflexStore(lg *core.Log) (*reflexStore, *core.Refusal) {
	st := &reflexStore{mu: make(chan struct{}, 1), log: lg}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *reflexStore) lock()   { <-st.mu }
func (st *reflexStore) unlock() { st.mu <- struct{}{} }

func (st *reflexStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec reflexRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "reflex" {
		return nil
	}
	st.applyRecord(rec)
	return nil
}

// applyRecord is shared by replay and the live path, so the two cannot drift.
func (st *reflexStore) applyRecord(rec reflexRecord) *core.Refusal {
	idx := -1
	for i := range st.rules {
		if st.rules[i].ID == rec.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		r, ref := reflex.New(rec.ID, rec.Stimulus, rec.Action)
		if ref != nil {
			return ref
		}
		st.rules = append(st.rules, r)
		idx = len(st.rules) - 1
	}
	switch rec.Outcome {
	case "ok":
		st.rules[idx] = reflex.Reinforce(st.rules[idx])
	case "failed":
		st.rules[idx] = reflex.Weaken(st.rules[idx])
	case "idle":
		// Extinction is an observation too: it has to be recorded, because it
		// changes what the layer will answer. A rule that quietly stopped firing
		// without a record would make the table unexplainable.
		st.rules[idx] = reflex.Decay(st.rules[idx], 1)
	default:
		return core.Refuse(core.CodeBadRequest, "unknown reflex outcome: "+rec.Outcome)
	}
	return nil
}

func (st *reflexStore) commit(rec reflexRecord, key string) (core.Entry, *core.Refusal) {
	canon, ref := core.Canonical(mustJSON(rec))
	if ref != nil {
		return core.Entry{}, ref
	}
	entry, ref := st.log.Append(key, canon)
	if ref != nil {
		return core.Entry{}, ref
	}
	if entry.Seq > st.applied {
		st.applyRecord(rec)
	}
	return entry, nil
}

// handleReflex dispatches reflex.* frames.
func (s *server) handleReflex(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "reflex.") {
		return false
	}
	if s.reflexes == nil {
		out.Err = core.Refuse(core.CodeBadRequest, "no reflex table is configured on this server")
		return true
	}
	switch f.T {
	case "reflex.observe":
		s.reflexObserve(f, out)
	case "reflex.match":
		s.reflexMatch(f, out)
	case "reflex.state":
		s.reflexState(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) reflexObserve(f core.Frame, out *core.Frame) {
	var body struct {
		RuleID   string `json:"rule_id"`
		Stimulus string `json:"stimulus"`
		Action   string `json:"action"`
		Outcome  string `json:"outcome"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	if body.RuleID == "" {
		out.Err = core.Refuse(core.CodeBadRequest, "an observation needs a rule_id: without it a rule can never be reinforced, weakened or extinguished")
		return
	}
	rec := reflexRecord{Rec: "reflex", ID: body.RuleID, Stimulus: body.Stimulus, Action: body.Action, Outcome: body.Outcome}
	s.reflexes.lock()
	// The shape check runs against a copy of the *real* table, never a fresh one.
	// A rule that already exists does not need its stimulus restated; a rule that
	// does not exist cannot be observed without one. Checking against an empty
	// table would accept a second rule with an existing id and quietly drop the
	// first — the failure mode memory hit in P3.
	probe := s.reflexes.clone()
	if ref := probe.applyRecord(rec); ref != nil {
		s.reflexes.unlock()
		out.Err = ref
		return
	}
	entry, ref := s.reflexes.commit(rec, f.Key)
	rules := len(s.reflexes.rules)
	s.reflexes.unlock()
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{"head": entry.Head, "rules": rules})
}

// clone copies the rule table so a candidate observation can be checked without
// touching the live one.
func (st *reflexStore) clone() *reflexStore {
	c := &reflexStore{rules: make([]reflex.Rule, len(st.rules))}
	copy(c.rules, st.rules)
	return c
}

func (s *server) reflexMatch(f core.Frame, out *core.Frame) {
	var body struct {
		Stimulus string `json:"stimulus"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	s.reflexes.lock()
	rule, ok := reflex.Match(body.Stimulus, s.reflexes.rules)
	s.reflexes.unlock()
	if !ok {
		out.Body = mustJSON(map[string]any{
			"matched": false,
			// Stated rather than implied: the reflex path composes no context at
			// all, and a caller that cannot see that would have no reason to
			// prefer it.
			"context_tokens": 0,
		})
		return
	}
	out.Body = mustJSON(map[string]any{
		"matched": true, "rule_id": rule.ID, "action": rule.Action,
		"strength": round(rule.Strength), "pattern": rule.Pattern,
		"context_tokens": 0,
	})
}

func (s *server) reflexState(f core.Frame, out *core.Frame) {
	s.reflexes.lock()
	rules := make([]map[string]any, 0, len(s.reflexes.rules))
	firing, extinguished := 0, 0
	for _, r := range s.reflexes.rules {
		fires := reflex.Fires(r)
		if fires {
			firing++
		} else if r.Fires > 0 {
			// It fired at least once and no longer does: that is extinction, as
			// opposed to a rule that never got strong enough to start.
			extinguished++
		}
		rules = append(rules, map[string]any{
			"id": r.ID, "pattern": r.Pattern, "action": r.Action,
			"strength": round(r.Strength), "fires": fires, "firings": r.Fires,
		})
	}
	s.reflexes.unlock()
	out.Body = mustJSON(map[string]any{
		"rules": rules, "total": len(rules),
		"firing": firing, "extinguished": extinguished,
		"threshold": reflex.FireThreshold,
	})
}
