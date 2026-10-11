package main

import (
	"testing"
	"time"

	"sansheng/core"
	"sansheng/organs/pool"
)

func testServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	lg, ref := core.Open(dir)
	if ref != nil {
		t.Fatalf("open log: %s", ref.Reason)
	}
	t.Cleanup(func() { lg.Close() })
	ps, ref := newPoolStore(lg)
	if ref != nil {
		t.Fatalf("open pool: %s", ref.Reason)
	}
	ds, ref := newDecisionStore(lg)
	if ref != nil {
		t.Fatalf("open decisions: %s", ref.Reason)
	}
	return &server{log: lg, pools: ps, decisions: ds}
}

func TestAdmitWithoutANameReservesNothing(t *testing.T) {
	s := testServer(t)
	a, ref := s.admit("att-1", "", false, core.Budget{MS: 100})
	if ref != nil {
		t.Fatalf("not naming a resource is not an error: %s", ref.Reason)
	}
	if a.Resource != "" || a.Holder != "" {
		t.Fatalf("nothing should have been reserved, got %+v", a)
	}
	// The pool is untouched: no lease was invented for a resource nobody
	// declared.
	if len(s.pools.leases) != 0 {
		t.Fatalf("no lease should exist, got %v", s.pools.leases)
	}
}

func TestAdmitRefusesARemoteResourceAndLeavesNoTrace(t *testing.T) {
	s := testServer(t)
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: "gpu.far", Kind: string(pool.KindRemote),
		MoneyPerMS: 0.002, Capacity: 2,
	}, "k1"); ref != nil {
		t.Fatalf("register: %s", ref.Reason)
	}
	before, _ := s.pools.lg.Head()

	_, ref := s.admit("att-2", "gpu.far", false, core.Budget{MS: 100})
	if ref == nil {
		t.Fatal("this executor cannot dispatch to a remote resource and must say so")
	}
	// ★ The ordering rule: an execution that could not be admitted must not
	// leave a paid-looking trace on the chain. Checking this by seq is the only
	// way to see it — the refusal text would look the same either way.
	after, _ := s.pools.lg.Head()
	if before != after {
		t.Fatalf("a refused admission must not write to the chain: head went %s -> %s", before, after)
	}
	if len(s.pools.leases) != 0 {
		t.Fatalf("no lease should have been taken, got %v", s.pools.leases)
	}
}

func TestAdmitRefusesAnUnknownResource(t *testing.T) {
	s := testServer(t)
	_, ref := s.admit("att-3", "ghost.0", false, core.Budget{MS: 100})
	if ref == nil {
		t.Fatal("an unknown resource id must be refused")
	}
	if ref.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", ref.Code)
	}
}

func TestPickWithNoLocalResourceIsRefused(t *testing.T) {
	s := testServer(t)
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: "gpu.far", Kind: string(pool.KindRemote), Capacity: 4,
	}, "k1"); ref != nil {
		t.Fatalf("register: %s", ref.Reason)
	}
	// A remote resource is not a candidate: picking among resources the
	// executor cannot use would produce an admission it cannot honour.
	if _, ref := s.admit("att-4", "", true, core.Budget{MS: 100}); ref == nil {
		t.Fatal("pick must have nothing to choose from and must refuse")
	}
}

func TestAdmitTakesASlotAndDischargeIsIdempotent(t *testing.T) {
	s := testServer(t)
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: "core.0", Kind: string(pool.KindLocal), Capacity: 1,
	}, "k1"); ref != nil {
		t.Fatalf("register: %s", ref.Reason)
	}
	a, ref := s.admit("att-5", "", true, core.Budget{MS: 100})
	if ref != nil {
		t.Fatalf("admit: %s", ref.Reason)
	}
	if a.Resource != "core.0" || a.Holder != "att-5" {
		t.Fatalf("want core.0 held by att-5, got %+v", a)
	}
	if _, ref := s.admit("att-6", "", true, core.Budget{MS: 100}); ref == nil {
		t.Fatal("capacity is 1, so a second admission must be refused")
	}
	s.discharge("att-5")
	s.discharge("att-5") // twice: releasing is not a one-shot
	if _, ref := s.admit("att-7", "", true, core.Budget{MS: 100}); ref != nil {
		t.Fatalf("after discharge the slot is free again: %s", ref.Reason)
	}
}

// ★ The lease must outlive the hard deadline.
//
// If it expires exactly when the process is killed, capacity comes back while a
// run is still finishing — and the failure only shows up as a slot that was
// briefly double-booked, which nothing notices until it matters.
//
// This test is written against the deadline, not against a ttl argument: the
// length is derived inside admit, so a caller cannot pass one that is too short.
func TestTheLeaseOutlivesTheHardDeadline(t *testing.T) {
	s := testServer(t)
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: "core.0", Kind: string(pool.KindLocal), Capacity: 1,
	}, "k1"); ref != nil {
		t.Fatalf("register: %s", ref.Reason)
	}
	const deadline = int64(1000)
	a, ref := s.admit("att-8", "", true, core.Budget{MS: deadline})
	if ref != nil {
		t.Fatalf("admit: %s", ref.Reason)
	}
	now := time.Now().UnixMilli()
	if a.Expires-now <= deadline {
		t.Fatalf("the lease expires at or before the deadline: expires in %dms, deadline %dms", a.Expires-now, deadline)
	}
}
