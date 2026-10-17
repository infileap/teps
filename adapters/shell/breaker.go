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
	open, halfOpen, closed := 0, 0, 0
	for _, h := range breaker.Hosts(trips) {
		st := breaker.StateOf(trips, h, now)
		// ★★★ 三个状态、三个计数。
		//
		// 此前这里是 `if st != breaker.Closed { open++ }` —— 一个叫 `open` 的数
		// 数的是**不是闭的**，也就是 `open + half-open`。实测（同一个宿主，
		// 三次失败之后）：
		//
		//   冷却 30s 内  -> {"open":1, hosts:[{"state":"open"}]}
		//   冷却过去之后 -> {"open":1, hosts:[{"state":"half-open"}]}
		//
		// **同一个 1，两个状态。** 而这两个状态在操作上是相反的：`open` 拒绝一切，
		// `half-open` **放一个探针进去**。一个读 `open: 1` 以为「不会有东西被尝试」
		// 的人，在第二种情况下正好读反了。
		switch st {
		case breaker.Open:
			open++
		case breaker.HalfOpen:
			halfOpen++
		default:
			closed++
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
		// ★ 三个状态三个计数。`open` 只数真的开着的那些；半开的**不是**开着的 ——
		// 它正在放一个探针，那是这一层唯一在做的事。
		"open":        open,
		"half_open":   halfOpen,
		"closed":      closed,
		"hosts_total": len(rows),
		// The two numbers the state is made of, reported next to it so a reader
		// can check the state rather than take it.
		"threshold":   breaker.Threshold,
		"cooldown_ms": breaker.CooldownMS,
		"trips":       len(trips),
		// 一句话说清这三个计数与 hosts 里那些 state 是同一批事实：
		// 否则读的人会把 `open` 当成「有多少宿主不闭」。
		"basis": "open / half_open / closed partition the hosts in this answer, one host each: " +
			"open refuses every attempt, half-open admits exactly one probe, closed admits all. " +
			"half-open is not open — a count named open that also counted half-open would " +
			"read as \"nothing will be tried\" at the one moment something is being tried",
	})
}
