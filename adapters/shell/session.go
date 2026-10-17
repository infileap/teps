package main

import (
	"encoding/json"
	"sync"

	"teps/core"
	"teps/organs/session"
)

// A session action, as it appears on the chain.
//
// Sessions are stored nowhere else. The map below is a cache rebuilt by
// replaying the log — which is what makes "upgrade = swap the binary and
// replay the log" true for sessions as well, and not only for the kernel's
// own state. If the session machine lived in adapter memory, restarting the
// adapter would silently reset every conversation.
type sessionRecord struct {
	Rec      string  `json:"rec"`
	Session  string  `json:"session"`
	Op       string  `json:"op"`
	Event    string  `json:"event,omitempty"`
	Strategy string  `json:"strategy,omitempty"`
	Gain     float64 `json:"gain,omitempty"`
	Action   string  `json:"action,omitempty"`
}

type sessionStore struct {
	mu   sync.Mutex
	log  *core.Log
	live map[string]session.Machine

	// applied is the log position this store has incorporated. It exists so
	// that a repeated idempotency key — which the log answers with the
	// original entry and no second append — does not get applied twice.
	applied uint64
}

func newSessionStore(lg *core.Log) (*sessionStore, *core.Refusal) {
	st := &sessionStore{log: lg, live: make(map[string]session.Machine)}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

// absorb is the replay callback. The chain is shared with records that have
// nothing to do with sessions, so a body that is not a session record is
// skipped rather than treated as an error.
func (st *sessionStore) absorb(e core.Entry) error {
	var rec sessionRecord
	if err := json.Unmarshal(e.Body, &rec); err == nil && rec.Rec == "session" && rec.Session != "" {
		st.applyRecord(rec)
	}
	st.applied = e.Seq
	return nil
}

// applyRecord folds one session record into the machine. It is used both by
// replay and by the live path, so the two cannot drift apart.
func (st *sessionStore) applyRecord(rec sessionRecord) {
	switch rec.Op {
	case "open":
		if _, exists := st.live[rec.Session]; !exists {
			st.live[rec.Session] = session.New()
		}
	case "event":
		if m, ok := st.live[rec.Session]; ok {
			prev := m.State
			if next, ok := m.Step(session.Event(rec.Event)); ok {
				// A strategy is recorded when an attempt starts. The retry
				// path records it at the retry itself — the server must record
				// the strategy it actually checked, not one the client
				// supplies afterwards — so rerun must not record it twice.
				if next.State == session.Executing && prev != session.Retrying && rec.Strategy != "" {
					next = next.RecordAttempt(rec.Strategy)
				}
				st.live[rec.Session] = next
			}
		}
	case "observe":
		if m, ok := st.live[rec.Session]; ok {
			st.live[rec.Session] = m.Observe(rec.Gain, rec.Action)
		}
	case "retry":
		if m, ok := st.live[rec.Session]; ok {
			if next, ok := m.Retry(rec.Strategy); ok {
				st.live[rec.Session] = next
			}
		}
	}
}

func (st *sessionStore) get(name string) (session.Machine, *core.Refusal) {
	m, ok := st.live[name]
	if !ok {
		return session.Machine{}, core.Refuse(core.CodeNoSession, "no session "+name)
	}
	return m, nil
}

// commit writes the record durably and then folds it in.
//
// Durable first, applied second: if the process dies in between, the next
// replay re-applies the record and the session catches up. The reverse order
// would lose the action.
func (st *sessionStore) commit(rec sessionRecord, key string) (core.Entry, bool, *core.Refusal) {
	body, err := json.Marshal(rec)
	if err != nil {
		return core.Entry{}, false, core.Refuse(core.CodeBadRequest, err.Error())
	}
	e, ref := st.log.Append(key, body)
	if ref != nil {
		return core.Entry{}, false, ref
	}
	if e.Seq <= st.applied {
		// A repeat of an earlier key: the log returned the original entry
		// without appending. Folding it in again would double-apply it.
		return e, false, nil
	}
	st.applyRecord(rec)
	st.applied = e.Seq
	return e, true, nil
}

// handleSession serves the session.* frame types.
func (s *server) handleSession(f core.Frame, out *core.Frame) bool {
	var body struct {
		Session  string  `json:"session"`
		Event    string  `json:"event"`
		Strategy string  `json:"strategy"`
		Gain     float64 `json:"gain"`
		Action   string  `json:"action"`
	}
	// ★★★ 请求体一律走 `decodeBody`，不走 `json.Unmarshal`。
	//
	// 这里此前是自己解一遍 —— 而 `json.Unmarshal` **静默丢掉不认识的字段**，
	// 于是 `session.*` 的每一个帧都带着与 `arbiter.observe` 同一条病：
	// 一个拼错的字段名不会拒，它会把这句话**悄悄改成另一句**再照做。
	// `decodeBody` 现在拒未知字段，所以这一处必须与别处同一扇门。
	if len(f.Body) > 0 {
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return true
		}
	}

	st := s.sessions
	st.mu.Lock()
	defer st.mu.Unlock()

	switch f.T {
	case "session.open":
		if body.Session == "" {
			out.Err = core.Refuse(core.CodeBadRequest, "session name is required")
			return true
		}
		rec := sessionRecord{Rec: "session", Session: body.Session, Op: "open"}
		e, _, ref := st.commit(rec, f.Key)
		if ref != nil {
			out.Err = ref
			return true
		}
		m, _ := st.get(body.Session)
		out.Seq, out.Rev = e.Seq, e.Seq
		out.Body = mustJSON(map[string]any{"session": body.Session, "state": string(m.State), "seq": e.Seq})

	case "session.event":
		m, ref := st.get(body.Session)
		if ref != nil {
			out.Err = ref
			return true
		}
		ev := session.Event(body.Event)
		next, legal := session.Next(m.State, ev)
		if !legal {
			// Refused before anything is written: a refused request must
			// leave no trace on the chain.
			out.Err = core.Refuse(core.CodeBadRequest,
				"event "+body.Event+" is not legal in state "+string(m.State))
			return true
		}
		rec := sessionRecord{
			Rec: "session", Session: body.Session, Op: "event",
			Event: body.Event, Strategy: body.Strategy,
		}
		e, _, ref := st.commit(rec, f.Key)
		if ref != nil {
			out.Err = ref
			return true
		}
		out.Seq, out.Rev = e.Seq, e.Seq
		out.Body = mustJSON(map[string]any{"session": body.Session, "state": string(next), "seq": e.Seq})

	case "session.observe":
		if _, ref := st.get(body.Session); ref != nil {
			out.Err = ref
			return true
		}
		rec := sessionRecord{
			Rec: "session", Session: body.Session, Op: "observe",
			Gain: body.Gain, Action: body.Action,
		}
		e, _, ref := st.commit(rec, f.Key)
		if ref != nil {
			out.Err = ref
			return true
		}
		after, _ := st.get(body.Session)
		out.Seq, out.Rev = e.Seq, e.Seq
		out.Body = mustJSON(map[string]any{
			"session": body.Session,
			"steps":   len(after.Gains),
			"flags":   flagsJSON(after.Flags()),
		})

	case "session.retry":
		m, ref := st.get(body.Session)
		if ref != nil {
			out.Err = ref
			return true
		}
		if !session.MayRetry(m.Attempts, body.Strategy) {
			out.Err = core.Refuse(core.CodeBadRequest,
				"strategy "+body.Strategy+" has already been retried as often as the contract allows")
			return true
		}
		rec := sessionRecord{
			Rec: "session", Session: body.Session, Op: "retry", Strategy: body.Strategy,
		}
		e, _, ref := st.commit(rec, f.Key)
		if ref != nil {
			out.Err = ref
			return true
		}
		after, _ := st.get(body.Session)
		out.Seq, out.Rev = e.Seq, e.Seq
		out.Body = mustJSON(map[string]any{
			"session":              body.Session,
			"state":                string(after.State),
			"attempts":             after.Attempts,
			"must_change_strategy": after.Flags().MustChangeStrategy,
		})

	case "session.status":
		m, ref := st.get(body.Session)
		if ref != nil {
			out.Err = ref
			return true
		}
		out.Body = mustJSON(map[string]any{
			"session":  body.Session,
			"state":    string(m.State),
			"trail":    trailStrings(m.Trail),
			"attempts": nonNilStrings(m.Attempts),
			"gains":    m.Gains,
			"flags":    flagsJSON(m.Flags()),
		})

	default:
		return false
	}
	return true
}

func flagsJSON(f session.Flags) map[string]any {
	return map[string]any{
		"stalled": f.Stalled,
		"looping": f.Looping,
		"waste":   f.Waste,
		// ★ 一个比例必须带着它的两个计数出现，否则 `waste: 0` 读不出
		// 是「1 次升级 0 次以 degraded 收尾」还是「50 次升级 0 次」。
		"escalations":          f.Escalations,
		"escalations_degraded": f.EscalationsDegraded,
		"has_waste_evidence":   f.HasWasteEvidence,
		"must_change_strategy": f.MustChangeStrategy,
		"terminal":             f.Terminal,
	}
}

func trailStrings(trail []session.State) []string {
	out := make([]string, 0, len(trail))
	for _, s := range trail {
		out = append(out, string(s))
	}
	return out
}

// nonNilStrings keeps JSON output as [] rather than null, so a client can
// index it without a nil check.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
