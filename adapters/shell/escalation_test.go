package main

import (
	"encoding/json"
	"strings"
	"testing"

	"teps/core"
)

// ★★★ 记录必须全部来自被定价的那一条 offer。
//
// 此前 escalationDecide 写进链里的 `skill` 与 `task_class` 取的是**请求**，
// 而 `utility` / `p` / `grant_cost` / `grant_ms` 取的是 arbiter 选中的那一条。
// 实测（两条都是 skill 动作的 offer：read.file cost 1 / latency 5，
// write.file cost 50 / latency 900）链上写下了
//
//	{"action":"skill","grant_cost":1,"grant_ms":5,"p":1,"rec":"escalation",
//	 "skill":"write.file","utility":8.5}
//
// ——「关于 write.file 做了一次决定，它花 1、效用 8.5」，而这三件事没有一件
// 是关于 write.file 的。**只算了一次价，而记录把它挂在了另一个主体上。**
func escalationServer(t *testing.T) *server {
	t.Helper()
	s := testServer(t)
	s.acct = core.NewAccount(core.Budget{MS: 60000, Tokens: 100000, Money: 1000})
	return s
}

func twoSkillOffers() []offerJSON {
	return []offerJSON{
		{Action: "skill", Skill: "read.file", TaskClass: "task_exec",
			Value: 10, Cost: 1, Latency: 5, LatencyWeight: 0.1},
		{Action: "skill", Skill: "write.file", TaskClass: "task_exec",
			Value: 8, Cost: 50, Latency: 900, LatencyWeight: 0.1},
	}
}

func quoteFor(t *testing.T, s *server, offers []offerJSON) string {
	t.Helper()
	_, _, id, ref := s.priceOf(quoteBody{TaskClass: "task_exec", Offers: offers})
	if ref != nil {
		t.Fatalf("quote: %s", ref.Reason)
	}
	if id == "" {
		t.Fatal("no quote id")
	}
	return id
}

func decideFrame(t *testing.T, s *server, id, skill, taskClass string, offers []offerJSON) (*core.Frame, string) {
	t.Helper()
	body, err := json.Marshal(decideBody{
		QuoteID: id, Action: "skill", Attempt: "att-1",
		Skill: skill, TaskClass: taskClass, Offers: offers,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out core.Frame
	s.escalationDecide(core.Frame{T: "escalation.decide", V: 1, Key: "k1", Body: body}, &out)
	return &out, string(out.Body)
}

// 链上最后一条 escalation 记录。
func lastEscalation(t *testing.T, s *server) map[string]any {
	t.Helper()
	entries, ref := s.log.Tail(50)
	if ref != nil {
		t.Fatalf("tail: %s", ref.Reason)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		var rec map[string]any
		if err := json.Unmarshal(entries[i].Body, &rec); err != nil {
			continue
		}
		if rec["rec"] == "escalation" {
			return rec
		}
	}
	return nil
}

func TestADecisionThatNamesTheWrongSkillIsRefused(t *testing.T) {
	s := escalationServer(t)
	offers := twoSkillOffers()
	id := quoteFor(t, s, offers)

	out, _ := decideFrame(t, s, id, "write.file", "task_exec", offers)
	if out.Err == nil {
		t.Fatal("naming write.file while the price is read.file must be refused")
	}
	if out.Err.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", out.Err.Code)
	}
	if !strings.Contains(out.Err.Reason, "write.file") || !strings.Contains(out.Err.Reason, "read.file") {
		t.Fatalf("the reason must name both sides, got %q", out.Err.Reason)
	}
	if rec := lastEscalation(t, s); rec != nil {
		t.Fatalf("a refused decision must leave no record, got %v", rec)
	}
}

func TestADecisionThatNamesTheWrongTaskClassIsRefused(t *testing.T) {
	s := escalationServer(t)
	offers := twoSkillOffers()
	id := quoteFor(t, s, offers)

	// ★ 这条尤其要紧：`priceOf` 根本不读 `quoteBody.TaskClass`（后验是按
	// `offer.TaskClass` 查的），所以把 task_exec 换成 chat 之后 quote_id
	// **照旧匹配** —— 唯一的防线就是这一条检查。
	out, _ := decideFrame(t, s, id, "read.file", "chat", offers)
	if out.Err == nil {
		t.Fatal("naming a task class the posterior was never read under must be refused")
	}
	if !strings.Contains(out.Err.Reason, "task_exec") || !strings.Contains(out.Err.Reason, "chat") {
		t.Fatalf("the reason must name both classes, got %q", out.Err.Reason)
	}
	if rec := lastEscalation(t, s); rec != nil {
		t.Fatalf("a refused decision must leave no record, got %v", rec)
	}
}

// 不点名是对的：那时记录用被定价的那一条。它必须真的用那一条，
// 而不是用请求里的空字符串。
func TestARecordWithNoNamesUsesThePricedOffer(t *testing.T) {
	s := escalationServer(t)
	offers := twoSkillOffers()
	id := quoteFor(t, s, offers)

	out, _ := decideFrame(t, s, id, "", "", offers)
	if out.Err != nil {
		t.Fatalf("naming nothing is not an error: %s", out.Err.Reason)
	}
	rec := lastEscalation(t, s)
	if rec == nil {
		t.Fatal("a granted decision must leave a record")
	}
	if rec["skill"] != "read.file" {
		t.Fatalf("the record must name the priced skill, got %v", rec["skill"])
	}
	if rec["task_class"] != "task_exec" {
		t.Fatalf("the record must name the class the posterior was read under, got %v", rec["task_class"])
	}
	// ★ 记录里的四件事必须描述**同一条 offer**：读的价、写的技能、
	// 算出的效用。read.file 是 10 − 1 − 0.5 = 8.5。
	if rec["grant_cost"] != float64(1) || rec["grant_ms"] != float64(5) {
		t.Fatalf("the record's price is not read.file's: %v / %v", rec["grant_cost"], rec["grant_ms"])
	}
	if rec["utility"] != 8.5 {
		t.Fatalf("utility = %v, want read.file's 8.5", rec["utility"])
	}
}

// 名字对得上时必须放行 —— 否则这条检查会变成「谁点名谁被拒」。
func TestADecisionThatNamesTheRightSkillIsGranted(t *testing.T) {
	s := escalationServer(t)
	offers := twoSkillOffers()
	id := quoteFor(t, s, offers)

	out, _ := decideFrame(t, s, id, "read.file", "task_exec", offers)
	if out.Err != nil {
		t.Fatalf("naming the priced offer must be allowed: %s", out.Err.Reason)
	}
	if rec := lastEscalation(t, s); rec == nil || rec["skill"] != "read.file" {
		t.Fatalf("the record must name read.file, got %v", rec)
	}
}
