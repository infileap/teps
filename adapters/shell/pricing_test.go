package main

import (
	"encoding/json"
	"testing"

	"teps/organs/arbiter"
	"teps/store"

	"teps/core"
	"teps/organs/pool"
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

// ★ The posterior mean and the raw success rate are two readings of the same
// records: one folds with forgetting, one does not.
//
// They are supposed to differ — forgetting is deliberate — but until now
// nothing reported the difference, so "this number has been decayed" lived only
// in a comment. A reader of the number could not tell.
func TestTheTwoReadingsComeFromTheSameRecords(t *testing.T) {
	s := testServer(t)
	verdicts := []bool{true, true, false, true, false, true}
	for i, ok := range verdicts {
		attempt := "two" + string(rune('0'+i))
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
	n := len(s.decisions.obs)
	if n != len(verdicts) {
		t.Fatalf("want %d observations, got %d", len(verdicts), n)
	}
	want := 0.0
	for _, ok := range verdicts {
		if ok {
			want++
		}
	}
	want /= float64(n)
	if got := rawRate(s.decisions.ok, n); got != want {
		t.Fatalf("rawRate must be the undecayed ratio: want %v, got %v", want, got)
	}
	// The decayed reading is a different number on the same records — and it is
	// lower here, because the two failures are the most recent and forgetting
	// weights recent evidence more.
	tbl := s.decisions.table()
	mean := meanOf(tbl)
	if mean == want {
		t.Fatalf("with a decay of %v the two readings should differ on these records, both were %v", arbiter.DefaultDecay, want)
	}
	if mean > want {
		t.Fatalf("the recent evidence here is two failures, so the decayed mean must be lower: mean %v, raw %v", mean, want)
	}
}

func TestRawRateWithNoObservationsIsZeroNotNaN(t *testing.T) {
	// 0/0 has no value. Reporting NaN there would put a non-number in a JSON
	// document, and reporting 1.0 would claim a perfect record for a system
	// that has never run.
	if got := rawRate(0, 0); got != 0 {
		t.Fatalf("an empty record has no rate: got %v", got)
	}
	if got := meanOf(arbiter.Table{}); got != 0 {
		t.Fatalf("an empty table has no mean: got %v", got)
	}
}

// ★★★ The decay gap has to be the decay, and only the decay.
//
// The old definition was `mean - raw_rate`, where `mean` is an **unweighted
// average over posterior rows** and `raw_rate` is a **success rate weighted by
// observations**. That difference therefore also carries the weighting and the
// Beta(1,1) prior, while the field name claimed it was the decay.
//
// The record set below is chosen to pull those two apart: one skill observed
// once and succeeding, another observed nine times and failing. The row average
// gives the single observation half the weight; the observation-weighted rate
// gives it a tenth. So the old number is large and the new one is small — and
// the old one is **positive**, while decay can only pull a row toward the prior.
func TestTheDecayGapIsTheDecayAndNotTheWeighting(t *testing.T) {
	obs := []arbiter.Observation{
		{Key: arbiter.Key{Skill: "dg.hit", TaskClass: "task_exec"}, OK: true},
	}
	for i := 0; i < 9; i++ {
		obs = append(obs, arbiter.Observation{
			Key: arbiter.Key{Skill: "dg.miss", TaskClass: "task_exec"}, OK: false,
		})
	}
	decayed := arbiter.Fold(obs, arbiter.DefaultDecay)
	undecayed := arbiter.Fold(obs, 1)

	mean, mu := meanOf(decayed), meanOf(undecayed)
	successes := 0
	for _, o := range obs {
		if o.OK {
			successes++
		}
	}
	raw := rawRate(successes, len(obs))

	// The identity the field now claims: the gap is the two folds' difference.
	if got, want := mean-mu, meanOf(decayed)-meanOf(undecayed); got != want {
		t.Fatalf("decay_gap must be mean - mean_undecayed: got %v want %v", got, want)
	}
	// And it must differ from the old definition by a lot on this record set.
	oldStyle := mean - raw
	newStyle := mean - mu
	if oldStyle-newStyle < 0.2 {
		t.Fatalf("this record set does not pull the two definitions apart: "+
			"old=%v new=%v (mean=%v mean_undecayed=%v raw=%v)",
			oldStyle, newStyle, mean, mu, raw)
	}
	if oldStyle <= 0 {
		t.Fatalf("the old definition should be positive here (the weighting "+
			"lifts it), got %v", oldStyle)
	}
	// ★ And the new one must be small: the decay is genuinely tiny on 10 records.
	if newStyle > 0.05 || newStyle < -0.05 {
		t.Fatalf("the decay's own effect on 10 records should be tiny, got %v", newStyle)
	}
}

// ★★★ The handler's own arithmetic, not a re-implementation of it.
//
// The first version of the test above called `arbiter.Fold` and `meanOf`
// directly — so it proved that *those* are right, and said nothing about what
// `metrics.get` reports. Four mutations of the handler (the gap going back to
// `mean - raw_rate`, `mean_undecayed` reporting the decayed table, and
// `undecayedTable` folding with decay) all survived it. A test that rebuilds
// the thing it is checking checks the rebuild.
func TestTheMetricsHandlerReportsTheDecayGap(t *testing.T) {
	s := testServer(t)
	tsk, ref := newTaskStore(s.log)
	if ref != nil {
		t.Fatalf("task store: %s", ref.Reason)
	}
	s.tasks = tsk
	// metrics.get 也会读技能店的目录（accuracy 那一块），所以它不能是 nil。
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	s.skills = st

	// One skill observed once and succeeding; another observed nine times and
	// failing. The row average gives the single observation half the weight.
	obs := []arbiter.Observation{{Key: arbiter.Key{Skill: "dg.hit", TaskClass: "task_exec"}, OK: true}}
	for i := 0; i < 9; i++ {
		obs = append(obs, arbiter.Observation{Key: arbiter.Key{Skill: "dg.miss", TaskClass: "task_exec"}})
	}
	s.decisions.obs = obs
	s.decisions.ok = 1

	out := core.Frame{}
	s.handleMetrics(core.Frame{T: "metrics.get"}, &out)
	if out.Err != nil {
		t.Fatalf("metrics.get refused: %s", out.Err.Reason)
	}
	var body struct {
		Reliability map[string]any `json:"reliability"`
	}
	// ★ 这是在**读响应**，不是在解析请求：`decodeBody` 现在会拒绝不认识的
	// 字段（那是给请求用的严格），而这里只要响应里的几个字段。
	if err := json.Unmarshal(out.Body, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	r := body.Reliability
	num := func(k string) float64 {
		v, ok := r[k].(float64)
		if !ok {
			t.Fatalf("reliability.%s is missing or not a number: %#v", k, r[k])
		}
		return v
	}
	mean, mu, raw, gap := num("mean"), num("mean_undecayed"), num("raw_rate"), num("decay_gap")

	sep := mean - mu
	if sep < 0 {
		sep = -sep
	}
	if sep < 1e-4 {
		t.Fatalf("this record set does not separate the two folds: mean=%v mean_undecayed=%v", mean, mu)
	}
	if d := gap - (mean - mu); d > 1e-4 || d < -1e-4 {
		t.Fatalf("decay_gap must be mean - mean_undecayed: gap=%v mean=%v mean_undecayed=%v", gap, mean, mu)
	}
	old := (mean - raw) - gap
	if old < 0 {
		old = -old
	}
	if old < 0.2 {
		t.Fatalf("the old definition and the new one coincide here (old=%v new=%v): "+
			"this test cannot tell them apart", mean-raw, gap)
	}
}

// And the same three quantities on `arbiter.state`'s per-skill branch.
//
// That branch reports `decay_gap` for one key rather than for the table, and it
// had its own copy of the old formula — so a mutation reverting only that one
// survived the handler test above. Two call sites, two tests; a test on one of
// them says nothing about the other.
func TestArbiterStateReportsTheDecayGapForOneKey(t *testing.T) {
	s := testServer(t)
	obs := []arbiter.Observation{{Key: arbiter.Key{Skill: "dg.hit", TaskClass: "task_exec"}, OK: true}}
	for i := 0; i < 9; i++ {
		obs = append(obs, arbiter.Observation{Key: arbiter.Key{Skill: "dg.miss", TaskClass: "task_exec"}})
	}
	s.decisions.obs = obs
	s.decisions.ok = 1

	out := core.Frame{}
	s.handleDecision(core.Frame{
		T:    "arbiter.state",
		Body: []byte(`{"skill":"dg.hit","task_class":"task_exec"}`),
	}, &out)
	if out.Err != nil {
		t.Fatalf("arbiter.state refused: %s", out.Err.Reason)
	}
	var body struct {
		Mean          float64 `json:"mean"`
		MeanUndecayed float64 `json:"mean_undecayed"`
		RawRate       float64 `json:"raw_rate"`
		DecayGap      float64 `json:"decay_gap"`
	}
	// ★ 这是在**读响应**，不是在解析请求：`decodeBody` 现在会拒绝不认识的
	// 字段（那是给请求用的严格），而这里只要响应里的几个字段。
	if err := json.Unmarshal(out.Body, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d := body.DecayGap - (body.Mean - body.MeanUndecayed); d > 1e-4 || d < -1e-4 {
		t.Fatalf("decay_gap must be mean - mean_undecayed: gap=%v mean=%v mean_undecayed=%v",
			body.DecayGap, body.Mean, body.MeanUndecayed)
	}
	if d := body.MeanUndecayed - body.Mean; d > -1e-6 && d < 1e-6 {
		t.Fatalf("the two folds coincide here, so the check above proves nothing")
	}
	if d := (body.Mean - body.RawRate) - body.DecayGap; d < 0.2 && d > -0.2 {
		t.Fatalf("the old definition and the new one coincide here (old=%v new=%v)",
			body.Mean-body.RawRate, body.DecayGap)
	}
}

// ★★★ The product names the factor that caps it, and names it the same way twice.
//
// The comment above `reliability.weakest` used to say the weakest link is what
// matters for a product — while `capability.value` multiplied the **mean**. The
// number that comment was reaching for is the weakest of the three factors, and
// that is what this reports.
//
// The tie is the part worth testing: three equal factors must produce the same
// name every time. A version iterating a map would pass a "picks the minimum"
// test and still report a different name on each call.
func TestTheProductNamesItsWeakestFactor(t *testing.T) {
	// A clear winner.
	v, name := weakestFactor([]capFactor{
		{"coverage.intercept_rate", 0.9},
		{"accuracy.value", 0.2},
		{"reliability.mean", 0.8},
	})
	if v != 0.2 || name != "accuracy.value" {
		t.Fatalf("want 0.2 / accuracy.value, got %v / %s", v, name)
	}

	// A tie: the answer must not depend on iteration order.
	first := ""
	for i := 0; i < 200; i++ {
		v, name = weakestFactor([]capFactor{
			{"coverage.intercept_rate", 0.5},
			{"accuracy.value", 0.5},
			{"reliability.mean", 0.5},
		})
		if v != 0.5 {
			t.Fatalf("tie must still report the value, got %v", v)
		}
		if i == 0 {
			first = name
			continue
		}
		if name != first {
			t.Fatalf("a tie reported two different names across calls: %q then %q "+
				"— a reading that changes between two identical inputs is not a reading",
				first, name)
		}
	}
	// And by name order, the tie goes to the alphabetically first.
	if first != "accuracy.value" {
		t.Fatalf("a tie should break by name, got %s", first)
	}
}
