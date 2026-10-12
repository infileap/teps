package main

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"teps/core"
	"teps/evidence"
	"teps/organs/arbiter"
	"teps/organs/pool"
)

// The arbiter's table is a projection of the chain, like sessions and memory.
//
// It is tempting to keep the posterior in memory and update it in place: it is
// one map, and folding the whole log on every query looks wasteful. But a
// posterior kept in memory is a second source of truth about how often something
// worked, and the first time the process restarts the two disagree. The log
// already records every execution; the table is what you get by reading it.
//
// The practical consequence is that "the system noticed by itself" is auditable:
// replay the chain and you get the decision the live system made.

// round keeps a reported number readable without turning it into a string.
//
// A number rendered as text stops being a number: a client cannot compare it,
// sort it or sum it without parsing it back, and every client would parse it
// slightly differently.
func round(f float64) float64 { return math.Round(f*10000) / 10000 }

// executionRecord is one attempt, as it lands on the chain.
//
// Intent and resolution are two records, not one. An intent written before the
// attempt and a resolution written after are joined by Attempt, and a replay
// that finds an intent with no resolution learns something a single combined
// record cannot express: that the attempt happened and was never concluded.
// Recording that as a failure would be a guess; recording it as a success would
// be a lie.
type executionRecord struct {
	Rec       string  `json:"rec"`
	Attempt   string  `json:"attempt"`
	Skill     string  `json:"skill,omitempty"`
	TaskClass string  `json:"task_class,omitempty"`
	Strategy  string  `json:"strategy,omitempty"`
	OK        *bool   `json:"ok,omitempty"`
	Verified  string  `json:"verified_by,omitempty"`
	Latency   float64 `json:"latency,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
	// Rules 是这次运行被判定所依据的那组断言的身份。
	Rules string `json:"rules,omitempty"`
}

type decisionStore struct {
	mu chan struct{}
	lg *core.Log

	// obs is every concluded attempt, in chain order. It is what the table folds.
	obs []arbiter.Observation
	// open holds attempts that were started and not concluded.
	open map[string]bool
	// spent is the money the chain says these attempts cost, and ok is how many
	// of them the verifier passed. Both are counted over the same records as
	// obs, in the same walk, so neither can drift from it.
	spent   float64
	ok      int
	applied uint64
}

func newDecisionStore(lg *core.Log) (*decisionStore, *core.Refusal) {
	st := &decisionStore{mu: make(chan struct{}, 1), lg: lg, open: map[string]bool{}}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *decisionStore) lock()   { <-st.mu }
func (st *decisionStore) unlock() { st.mu <- struct{}{} }

func (st *decisionStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec executionRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "execution" {
		return nil
	}
	if rec.OK == nil {
		st.open[rec.Attempt] = true
		return nil
	}
	delete(st.open, rec.Attempt)
	st.spent += rec.Cost
	if *rec.OK {
		st.ok++
	}
	st.obs = append(st.obs, arbiter.Observation{
		Key:   arbiter.Key{Skill: rec.Skill, TaskClass: rec.TaskClass},
		OK:    *rec.OK,
		Rules: rec.Rules,
	})
	return nil
}

func (st *decisionStore) table() arbiter.Table {
	return arbiter.Fold(st.obs, arbiter.DefaultDecay)
}

func (st *decisionStore) append(rec executionRecord, key string) (core.Entry, *core.Refusal) {
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

// handleDecision dispatches arbiter.* frames.
func (s *server) handleDecision(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "arbiter.") {
		return false
	}
	switch f.T {
	case "arbiter.observe":
		s.arbiterObserve(f, out)
	case "arbiter.rank":
		s.arbiterRank(f, out)
	case "arbiter.state":
		s.arbiterState(f, out)
	case "arbiter.fix":
		s.arbiterFix(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

// arbiterObserve records one attempt, and optionally its outcome.
func (s *server) arbiterObserve(f core.Frame, out *core.Frame) {
	var body struct {
		Attempt   string  `json:"attempt"`
		Skill     string  `json:"skill"`
		TaskClass string  `json:"task_class"`
		Strategy  string  `json:"strategy"`
		Outcome   string  `json:"outcome"`
		Verified  string  `json:"verified_by"`
		Latency   float64 `json:"latency"`
		Cost      float64 `json:"cost"`
		Rules     string  `json:"rules"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	if body.Attempt == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"an observation needs an attempt id: without it an intent can never be resolved")
		return
	}
	rec := executionRecord{
		Rec: "execution", Attempt: body.Attempt, Skill: body.Skill,
		TaskClass: body.TaskClass, Strategy: body.Strategy,
		Latency: body.Latency, Cost: body.Cost, Rules: body.Rules,
	}
	s.decisions.lock()
	_, known := s.decisions.open[body.Attempt]
	switch body.Outcome {
	case "":
		// An intent. It stays open until a resolution lands, and if none ever
		// does, the replay shows that rather than guessing an outcome.
		if known {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest, "attempt already open: "+body.Attempt)
			return
		}
		if body.Skill == "" || body.TaskClass == "" {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest, "an intent needs a skill and a task class")
			return
		}
	case "ok":
		yes := true
		rec.OK = &yes
		rec.Verified = body.Verified
	case "failed", "timeout", "refused", "degraded":
		no := false
		rec.OK = &no
		rec.Verified = body.Verified
	case "unknown":
		// "unknown" is what a reader infers from a missing resolution; it is not
		// a value anyone writes. Writing it would make it terminal — "this
		// attempt is concluded, and the conclusion is that we do not know" —
		// when the honest record is an intent that stays open and can still be
		// resolved by a human, by a second verifier, or by a re-run.
		//
		// A written unknown closes a door. An open intent leaves one open.
		s.decisions.unlock()
		out.Err = core.Refuse(core.CodeBadRequest,
			"unknown is a derived reading, not a written outcome: leave the intent open instead")
		return
	default:
		s.decisions.unlock()
		out.Err = core.Refuse(core.CodeBadRequest, "unknown outcome: "+body.Outcome)
		return
	}
	if rec.OK != nil {
		// The executor does not get to certify itself. A posterior built from
		// unverified outcomes measures optimism, not reliability.
		if rec.Verified == "" {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest,
				"an outcome needs verified_by: an unverified ok is the executor's own opinion")
			return
		}
		// A resolution whose intent was never written is refused. Accepting it
		// would make the chain's account of what happened depend on records that
		// are not in the chain.
		if !known {
			s.decisions.unlock()
			out.Err = core.Refuse(core.CodeBadRequest, "no open attempt to resolve: "+body.Attempt)
			return
		}
	}
	entry, ref := s.decisions.append(rec, f.Key)
	open := len(s.decisions.open)
	s.decisions.unlock()
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{"head": entry.Head, "open": open})
}

