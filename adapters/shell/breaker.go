package main

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"teps/core"
	"teps/organs/breaker"
)

// The circuit breaker, projected from the chain like every other table here.
//
// A breaker kept in memory would forget every trip on restart, which is exactly
// the failure it exists to prevent: a host that has been failing for an hour
// would look brand new. So a trip is a record, and the live table is what you
// get by reading them.
//
// One decision is worth stating: **a trip is written when it changes something.**
// A host failure is always written — it is the fact the breaker is made of. A
// host success is written only when that host already has a run of failures on
// record, because that is the only moment a success changes the state. Writing
// every success would make the chain grow with the traffic rather than with the
// history.

type breakerRecord struct {
	Rec  string `json:"rec"`
	Host string `json:"host"`
	OK   bool   `json:"ok"`
	At   int64  `json:"at"`
}

type breakerStore struct {
	mu chan struct{}
	lg *core.Log

	trips   []breaker.Trip
	applied uint64
}

func newBreakerStore(lg *core.Log) (*breakerStore, *core.Refusal) {
	st := &breakerStore{mu: make(chan struct{}, 1), lg: lg}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *breakerStore) lock()   { <-st.mu }
func (st *breakerStore) unlock() { st.mu <- struct{}{} }

func (st *breakerStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec breakerRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "breaker" {
		return nil
	}
	st.trips = append(st.trips, breaker.Trip{Host: rec.Host, OK: rec.OK, At: rec.At})
	return nil
}

func (st *breakerStore) append(rec breakerRecord, key string) (core.Entry, *core.Refusal) {
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

// consecutive counts back from the end, the same way the organ does.
func (st *breakerStore) consecutive(host string) int {
	n := 0
	for i := len(st.trips) - 1; i >= 0; i-- {
		if st.trips[i].Host != host {
			continue
		}
		if st.trips[i].OK {
			break
		}
		n++
	}
	return n
}

// allowHost refuses a spawn when the host's breaker is open.
//
// The host is the interpreter, because that is what actually fails. Two skills
// that share a broken interpreter share the breaker, which is the point: the
// thing that is broken is the thing that runs them.
func (s *server) allowHost(host string) *core.Refusal {
	if s.breakers == nil || host == "" {
		return nil
	}
	now := time.Now().UnixMilli()
	s.breakers.lock()
	defer s.breakers.unlock()
	return breaker.Allow(s.breakers.trips, host, now)
}

// recordTrip writes the attempt's effect on the breaker.
//
// It is called after every spawn that got as far as a verdict. A host failure is
// always written; a success is written only when it is the one that resets a
// run, because that is the only time it changes the answer.
func (s *server) recordTrip(host, verdict, attempt string) {
	if s.breakers == nil || host == "" {
		return
	}
	ok := !breaker.IsHostFailure(verdict)
	s.breakers.lock()
	defer s.breakers.unlock()
	if ok && s.breakers.consecutive(host) == 0 {
		return
	}
	s.breakers.append(breakerRecord{
		Rec: "breaker", Host: host, OK: ok, At: time.Now().UnixMilli(),
	}, attempt+keyTrip)
}

const keyTrip = ":trip"

func (s *server) handleBreaker(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "breaker.") {
		return false
	}
	switch f.T {
	case "breaker.state":
		s.breakerState(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) breakerState(f core.Frame, out *core.Frame) {
	now := time.Now().UnixMilli()
	s.breakers.lock()
	trips := append([]breaker.Trip(nil), s.breakers.trips...)
	s.breakers.unlock()

	rows := make([]map[string]any, 0)
	open := 0
	for _, h := range breaker.Hosts(trips) {
		st := breaker.StateOf(trips, h, now)
		if st != breaker.Closed {
			open++
		}
		row := map[string]any{"host": h, "state": string(st)}
		if st == breaker.Open {
			if ref := breaker.Allow(trips, h, now); ref != nil {
				row["reason"] = ref.Reason
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["host"].(string) < rows[j]["host"].(string) })
	out.Body = mustJSON(map[string]any{
		"hosts": rows,
		"open":  open,
		// The two numbers the state is made of, reported next to it so a reader
		// can check the state rather than take it.
		"threshold":   breaker.Threshold,
		"cooldown_ms": breaker.CooldownMS,
		"trips":       len(trips),
	})
}
