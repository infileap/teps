package main

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"

	"teps/core"
	"teps/organs/arbiter"
)

// Asking the price and paying it are two operations.
//
// `quote` prices an escalation and consumes nothing: no budget, no chain record.
// `decide` acts on a price and consumes a grant.
//
// The reason they are separate is not tidiness. If looking at the price cost
// something, a system choosing between four options would pay four times to
// think, and the cheapest strategy would be to stop comparing and take whatever
// it already knew. **A quoter that charges produces a system that escalates by
// habit** — which is the failure the whole arbiter exists to prevent.
//
// # A quote is only good while the price holds
//
// `decide` carries the `quote_id` it was based on, and the server recomputes the
// price from current state before honouring it. A quote whose posterior has moved
// is refused rather than honoured at the old number.
//
// Without that, the two operations would combine into "price it once, act on that
// price forever", and a decision would rest on evidence that no longer exists.
//
// # Why decide re-sends the offers
//
// The alternative is for the server to remember the last quote, and that memory
// would be a second source of truth: it is not on the chain, so a restart loses
// it, and a decision would then depend on how recently someone asked. Re-sending
// the offers costs a few hundred bytes and makes the quote id the *whole* claim —
// it is checked against the table as it is now, and nothing is remembered.

type quoteBody struct {
	TaskClass string      `json:"task_class"`
	Margin    float64     `json:"margin"`
	Offers    []offerJSON `json:"offers"`
}

type decideBody struct {
	QuoteID   string      `json:"quote_id"`
	Action    string      `json:"action"`
	Attempt   string      `json:"attempt"`
	Skill     string      `json:"skill"`
	TaskClass string      `json:"task_class"`
	Margin    float64     `json:"margin"`
	Offers    []offerJSON `json:"offers"`
}

type escalationRecord struct {
	Rec       string  `json:"rec"`
	Attempt   string  `json:"attempt"`
	QuoteID   string  `json:"quote_id"`
	Action    string  `json:"action"`
	Skill     string  `json:"skill"`
	TaskClass string  `json:"task_class"`
	Utility   float64 `json:"utility"`
	P         float64 `json:"p"`
	GrantMS   int64   `json:"grant_ms"`
	GrantCost int64   `json:"grant_cost"`
}

