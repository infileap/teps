package main

import (
	"encoding/json"
	"testing"

	"teps/core"
	"teps/organs/arbiter"
)

// ★★★ 这个 key 自己被看过几次 —— 不是登记表的次数，也不是带遗忘的权重。
//
// `arbiter.state` 此前报的是 `n := len(s.decisions.obs)`（**整张表**的观测次数），
// 而它旁边坐的是**这一个 key** 的 alpha/beta/mean/upper。实测（s.a 拿 3 次、
// s.b 拿 7 次，表里共 10 次）：两个技能都报 `observations = 10`，而它们的证据量
// 差三倍 —— 而「这个技能证据够不够」正是读的人拿 `observations` 要回答的问题。
//
// ★ 而**同一个字段名在这个帧里还有第二个意思**：不点名时它就是表级的那个数。
// 一个名字两个意思，在同一个帧里 —— 那就不是一个名字。所以现在三个数三个名字。
func stateOf(t *testing.T, s *server, skill, class string) map[string]any {
	t.Helper()
	var f core.Frame
	if skill == "" {
		// ★ 空 body 是「没给」；不点名要写成 `{}`（`decodeBody` 现在会拒空 body）。
		f = core.Frame{T: "arbiter.state", V: 1, Body: json.RawMessage(`{}`)}
	} else {
		f = core.Frame{T: "arbiter.state", V: 1,
			Body: json.RawMessage(`{"skill":"` + skill + `","task_class":"` + class + `"}`)}
	}
	out := s.handle(f)
	if out.Err != nil {
		t.Fatalf("arbiter.state refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// observeOnce 记一条观测。
//
// ★ 登记表里没有 `observe` 方法 —— `table()` 就是 `arbiter.Fold(st.obs, …)`，
// 所以「记一次观测」就是往 `st.obs` 里追加一条，别的什么都不用做。
func observeOnce(t *testing.T, s *server, skill, class string) {
	t.Helper()
	s.decisions.lock()
	s.decisions.obs = append(s.decisions.obs, arbiter.Observation{
		Key: arbiter.Key{Skill: skill, TaskClass: class}, OK: true,
	})
	s.decisions.unlock()
}

func TestObservationsIsThisKeysCountNotTheTables(t *testing.T) {
	s := testServer(t)
	for i := 0; i < 3; i++ {
		observeOnce(t, s, "s.a", "task_exec")
	}
	for i := 0; i < 7; i++ {
		observeOnce(t, s, "s.b", "task_exec")
	}

	a := stateOf(t, s, "s.a", "task_exec")
	if a["observations"] != float64(3) {
		t.Fatalf("s.a was observed 3 times, arbiter.state says %v", a["observations"])
	}
	if a["table_observations"] != float64(10) {
		t.Fatalf("the table holds 10 observations, got %v", a["table_observations"])
	}
	b := stateOf(t, s, "s.b", "task_exec")
	if b["observations"] != float64(7) {
		t.Fatalf("s.b was observed 7 times, arbiter.state says %v", b["observations"])
	}
	if b["table_observations"] != float64(10) {
		t.Fatalf("the table holds 10 observations, got %v", b["table_observations"])
	}
	// ★ 而两个技能的表级那个数**必须相同** —— 它是表级的。
	if a["table_observations"] != b["table_observations"] {
		t.Fatalf("table_observations is a fact about the table: %v vs %v",
			a["table_observations"], b["table_observations"])
	}
	// ★ 而它俩自己的那个数必须**不同** —— 否则这一条测不出上面那个区别。
	if a["observations"] == b["observations"] {
		t.Fatal("the fixture cannot tell the two counts apart: both keys have the same count")
	}
	// ★ 不点名时 observations 就是表级那个数：同一个名字在两条分支上指同一件事。
	all := stateOf(t, s, "", "")
	if all["observations"] != float64(10) || all["table_observations"] != float64(10) {
		t.Fatalf("unnamed: %v / %v", all["observations"], all["table_observations"])
	}
}

// ★★★ 而 `weight` 是**另一个数、另一个名字**。
//
// 第一版修法把它写成 `Posterior.Observations() = Count() - 2`，那个名字是错的：
// 有遗忘（`DefaultDecay = 0.98`）时先验的两个伪计数**也在一起衰减**，实测记
// 1 次是 **0.96**、3 次是 2.82、7 次是 6.33。**一个名字读作 A 而值是 B。**
func TestTheWeightIsNotTheCount(t *testing.T) {
	s := testServer(t)
	observeOnce(t, s, "s.a", "task_exec")
	got := stateOf(t, s, "s.a", "task_exec")

	if got["observations"] != float64(1) {
		t.Fatalf("one observation, count says %v", got["observations"])
	}
	w, ok := got["weight"].(float64)
	if !ok {
		t.Fatalf("weight must be reported: a probability with no sample size is the claim nobody can check (%v)", got)
	}
	// ★ 一次观测的权重**小于 1** —— 因为先验那两个伪计数也在衰减。
	if w >= 1 {
		t.Fatalf("the weight of one observation under decay must be below 1, got %v", w)
	}
	if w <= 0 {
		t.Fatalf("the weight of one observation must be positive, got %v", w)
	}
	// ★ 而它必须就是器官里那个减法 —— 别在这里另算一遍。
	p := arbiter.Posterior{Alpha: got["alpha"].(float64), Beta: got["beta"].(float64)}
	if diff := p.Weight() - w; diff > 1e-3 || diff < -1e-3 {
		t.Fatalf("weight %v is not the posterior's own Weight() %v", w, p.Weight())
	}
}
