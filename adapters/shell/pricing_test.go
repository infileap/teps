package main

import (
	"testing"

	"sansheng/organs/arbiter"

	"sansheng/core"
	"sansheng/organs/pool"
)

// ★ Two offers may carry the same action name — two different skills are both
// "skill" actions. A response that keyed its per-offer notes by action would
// attribute one offer's cost to the other, and it would look completely normal.
//
// This is the same failure the project keeps paying for: one fact with two
// copies, and only one of them updated. Here the fact is "which resource priced
// this offer".
func TestTwoOffersWithTheSameActionKeepTheirOwnCosts(t *testing.T) {
	s := testServer(t)
	for _, r := range []poolRecord{
		{Rec: "pool", Op: "register", ID: "core.cheap", Kind: string(pool.KindLocal), MoneyPerMS: 0.001, Capacity: 4},
		{Rec: "pool", Op: "register", ID: "core.dear", Kind: string(pool.KindLocal), MoneyPerMS: 0.05, Capacity: 4},
	} {
		if _, ref := s.pools.append(r, "k-"+r.ID); ref != nil {
			t.Fatalf("register %s: %s", r.ID, ref.Reason)
		}
	}

	in := []offerJSON{
		{Action: "skill", Skill: "a", Value: 10, Resource: "core.cheap", MS: 1000},
		{Action: "skill", Skill: "b", Value: 10, Resource: "core.dear", MS: 1000},
	}
	offers, meta, skipped, ref := s.resolveOffers(in, arbiter.Table{})
	if ref != nil {
		t.Fatalf("resolve: %s", ref.Reason)
	}
	if len(offers) != 2 || len(meta) != 2 || len(skipped) != 0 {
		t.Fatalf("want two offers and two notes, got %d/%d/%d", len(offers), len(meta), len(skipped))
	}
	if offers[0].Cost == offers[1].Cost {
		t.Fatalf("the two resources have different prices, so the costs must differ: both %v", offers[0].Cost)
	}
	if meta[0]["cost"] == meta[1]["cost"] {
		t.Fatalf("each offer must keep its own note: both %v", meta[0]["cost"])
	}
	// The notes must line up with the offers, not merely differ.
	if meta[0]["resource"] != "core.cheap" || meta[1]["resource"] != "core.dear" {
		t.Fatalf("notes are misaligned: %v / %v", meta[0]["resource"], meta[1]["resource"])
	}
	// And the tag must be what makes the lookup work.
	if got := metaAt(meta, offers[1].Tag); got["resource"] != "core.dear" {
		t.Fatalf("tag lookup returned %v", got)
	}
	if metaAt(meta, "99") != nil {
		t.Fatal("an out-of-range tag must return nothing rather than a wrong note")
	}
}

func TestAnOfferCannotGiveTwoSourcesForOneNumber(t *testing.T) {
	s := testServer(t)
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: "core.0", Kind: string(pool.KindLocal), Capacity: 1,
	}, "k1"); ref != nil {
		t.Fatalf("register: %s", ref.Reason)
	}
	_, _, _, ref := s.resolveOffers([]offerJSON{
		{Action: "skill", Value: 10, Resource: "core.0", Cost: 3, MS: 1000},
	}, arbiter.Table{})
	if ref == nil {
		t.Fatal("a resource and a cost together are two sources for one number")
	}
	if ref.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", ref.Code)
	}
}

func TestAnOfferForAnUnregisteredResourceIsRefused(t *testing.T) {
	s := testServer(t)
	_, _, _, ref := s.resolveOffers([]offerJSON{
		{Action: "skill", Value: 10, Resource: "ghost.9", MS: 1000},
	}, arbiter.Table{})
	if ref == nil {
		t.Fatal("a named resource that does not exist is a typo, not a price of zero")
	}
}

// ★ A slot you cannot take is not a cheaper option.
//
// It is excluded from the ranking but still reported: a silently dropped offer
// cannot be audited, and "I bid and was not chosen" is exactly the sentence
// somebody needs to be able to check.
func TestAnOfferAtCapacityIsSkippedButReported(t *testing.T) {
	s := testServer(t)
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "register", ID: "core.0", Kind: string(pool.KindLocal), Capacity: 1,
	}, "k1"); ref != nil {
		t.Fatalf("register: %s", ref.Reason)
	}
	if _, ref := s.pools.append(poolRecord{
		Rec: "pool", Op: "lease", ID: "core.0", Holder: "someone-else", Expires: 1 << 62,
	}, "k2"); ref != nil {
		t.Fatalf("lease: %s", ref.Reason)
	}
	offers, meta, skipped, ref := s.resolveOffers([]offerJSON{
		{Action: "skill", Value: 10, Resource: "core.0", MS: 1000},
	}, arbiter.Table{})
	if ref != nil {
		t.Fatalf("resolve: %s", ref.Reason)
	}
	if len(offers) != 0 {
		t.Fatalf("an offer that cannot be taken must not be ranked, got %d", len(offers))
	}
	if len(meta) != 0 {
		t.Fatalf("and it has no note among the ranked, got %d", len(meta))
	}
	if len(skipped) != 1 {
		t.Fatalf("it must still be reported, got %d", len(skipped))
	}
}