// handleEscalation dispatches escalation.* frames.
func (s *server) handleEscalation(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "escalation.") {
		return false
	}
	switch f.T {
	case "escalation.quote":
		s.escalationQuote(f, out)
	case "escalation.decide":
		s.escalationDecide(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func offersOf(in []offerJSON) []arbiter.Offer {
	out := make([]arbiter.Offer, 0, len(in))
	for _, o := range in {
		out = append(out, arbiter.Offer{
			Action: arbiter.Action(o.Action), Skill: o.Skill, TaskClass: o.TaskClass,
			Value: o.Value, Cost: o.Cost, Latency: o.Latency, LatencyWeight: o.LatencyWeight,
			Pinned: o.Pinned, UsePinned: o.UsePinned,
		})
	}
	return out
}

// priceOf is the single place a price is computed.
//
// `quote` and `decide` both call it. Two computations of the same price is how a
// system ends up quoting one number and acting on another — the same class of
// defect as two gates assigning different refusal codes to one fact.
func (s *server) priceOf(body quoteBody) ([]arbiter.Ranked, arbiter.Ranked, string, *core.Refusal) {
	offers := offersOf(body.Offers)
	if len(offers) == 0 {
		return nil, arbiter.Ranked{}, "", core.Refuse(core.CodeBadRequest,
			"a quote needs offers: there is nothing to price")
	}
	s.decisions.lock()
	tbl := s.decisions.table()
	s.decisions.unlock()

	ranked := arbiter.Rank(offers, tbl)
	var chosen arbiter.Ranked
	if body.Margin > 0 {
		esc, ref := arbiter.ShouldEscalate(offers, tbl, body.Margin)
		if ref != nil {
			return nil, arbiter.Ranked{}, "", ref
		}
		chosen = esc.Chosen
	} else {
		chosen = ranked[0]
	}
	return ranked, chosen, quoteID(ranked, body.Margin), nil
}

// quoteID identifies the price, not the request.
//
// It is derived from the ranked result, so it changes when any posterior moves,
// when an offer changes, or when the margin changes. Two requests that would
// decide the same way get the same id, which is what makes "act on this price"
// checkable at all.
func quoteID(ranked []arbiter.Ranked, margin float64) string {
	rows := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		rows = append(rows, map[string]any{
			"action": string(r.Offer.Action), "skill": r.Offer.Skill,
			"p": round(r.Prob), "u": round(r.Utility),
		})
	}
	canon, ref := core.Canonical(mustJSON(map[string]any{"ranked": rows, "margin": round(margin)}))
	if ref != nil {
		return ""
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

func (s *server) escalationQuote(f core.Frame, out *core.Frame) {
	var body quoteBody
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	ranked, chosen, id, ref := s.priceOf(body)
	if ref != nil {
		out.Err = ref
		return
	}
	rows := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		rows = append(rows, map[string]any{
			"action": string(r.Offer.Action), "skill": r.Offer.Skill,
			"p": round(r.Prob), "utility": round(r.Utility),
			"cost": r.Offer.Cost, "latency": r.Offer.Latency,
		})
	}
	// No chain write and no budget spend. This response is the whole side effect,
	// and `spent: 0` says so rather than leaving it to be inferred.
	out.Body = mustJSON(map[string]any{
		"quote_id": id, "ranked": rows,
		"chosen": string(chosen.Offer.Action), "chosen_skill": chosen.Offer.Skill,
		"utility": round(chosen.Utility), "p": round(chosen.Prob),
		"grant_ms": ceil64(chosen.Offer.Latency), "grant_cost": ceil64(chosen.Offer.Cost),
		"spent": 0,
	})
}

func (s *server) escalationDecide(f core.Frame, out *core.Frame) {
	var body decideBody
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	if body.QuoteID == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"a decision needs the quote_id it was based on: without it the decision rests on a price nobody can check")
		return
	}
	if body.Action == "" || body.Attempt == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"a decision needs an action and an attempt: the action is what was chosen, the attempt is what it belongs to")
		return
	}
	ranked, _, id, ref := s.priceOf(quoteBody{
		TaskClass: body.TaskClass, Margin: body.Margin, Offers: body.Offers,
	})
	if ref != nil {
		out.Err = ref
		return
	}
	if id != body.QuoteID {
		out.Err = core.Refuse(core.CodeBadRequest,
			"the quote is stale: the price is now "+id+" and the decision cites "+body.QuoteID+
				". Ask for a new quote rather than acting on the old number")
		return
	}
	var chosen *arbiter.Ranked
	for i := range ranked {
		if string(ranked[i].Offer.Action) == body.Action {
			chosen = &ranked[i]
			break
		}
	}
	if chosen == nil {
		out.Err = core.Refuse(core.CodeBadRequest,
			"action "+body.Action+" was not among the quoted offers")
		return
	}
	// ★★★ 记录必须**全部来自被定价的那一条 offer**，不能一半来自请求。
	//
	// 此前 `rec.Skill` 与 `rec.TaskClass` 取的是 `body.Skill` / `body.TaskClass`，
	// 而 `utility` / `p` / `grant_cost` / `grant_ms` 取的是 `chosen`。实测
	// （两条都是 `skill` 动作的 offer：`read.file` cost 1 / latency 5，
	// `write.file` cost 50 / latency 900）：
	//
	//   quote 选的： skill / read.file   grant_cost=1  grant_ms=5
	//   decide 传 skill=write.file，quote_id 照旧匹配 ⇒ 链上写下
	//   {"action":"skill","grant_cost":1,"grant_ms":5,"p":1,"rec":"escalation",
	//    "skill":"write.file","utility":8.5,...}
	//
	// **那条记录说「关于 write.file 做了一次决定，它花 1、效用 8.5」——
	// 而这三件事没有一件是关于 write.file 的。** write.file 花 50、延迟 900、
	// 效用是负的。`priceOf` 的注释写着「Two computations of the same price is how
	// a system ends up quoting one number and acting on another」；这里比那更糟：
	// 只算了一次价，而**记录把它挂在了另一个主体上**。
	//
	// `task_class` 是同一件事的第二个入口：它是 `decideBody` 的字段，`priceOf`
	// 根本不读它（后验是按 `offer.TaskClass` 查的），于是把 `task_exec` 换成
	// `chat` 之后 quote_id **照旧匹配**，而链上记下了 `"task_class":"chat"` ——
	// 一个后验从未在那个类下被读过的类。
	//
	// 判据取「调用方说了就必须一致，没说就用被定价的那一条」：一个不点名的
	// 决定没有错，一个点名点错的必须被拒，而不是被静默改写成对的那个。
	if body.Skill != "" && body.Skill != chosen.Offer.Skill {
		out.Err = core.Refuse(core.CodeBadRequest,
			"the decision names skill "+body.Skill+" but the price it cites is for "+
				chosen.Offer.Skill+": a record must name the thing that was priced")
		return
	}
	if body.TaskClass != "" && body.TaskClass != chosen.Offer.TaskClass {
		out.Err = core.Refuse(core.CodeBadRequest,
			"the decision names task class "+body.TaskClass+" but the price it cites was read "+
				"under "+chosen.Offer.TaskClass+": recording another one would name a fact that "+
				"never happened")
		return
	}
	grant := core.Budget{MS: ceil64(chosen.Offer.Latency), Money: ceil64(chosen.Offer.Cost)}
	// The grant is charged before the record is written. A record of a decision
	// that was never funded would be a decision the chain claims and the ledger
	// denies; the other order fails as a budget that moved with no decision,
	// which is visible and cannot be mistaken for a decision that happened.
	if ref := s.acct.Spend(grant); ref != nil {
		out.Err = ref
		return
	}
	rec := escalationRecord{
		Rec: "escalation", Attempt: body.Attempt, QuoteID: body.QuoteID,
		// ★ 这四行全部来自 `chosen`。此前 `Skill` 与 `TaskClass` 来自请求，
		// 于是同一个记录里一半描述被定价的那一条、一半描述调用方说出口的
		// 那一条 —— 而它们可以是两条不同的 offer。
		Action:    string(chosen.Offer.Action),
		Skill:     chosen.Offer.Skill,
		TaskClass: chosen.Offer.TaskClass,
		Utility:   round(chosen.Utility), P: round(chosen.Prob),
		GrantMS: grant.MS, GrantCost: grant.Money,
	}
	canon, ref := core.Canonical(mustJSON(rec))
	if ref != nil {
		out.Err = ref
		return
	}
	entry, ref := s.log.Append(f.Key, canon)
	if ref != nil {
		out.Err = ref
		return
	}
	left := s.acct.Left()
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{
		"head": entry.Head, "action": body.Action, "quote_id": body.QuoteID,
		"utility": round(chosen.Utility), "grant_ms": grant.MS, "grant_cost": grant.Money,
		"left_ms": left.MS, "left_money": left.Money,
	})
}

// ceil64 converts a quoted price into a grant the ledger can hold.
//
// Budget's three dimensions are integers and Offer.Cost is a float, so the
// conversion has to pick a direction. It rounds **up**, and the reason is not
// caution: rounding down would let a caller spend for free by pricing everything
// at 0.001. The consequence is a floor of one unit per charge, which is why the
// response reports `grant_cost` explicitly — a charge the caller did not see
// coming is the only thing worse than a coarse one.
//
// The ranking still uses the true float cost, so the arbiter keeps preferring
// the cheaper action; only the ledger is coarse.
func ceil64(f float64) int64 {
	if f <= 0 {
		return 0
	}
	return int64(math.Ceil(f))
}
