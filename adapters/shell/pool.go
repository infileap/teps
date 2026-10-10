package main

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"sansheng/core"
	"sansheng/organs/pool"
)

// The compute registry, projected from the chain like everything else.
//
// A registry kept in memory would be a second source of truth about what
// machines exist, and the first restart would make the two disagree. So a
// registration and a lease are records on the chain, and the live tables are
// what you get by reading it.
//
// One decision is worth stating: **a lease record stores the absolute expiry,
// not the ttl.** The server stamps it from its own clock at the moment the
// lease is taken, so replaying the chain produces the same expiry. If the ttl
// were stored instead, every replay would recompute it against a different
// "now" and the same chain would describe different pools.

type poolRecord struct {
	Rec  string `json:"rec"`
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind,omitempty"`
	// The price, as declared.
	MoneyPerMS    float64 `json:"money_per_ms,omitempty"`
	MoneyPerToken float64 `json:"money_per_token,omitempty"`
	Capacity      int     `json:"capacity,omitempty"`
	Priority      int     `json:"priority,omitempty"`
	Note          string  `json:"note,omitempty"`
	// The lease, as taken.
	Holder  string `json:"holder,omitempty"`
	Expires int64  `json:"expires,omitempty"`
}

type poolStore struct {
	mu chan struct{}
	lg *core.Log

	resources []pool.Resource
	leases    []pool.Lease
	applied   uint64
}

func newPoolStore(lg *core.Log) (*poolStore, *core.Refusal) {
	st := &poolStore{mu: make(chan struct{}, 1), lg: lg}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *poolStore) lock()   { <-st.mu }
func (st *poolStore) unlock() { st.mu <- struct{}{} }

func (st *poolStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec poolRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "pool" {
		return nil
	}
	st.apply(rec)
	return nil
}

// apply folds one record. It has no failure mode, which is what lets the replay
// path and the live path share it: a record that reached the chain already
// passed its checks.
func (st *poolStore) apply(rec poolRecord) {
	switch rec.Op {
	case "register":
		st.resources = append(st.resources, pool.Resource{
			ID: rec.ID, Kind: pool.Kind(rec.Kind),
			MoneyPerMS: rec.MoneyPerMS, MoneyPerToken: rec.MoneyPerToken,
			Capacity: rec.Capacity, Priority: rec.Priority, Note: rec.Note,
		})
	case "lease":
		st.leases = append(st.leases, pool.Lease{
			Resource: rec.ID, Holder: rec.Holder, Expires: rec.Expires,
		})
	case "release":
		st.leases = pool.Release(st.leases, rec.Holder)
	}
}

func (st *poolStore) append(rec poolRecord, key string) (core.Entry, *core.Refusal) {
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

func (st *poolStore) find(id string) (pool.Resource, bool) {
	for _, r := range st.resources {
		if r.ID == id {
			return r, true
		}
	}
	return pool.Resource{}, false
}

// handlePool dispatches pool.* frames.
func (s *server) handlePool(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "pool.") {
		return false
	}
	switch f.T {
	case "pool.register":
		s.poolRegister(f, out)
	case "pool.lease":
		s.poolLease(f, out)
	case "pool.release":
		s.poolRelease(f, out)
	case "pool.quote":
		s.poolQuote(f, out)
	case "pool.state":
		s.poolState(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) poolRegister(f core.Frame, out *core.Frame) {
	var body struct {
		ID            string  `json:"id"`
		Kind          string  `json:"kind"`
		MoneyPerMS    float64 `json:"money_per_ms"`
		MoneyPerToken float64 `json:"money_per_token"`
		Capacity      int     `json:"capacity"`
		Priority      int     `json:"priority"`
		Note          string  `json:"note"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	r := pool.Resource{
		ID: body.ID, Kind: pool.Kind(body.Kind),
		MoneyPerMS: body.MoneyPerMS, MoneyPerToken: body.MoneyPerToken,
		Capacity: body.Capacity, Priority: body.Priority, Note: body.Note,
	}
	if ref := pool.Validate(r); ref != nil {
		out.Err = ref
		return
	}
	s.pools.lock()
	defer s.pools.unlock()
	if _, exists := s.pools.find(r.ID); exists {
		// Re-registering an id would either replace a resource while leases
		// point at it, or silently do nothing. Both are worse than a refusal
		// that names the id.
		out.Err = core.Refuse(core.CodeBadRequest, "resource already registered: "+r.ID)
		return
	}
	entry, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: r.ID, Kind: string(r.Kind),
		MoneyPerMS: r.MoneyPerMS, MoneyPerToken: r.MoneyPerToken,
		Capacity: r.Capacity, Priority: r.Priority, Note: r.Note,
	}, f.Key)
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{"id": r.ID, "head": entry.Head})
}

func (s *server) poolLease(f core.Frame, out *core.Frame) {
	var body struct {
		ID      string `json:"id"`
		Holder  string `json:"holder"`
		TTLMS   int64  `json:"ttl_ms"`
		Attempt string `json:"attempt"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	now := time.Now().UnixMilli()
	s.pools.lock()
	defer s.pools.unlock()
	r, ok := s.pools.find(body.ID)
	if !ok {
		out.Err = core.Refuse(core.CodeBadRequest, "no such resource: "+body.ID)
		return
	}
	lease, ref := pool.Acquire(r, s.pools.leases, body.Holder, now, body.TTLMS)
	if ref != nil {
		out.Err = ref
		return
	}
	entry, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "lease", ID: r.ID, Holder: lease.Holder, Expires: lease.Expires,
	}, f.Key)
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{
		"id": r.ID, "holder": lease.Holder, "expires": lease.Expires,
		"used": len(pool.Live(s.pools.leases, r.ID, now)), "capacity": r.Capacity,
	})
}

