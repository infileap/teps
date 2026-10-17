package main

import (
	"encoding/json"
	"testing"

	"teps/core"
	"teps/organs/arbiter"
)

// ★★★ 补一扇门不叫修好一个缺陷。
//
// 同一个 `arbiter.Rank` 有两扇门 —— `arbiter.rank` 与 `escalation.quote`。
// Round 62 只给后者补上了 `weight` / `posterior`，而前者**一行都没有**。实测：
//
//	一个从没被观测过的技能：
//	  {"action":"read.file","cost":{…},"p":1,"skill":"brand.new","utility":9}
//	一个 30 次全成的技能：
//	  {"action":"read.file","cost":{…},"p":1,"skill":"proven","utility":9}
//
// 两份回答**逐字相同**（除了 skill 名），而 `arbiter.state` 对同一个 key 说的是
// `weight=0` 与 `weight=21.8168`。**同一个数报给了证据量差三十倍的技能。**
//
// 这与 `audit.tail`→`task.stats`（Round 44 → 59）、`decide`→`quote`
// （Round 47 → 61）是同一条，只是又换了一处。
func rankRow(t *testing.T, s *server, skill string) map[string]any {
	t.Helper()
	out := s.handle(core.Frame{T: "arbiter.rank", V: 1,
		Body: json.RawMessage(`{"offers":[{"action":"read.file","skill":"` + skill +
			`","task_class":"task_exec","value":10,"cost":1,"latency":100}]}`)})
	if out.Err != nil {
		t.Fatalf("arbiter.rank refused: %s", out.Err.Reason)
	}
	var got struct {
		Ranked []map[string]any `json:"ranked"`
	}
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Ranked) != 1 {
		t.Fatalf("want one ranked row, got %d", len(got.Ranked))
	}
	return got.Ranked[0]
}

func TestRankRowsCarryTheSampleSize(t *testing.T) {
	s := testServer(t)
	for i := 0; i < 30; i++ {
		observeOnce(t, s, "proven", "task_exec")
	}
	fresh := rankRow(t, s, "brand.new")
	proven := rankRow(t, s, "proven")

	// ★ 而这两行此前**逐字相同**（除了 skill）。先把那件事钉住：
	if fresh["p"] != proven["p"] {
		t.Fatalf("this test is about what the row says BEYOND p: p is %v vs %v, "+
			"so the fixture no longer reproduces the defect", fresh["p"], proven["p"])
	}
	// ★ 所以区分它们的东西必须在行里，而不是在别的帧里。
	if fresh["weight"] == proven["weight"] {
		t.Fatalf("the two rows must be distinguishable: weight is %v for both — "+
			"the sample size is the only thing that separates them", fresh["weight"])
	}
	if fresh["weight"] != float64(0) {
		t.Fatalf("a key with no observations has weight 0, got %v", fresh["weight"])
	}
	if w, _ := proven["weight"].(float64); w <= 20 {
		t.Fatalf("30 observations under decay weigh more than 20, got %v", proven["weight"])
	}
	// ★ 而价格是在某个类下读的：那一行必须点名它。
	if fresh["task_class"] != "task_exec" {
		t.Fatalf("the row must name the task class the posterior was read under, got %v", fresh["task_class"])
	}
	// ★ 而 α/β 必须与器官算出来的一致，不能在这里另算一遍。
	post, ok := fresh["posterior"].(map[string]any)
	if !ok {
		t.Fatalf("posterior must be reported: %v", fresh)
	}
	if post["alpha"] != float64(1) || post["beta"] != float64(1) {
		t.Fatalf("an unobserved key is Beta(1,1), got %v", post)
	}
}