func (s *server) arbiterRank(f core.Frame, out *core.Frame) {
	var body struct {
		Margin float64     `json:"margin"`
		Offers []offerJSON `json:"offers"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	s.decisions.lock()
	tbl := s.decisions.table()
	s.decisions.unlock()
	offers, meta, skipped, ref := s.resolveOffers(body.Offers, tbl)
	if ref != nil {
		out.Err = ref
		return
	}
	if body.Margin > 0 {
		esc, ref := arbiter.ShouldEscalate(offers, tbl, body.Margin)
		if ref != nil {
			out.Err = ref
			return
		}
		out.Body = mustJSON(map[string]any{
			"chosen": esc.Chosen.Offer.Action, "utility": round(esc.Chosen.Utility),
			"reason": esc.Reason, "threshold": body.Margin,
			"cost": metaAt(meta, esc.Chosen.Offer.Tag), "skipped": skipped,
		})
		return
	}
	ranked := arbiter.Rank(offers, tbl)
	rows := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		row := map[string]any{
			"action": r.Offer.Action, "skill": r.Offer.Skill,
			"utility": round(r.Utility), "p": round(r.Prob),
		}
		if m := metaAt(meta, r.Offer.Tag); m != nil {
			row["cost"] = m
		}
		rows = append(rows, row)
	}
	out.Body = mustJSON(map[string]any{"ranked": rows, "skipped": skipped})
}

// meanOf is the average posterior mean over a table: the decayed reading.
func meanOf(tbl arbiter.Table) float64 {
	if len(tbl) == 0 {
		return 0
	}
	sum := 0.0
	for _, p := range tbl {
		sum += p.Mean()
	}
	return sum / float64(len(tbl))
}

// rawRate is the undecayed success rate: successes over observations.
//
// It is the same records the posterior folds, read without forgetting. The two
// are supposed to differ — forgetting is deliberate — but reporting only the
// decayed one hides how much work the forgetting is doing.
func rawRate(ok, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(ok) / float64(n)
}

// metaAt 把 Tag 换回那条报价的账。Tag 是索引的十进制写法，由 resolveOffers 打上。
func metaAt(meta []map[string]any, tag string) map[string]any {
	i, err := strconv.Atoi(tag)
	if err != nil || i < 0 || i >= len(meta) {
		return nil
	}
	return meta[i]
}

// resolveOffers 决定每个 offer 的钱从哪里来。
//
// 一个数不能有两个来源。调用方填了 cost 又说这个 offer 跑在某个资源上，
// 就是给了两个来源 —— 而它们不一致的时候，谁也不会发现。
//
// 占不到的资源不是更便宜的选择。它被**排除出排序**，但**照样被报出来**：
// 静默丢弃的剪枝无法被审查，而「我明明报价了却没被选」是最需要能查的一句话。
func (s *server) resolveOffers(in []offerJSON, tbl arbiter.Table) ([]arbiter.Offer, []map[string]any, []map[string]any, *core.Refusal) {
	offers := make([]arbiter.Offer, 0, len(in))
	// meta 与 offers 一一对齐，靠 offer 自己的 Tag 关联。
	// 用 action 当键是不行的：两个 offer 可以都是 "skill" 动作，
	// 于是后一个会把前一个的账盖掉，而响应看起来完全正常。
	meta := make([]map[string]any, 0, len(in))
	skipped := []map[string]any{}

	var leases []pool.Lease
	if s.pools != nil {
		s.pools.lock()
		leases = append([]pool.Lease(nil), s.pools.leases...)
		s.pools.unlock()
	}
	now := time.Now().UnixMilli()

	for _, o := range in {
		cost := o.Cost
		m := map[string]any{"cost": round(cost), "cost_from": "caller"}

		if o.Resource != "" {
			if o.Cost != 0 {
				return nil, nil, nil, core.Refuse(core.CodeBadRequest,
					"offer "+o.Action+" gives both a resource and a cost: one number, one source — the pool prices a resource, the caller prices a guess")
			}
			if s.pools == nil {
				return nil, nil, nil, core.Refuse(core.CodeBadRequest, "no compute pool is attached")
			}
			s.pools.lock()
			r, ok := s.pools.find(o.Resource)
			s.pools.unlock()
			if !ok {
				return nil, nil, nil, core.Refuse(core.CodeBadRequest,
					"offer "+o.Action+" names a resource that is not registered: "+o.Resource)
			}
			cost = float64(pool.Cost(r, core.Budget{MS: o.MS, Tokens: o.Tokens}).Money)
			m = map[string]any{
				"cost": round(cost), "cost_from": "pool",
				"resource": r.ID, "kind": string(r.Kind),
			}
			if len(pool.Live(leases, r.ID, now)) >= r.Capacity {
				m["available"] = false
				m["reason"] = "at capacity: a slot you cannot take is not a cheaper option"
				skipped = append(skipped, map[string]any{"action": o.Action, "cost": m})
				continue
			}
			m["available"] = true
		}

		expect := arbiter.Expectation(o.Expectation)
		switch expect {
		case "", arbiter.ExpectAttempt:
			expect = arbiter.ExpectAttempt
		case arbiter.ExpectSuccess:
		default:
			return nil, nil, nil, core.Refuse(core.CodeBadRequest,
				"offer "+o.Action+" asks to be priced per "+string(expect)+": the two questions are \"attempt\" and \"success\"")
		}
		offer := arbiter.Offer{
			Action: arbiter.Action(o.Action), Skill: o.Skill, TaskClass: o.TaskClass,
			Value: o.Value, Cost: cost, Latency: o.Latency, LatencyWeight: o.LatencyWeight,
			Pinned: o.Pinned, UsePinned: o.UsePinned, Expect: expect,
			Tag: strconv.Itoa(len(offers)),
		}
		m["expectation"] = string(expect)
		m["cost_per_attempt"] = round(cost)
		// cost_per_success 永远是「成功一次多少钱」，不管这次用的是哪个口径。
		// 一个叫 cost_per_success 的字段里放一次尝试的价格，就是同一个名字下的另一个数。
		prob := arbiter.ProbOf(offer, tbl.Get(arbiter.Key{Skill: o.Skill, TaskClass: o.TaskClass}))
		if prob <= 0 {
			// 一次也不成功的动作没有价格，因为它什么都买不到。
			// 报一个很大的数会是另一种形状的假话。
			m["cost_per_success"] = nil
			m["reason"] = "never succeeds, so it has no cost per success: an action that buys nothing has no price"
			skipped = append(skipped, map[string]any{"action": o.Action, "cost": m})
			continue
		}
		m["cost_per_success"] = round(cost / prob)
		// cost 是**真正进了效用函数**的那个数，不是原始报价。
		// 报一个没被用上的数，等于让人以为他看懂了那次决定。
		m["cost"] = m["cost_per_success"]
		if expect == arbiter.ExpectAttempt {
			m["cost"] = round(cost)
		}
		offers = append(offers, offer)
		meta = append(meta, m)
	}
	return offers, meta, skipped, nil
}

func (s *server) arbiterState(f core.Frame, out *core.Frame) {
	var body struct {
		Skill     string `json:"skill"`
		TaskClass string `json:"task_class"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	s.decisions.lock()
	tbl := s.decisions.table()
	// rules 与 tbl 出自**同一个切片**，所以它们不可能描述不同的观测集合。
	rules := arbiter.RulesOf(s.decisions.obs)
	open := len(s.decisions.open)
	n := len(s.decisions.obs)
	spent := s.decisions.spent
	s.decisions.unlock()
	if body.Skill != "" && body.TaskClass != "" {
		k := arbiter.Key{Skill: body.Skill, TaskClass: body.TaskClass}
		p := tbl.Get(k)
		out.Body = mustJSON(map[string]any{
			"skill": body.Skill, "task_class": body.TaskClass,
			"alpha": round(p.Alpha), "beta": round(p.Beta),
			"mean": round(p.Mean()), "upper": round(p.UpperBound()),
			"rules": rules[k], "mixed": len(rules[k]) > 1,
			"observations": n, "open": open, "spent": round(spent),
			// ★ 后验的 mean 与原始成功率是**同一批记录的两个读数**。
			//
			// mean 是带遗忘折出来的（DefaultDecay = 0.98），raw 是不折的。
			// 两者不同是设计使然——遗忘是故意的。但在此之前**没有任何地方
			// 报过这个差**，于是「这个数是折过的」这句话只活在注释里。
			"raw_rate":  round(rawRate(s.decisions.ok, n)),
			"decay_gap": round(p.Mean() - rawRate(s.decisions.ok, n)),
		})
		return
	}
	rows := make([]map[string]any, 0, len(tbl))
	for k, p := range tbl {
		rows = append(rows, map[string]any{
			"skill": k.Skill, "task_class": k.TaskClass,
			"mean": round(p.Mean()), "upper": round(p.UpperBound()),
			"rules": rules[k], "mixed": len(rules[k]) > 1,
		})
	}
	out.Body = mustJSON(map[string]any{
		"posteriors": rows, "observations": n, "open": open, "spent": round(spent),
		"successes": s.decisions.ok,
		"raw_rate":  round(rawRate(s.decisions.ok, n)),
		// mean 与 raw_rate 是同一批记录的两个读数：一个折过，一个没折。
		// 两个都报，decay_gap 才是可算的 —— 只说「这是折过的」等于没说。
		"mean":         round(meanOf(tbl)),
		"decay_gap":    round(meanOf(tbl) - rawRate(s.decisions.ok, n)),
		"mixed_skills": mixedSkills(rules),
	})
}

// mixedSkills counts the keys whose observations were judged under more than
// one rule set. It is the number a reader needs before believing any mean above
// it: a posterior folded across two judging rules is not one posterior.
func mixedSkills(rules map[arbiter.Key][]string) int {
	n := 0
	for _, ids := range rules {
		if len(ids) > 1 {
			n++
		}
	}
	return n
}

func (s *server) arbiterFix(f core.Frame, out *core.Frame) {
	var body struct {
		Calls         float64 `json:"calls"`
		OpexNow       float64 `json:"opex_now"`
		OpexAfter     float64 `json:"opex_after"`
		Capex         float64 `json:"capex"`
		ExpectedCalls float64 `json:"expected_calls"`
		Loss          float64 `json:"loss"`
		At            string  `json:"at"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	v, ok := arbiter.FixValue(arbiter.Fixing{
		Calls: body.Calls, OpexNow: body.OpexNow, OpexAfter: body.OpexAfter,
		Capex: body.Capex, ExpectedCalls: body.ExpectedCalls, Loss: body.Loss,
		At: evidence.Level(body.At),
	})
	if !ok {
		out.Err = core.Refuse(core.CodeBadRequest, "unknown evidence level: "+body.At)
		return
	}
	out.Body = mustJSON(map[string]any{"value": round(v), "fix": v > 0})
}

type offerJSON struct {
	Action        string  `json:"action"`
	Skill         string  `json:"skill"`
	TaskClass     string  `json:"task_class"`
	Value         float64 `json:"value"`
	Cost          float64 `json:"cost"`
	Latency       float64 `json:"latency"`
	LatencyWeight float64 `json:"latency_weight"`
	Pinned        float64 `json:"pinned"`
	UsePinned     bool    `json:"use_pinned"`
	// Resource 让这次报价的钱从池里取，而不是由调用方填。
	// 与 Cost 同时给是矛盾的：一个数不能有两个来源。
	Resource string `json:"resource"`
	// MS 与 Tokens 是这份工作的大小，池用它算钱。
	MS     int64 `json:"ms"`
	Tokens int64 `json:"tokens"`
	// Expectation 说这张报价回答的是哪个问题：
	// "attempt"（默认）＝一次尝试多少钱；"success"＝成功一次多少钱。
	// 两者排序不同，而哪个都不是错的——不问清就答，答案没法读。
	Expectation string `json:"expectation"`
}