func (s *server) poolRelease(f core.Frame, out *core.Frame) {
	var body struct {
		Holder string `json:"holder"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	if body.Holder == "" {
		out.Err = core.Refuse(core.CodeBadRequest, "a release needs a holder")
		return
	}
	s.pools.lock()
	defer s.pools.unlock()
	entry, ref := s.pools.append(poolRecord{Rec: "pool", Op: "release", Holder: body.Holder}, f.Key)
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{"holder": body.Holder, "head": entry.Head})
}

// poolQuote prices work without taking anything.
//
// Read-only, like escalation.quote and for the same reason: an estimator that
// charges would teach the system to stop estimating.
func (s *server) poolQuote(f core.Frame, out *core.Frame) {
	var body struct {
		MS       int64 `json:"ms"`
		Tokens   int64 `json:"tokens"`
		Money    int64 `json:"available_money"`
		AvTokens int64 `json:"available_tokens"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	work := core.Budget{MS: body.MS, Tokens: body.Tokens}
	available := core.Budget{Money: body.Money, Tokens: body.AvTokens}
	now := time.Now().UnixMilli()
	s.pools.lock()
	rs := append([]pool.Resource(nil), s.pools.resources...)
	ls := append([]pool.Lease(nil), s.pools.leases...)
	s.pools.unlock()
	best, ref := pool.Cheapest(rs, work, available, ls, now)
	if ref != nil {
		out.Err = ref
		return
	}
	c := pool.Cost(best, work)
	out.Body = mustJSON(map[string]any{
		"id": best.ID, "kind": string(best.Kind), "note": best.Note,
		"money": c.Money, "tokens": c.Tokens,
		"free": best.MoneyPerMS == 0 && best.MoneyPerToken == 0,
	})
}

func (s *server) poolState(f core.Frame, out *core.Frame) {
	now := time.Now().UnixMilli()
	s.pools.lock()
	rs := append([]pool.Resource(nil), s.pools.resources...)
	ls := append([]pool.Lease(nil), s.pools.leases...)
	s.pools.unlock()

	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	rows := make([]map[string]any, 0, len(rs))
	total := 0
	for _, r := range rs {
		live := pool.Live(ls, r.ID, now)
		held := make([]string, 0, len(live))
		for _, l := range live {
			held = append(held, l.Holder)
		}
		sort.Strings(held)
		total += r.Capacity
		rows = append(rows, map[string]any{
			"id": r.ID, "kind": string(r.Kind),
			"capacity": r.Capacity, "used": len(live), "held_by": held,
			"money_per_ms": r.MoneyPerMS, "money_per_token": r.MoneyPerToken,
			"note": r.Note,
		})
	}
	out.Body = mustJSON(map[string]any{
		"resources": rows,
		// Leases that have expired are not counted anywhere, which is the
		// point: a dead holder must not shrink the pool.
		"capacity": total,
		"used":     liveCount(ls, now),
	})
}

// liveCount is the aggregate of Live over every resource at once.
func liveCount(ls []pool.Lease, now int64) int {
	n := 0
	for _, l := range ls {
		if l.Expires > now {
			n++
		}
	}
	return n
}