// ★ 而 `margin > 0` 那一支此前**连 `p` 都不报** —— 一个「谁赢了」的回答里没有
// 那个赢家的成功概率。现在它与默认那一支共用同一个行形状。
func TestTheMarginBranchAlsoReportsTheChosenRow(t *testing.T) {
	s := testServer(t)
	for i := 0; i < 30; i++ {
		observeOnce(t, s, "proven", "task_exec")
	}
	out := s.handle(core.Frame{T: "arbiter.rank", V: 1, Body: json.RawMessage(
		`{"margin":0.5,"offers":[` +
			`{"action":"read.file","skill":"brand.new","task_class":"task_exec","value":10,"cost":1,"latency":100},` +
			`{"action":"write.file","skill":"proven","task_class":"task_exec","value":10,"cost":1,"latency":100}]}`)})
	if out.Err != nil {
		t.Fatalf("arbiter.rank refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// ★ `chosen` **仍然是那个动作名** —— 把它的类型从字符串改成对象是一次
	// **破坏性**改动，而这一轮要修的是「缺了几个字段」。
	if _, isString := got["chosen"].(string); !isString {
		t.Fatalf("chosen must stay the action name (a string), got %T: changing its type "+
			"is a breaking change and is not what this round is about", got["chosen"])
	}
	row, ok := got["chosen_offer"].(map[string]any)
	if !ok {
		t.Fatalf("the full chosen row must be reported under its own name: %v", got)
	}
	for _, k := range []string{"p", "utility", "weight", "posterior", "task_class"} {
		if _, has := row[k]; !has {
			t.Fatalf("the chosen row is missing %q: %v", k, row)
		}
	}
}

// ★★★ 两扇门必须给同一个行形状 —— 这是这一条存在的理由。
func TestBothDoorsUseTheSameRowShape(t *testing.T) {
	s := testServer(t)
	observeOnce(t, s, "s.a", "task_exec")

	ranked := rankRow(t, s, "s.a")
	quote := s.handle(core.Frame{T: "escalation.quote", V: 1, Body: json.RawMessage(
		`{"task_class":"task_exec","offers":[{"action":"read.file","skill":"s.a",` +
			`"task_class":"task_exec","value":10,"cost":1,"latency":100}]}`)})
	if quote.Err != nil {
		t.Fatalf("escalation.quote refused: %s", quote.Err.Reason)
	}
	var q struct {
		Ranked []map[string]any `json:"ranked"`
	}
	if err := json.Unmarshal(quote.Body, &q); err != nil {
		t.Fatalf("decode: %v", err)
	}
	row := q.Ranked[0]
	for _, k := range []string{"action", "skill", "task_class", "p", "utility", "weight", "posterior"} {
		if _, has := ranked[k]; !has {
			t.Fatalf("arbiter.rank's row is missing %q", k)
		}
		if _, has := row[k]; !has {
			t.Fatalf("escalation.quote's row is missing %q", k)
		}
		// ★ 而同一个 key 在两扇门下的值必须相同。
		if a, b := jsonNum(ranked[k]), jsonNum(row[k]); a != b {
			t.Fatalf("%q differs between the two doors: %v vs %v", k, a, b)
		}
	}
	_ = arbiter.Key{}
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ★★★ 报价单的 id 必须**从被报出去的那一行**算出来。
//
// 它此前是从 `{"action","skill","p","u"}` 算的 —— 一个**只有 p 与 u** 的投影。
// 而 `p` 是上界、在 `Exploration = 0.25` 下被夹在 1 上一大片，于是实测（同一个
// 技能的一生）：
//
//	obs= 0 p=1 u=9 id=08c6d3a1c40c…
//	obs=20 p=1 u=9 id=08c6d3a1c40c…   ← 同一个 id
//	obs=40 p=1 u=9 id=08c6d3a1c40c…   ← 同一个 id
//
// 而 `quoteID` 的文档逐字写着「It is derived from the ranked result, so it changes
// when any posterior moves」。**后验移动了，id 没有。**
//
// 而 `escalationDecide` 拒一份过期报价**就是靠比这个 id** —— 于是那个检查在
// 「p 与 u 恰好没动」的那一大片里是聋的。
func TestTheQuoteIDMovesWhenThePosteriorMoves(t *testing.T) {
	s := testServer(t)

	quoteIDFor := func() (string, map[string]any) {
		t.Helper()
		out := s.handle(core.Frame{T: "escalation.quote", V: 1, Body: json.RawMessage(
			`{"task_class":"task_exec","offers":[{"action":"read.file","skill":"s.a",` +
				`"task_class":"task_exec","value":10,"cost":1,"latency":100}]}`)})
		if out.Err != nil {
			t.Fatalf("escalation.quote refused: %s", out.Err.Reason)
		}
		var got struct {
			QuoteID string           `json:"quote_id"`
			P       float64          `json:"p"`
			Utility float64          `json:"utility"`
			Ranked  []map[string]any `json:"ranked"`
		}
		if err := json.Unmarshal(out.Body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got.QuoteID, got.Ranked[0]
	}

	before, rowBefore := quoteIDFor()
	for i := 0; i < 20; i++ {
		observeOnce(t, s, "s.a", "task_exec")
	}
	after, rowAfter := quoteIDFor()

	// ★ 先钉住「这一对点恰好是 p 与 u 都没动的那一对」—— 否则这一条测的是别的东西。
	if rowBefore["p"] != rowAfter["p"] || rowBefore["utility"] != rowAfter["utility"] {
		t.Fatalf("this test is about what the id says BEYOND p and u: they moved "+
			"(%v/%v -> %v/%v), so the fixture no longer reproduces the defect",
			rowBefore["p"], rowBefore["utility"], rowAfter["p"], rowAfter["utility"])
	}
	// ★ 而样本量动了。
	if rowBefore["weight"] == rowAfter["weight"] {
		t.Fatalf("the weight must have moved: %v vs %v", rowBefore["weight"], rowAfter["weight"])
	}
	// ★ 所以 id 必须动。
	if before == after {
		t.Fatalf("the quote id must change when the posterior moves, even when p and "+
			"utility happen to be pinned: %s for both", before)
	}
}
