package main

import (
	"testing"

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
	offers, meta, skipped, ref := s.resolveOffers(in)
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
	})
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
	})
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
	})
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
