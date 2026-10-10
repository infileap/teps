package main

import (
	"encoding/json"
	"strings"

	"sansheng/core"
)

// audit.tail exposes the chain itself, not a summary of it.
//
// The contract has promised this frame since the contract was written, and the
// frame guard has been reporting it as "documented but nobody dispatches it"
// since the guard existed. A promise nobody implements is worse than no
// promise: a client writes against it and finds out at runtime.
//
// It is also the only frame that answers "what actually happened" without
// asking any organ. Every other op reports a projection — sessions, memory,
// posteriors — and every projection is somebody's reading. This one returns the
// records those readings are readings of.

type tailEntry struct {
	Seq  uint64          `json:"seq"`
	Key  string          `json:"key"`
	Prev string          `json:"prev"`
	Head string          `json:"head"`
	Body json.RawMessage `json:"body"`
}

// handleAudit dispatches audit.* frames.
func (s *server) handleAudit(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "audit.") {
		return false
	}
	switch f.T {
	case "audit.tail":
		s.auditTail(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) auditTail(f core.Frame, out *core.Frame) {
	var body struct {
		Limit int `json:"limit"`
	}
	// decodeBody refuses an absent body, which is right for an op that needs
	// fields. Every field here is optional, so requiring a body would make
	// "all fields are optional" a claim the API contradicts — and clients would
	// send {"limit":0} forever to satisfy it.
	if len(f.Body) > 0 {
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return
		}
	}
	// A default limit is not a silent decision: reading a tail without saying
	// how much of it is a request whose size depends on who is asking.
	if body.Limit == 0 {
		body.Limit = 20
	}
	entries, ref := s.log.Tail(body.Limit)
	if ref != nil {
		out.Err = ref
		return
	}
	rows := make([]tailEntry, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, tailEntry{Seq: e.Seq, Key: e.Key, Prev: e.Prev, Head: e.Head, Body: e.Body})
	}
	head, seq := s.log.Head()
	out.Body = mustJSON(map[string]any{
		"entries": rows,
		"head":    head,
		"seq":     seq,
		"epoch":   s.log.Epoch(),
	})
}