// ★ spent must be a sum over the same records as obs.
//
// If they were computed from different sources, one of them would eventually be
// updated without the other — which is the failure this project has paid for
// often enough to stop counting. The invariant is cheap to state and the only
// way to keep it true is to derive both from the same walk.
func TestSpentIsASumOverTheSameRecordsAsTheObservations(t *testing.T) {
	s := testServer(t)
	costs := []float64{2, 0, 5, 1}
	for i, c := range costs {
		ok := i%2 == 0
		rec := executionRecord{
			Rec: "execution", Attempt: "a", Skill: "runner.ok", TaskClass: "task_exec",
			Cost: c,
		}
		if i > 0 {
			rec.Attempt = "a" + string(rune('0'+i))
		}
		if _, ref := s.decisions.append(executionRecord{
			Rec: "execution", Attempt: rec.Attempt, Skill: rec.Skill, TaskClass: rec.TaskClass,
		}, rec.Attempt+keyIntent); ref != nil {
			t.Fatalf("intent %d: %s", i, ref.Reason)
		}
		yes, no := true, false
		if ok {
			rec.OK = &yes
		} else {
			rec.OK = &no
		}
		rec.Verified = "verify_assert"
		if _, ref := s.decisions.append(rec, rec.Attempt+keyResolution); ref != nil {
			t.Fatalf("resolution %d: %s", i, ref.Reason)
		}
	}
	if len(s.decisions.obs) != len(costs) {
		t.Fatalf("want %d observations, got %d", len(costs), len(s.decisions.obs))
	}
	want := 0.0
	for _, c := range costs {
		want += c
	}
	if s.decisions.spent != want {
		t.Fatalf("spent must be the sum of the recorded costs: want %v, got %v", want, s.decisions.spent)
	}
	// And it counts money only for attempts that concluded: an open intent has
	// no cost on the chain, so it cannot contribute one here.
	if _, ref := s.decisions.append(executionRecord{
		Rec: "execution", Attempt: "still-open", Skill: "runner.ok", TaskClass: "task_exec",
	}, "still-open"+keyIntent); ref != nil {
		t.Fatalf("open intent: %s", ref.Reason)
	}
	if s.decisions.spent != want {
		t.Fatalf("an open intent must not add to spent: want %v, got %v", want, s.decisions.spent)
	}
}

// ★ ok must be a count over the same records as obs, and it must count only
// what the verifier passed.
//
// A success count that came from anywhere else — the caller's claim, the exit
// code, the executor's own opinion — would be a different number wearing the
// same name. The chain already says which attempts the verifier passed; this
// is a count of that, not a second judgement.
func TestTheSuccessCountIsOverTheSameRecordsAsTheObservations(t *testing.T) {
	s := testServer(t)
	verdicts := []bool{true, false, true, true, false}
	for i, ok := range verdicts {
		attempt := "ok" + string(rune('0'+i))
		if _, ref := s.decisions.append(executionRecord{
			Rec: "execution", Attempt: attempt, Skill: "runner.ok", TaskClass: "task_exec",
		}, attempt+keyIntent); ref != nil {
			t.Fatalf("intent %d: %s", i, ref.Reason)
		}
		yes, no := true, false
		rec := executionRecord{
			Rec: "execution", Attempt: attempt, Skill: "runner.ok", TaskClass: "task_exec",
			Verified: "verify_assert",
		}
		if ok {
			rec.OK = &yes
		} else {
			rec.OK = &no
		}
		if _, ref := s.decisions.append(rec, attempt+keyResolution); ref != nil {
			t.Fatalf("resolution %d: %s", i, ref.Reason)
		}
	}
	want := 0
	for _, ok := range verdicts {
		if ok {
			want++
		}
	}
	if len(s.decisions.obs) != len(verdicts) {
		t.Fatalf("want %d observations, got %d", len(verdicts), len(s.decisions.obs))
	}
	if s.decisions.ok != want {
		t.Fatalf("the success count must be over the same records: want %d, got %d", want, s.decisions.ok)
	}
	// An intent with no resolution is not a failure and not a success: it has
	// no verdict, so it must move neither number.
	before, beforeObs := s.decisions.ok, len(s.decisions.obs)
	if _, ref := s.decisions.append(executionRecord{
		Rec: "execution", Attempt: "never-concluded", Skill: "runner.ok", TaskClass: "task_exec",
	}, "never-concluded"+keyIntent); ref != nil {
		t.Fatalf("open intent: %s", ref.Reason)
	}
	if s.decisions.ok != before || len(s.decisions.obs) != beforeObs {
		t.Fatalf("an open intent moved a count: ok %d->%d, obs %d->%d",
			before, s.decisions.ok, beforeObs, len(s.decisions.obs))
	}
}
