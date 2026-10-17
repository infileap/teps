package main

import (
	"encoding/json"
	"strings"
	"testing"

	"teps/core"
	"teps/organs/intake"
	"teps/store"
)

// ★★★ 率必须带着它的分母**与那句话**。
//
// `organs/intake` 的 `Stats` 有 `Total` / `ByLayer` / `InterceptRate` / `Scope`，
// 而它的注释逐字写着那句话为什么必须一起走：
//
//	Scope always travels with the rate. A rate without its denominator is how
//	"60-80%" became a claim nobody could check.
//
// 而 `metrics.get` 是**这个率被报出来的唯一一个地方**，它此前把 `by_layer`
// 与 `scope` 都丢了。实测（取进 4 个任务，全落在 full 层）：
//
//	{"answered_without_context":0,"defined":true,"intercept_rate":0,
//	 "source":"task.stats","tasks":4}
//
// 读的人看不到那 4 个是怎么被答的。而 `source: "task.stats"` 读起来像在回答
// 那个问题 —— 它说的是这个数**从哪来**，不是它**除的是什么**。两件事，两个名字。
func metricsServer(t *testing.T) *server {
	t.Helper()
	s := testServer(t)
	ts, ref := newTaskStore(s.log)
	if ref != nil {
		t.Fatalf("open task store: %s", ref.Reason)
	}
	s.tasks = ts
	// ★ metrics.get 还会问技能店（accuracy 要数「声明由规划器产出的链」），
	// 而 testServer 不建它 —— 不建就是一次 nil 解引用，而那不是这一条要测的东西。
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	s.skills = st
	return s
}

func coverageOf(t *testing.T, s *server) map[string]any {
	t.Helper()
	var out core.Frame
	s.handleMetrics(core.Frame{T: "metrics.get", V: 1}, &out)
	if out.Err != nil {
		t.Fatalf("metrics.get refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	c, ok := got["coverage"].(map[string]any)
	if !ok {
		t.Fatalf("no coverage block: %v", got)
	}
	return c
}

func TestCoverageCarriesItsPartsAndItsScope(t *testing.T) {
	s := metricsServer(t)
	for i := 0; i < 4; i++ {
		rec := intake.Record{TaskID: "t" + string(rune('a'+i)), Stimulus: "q"}
		if _, ref := s.tasks.commit(taskRecord{
			Rec: "task", TaskID: rec.TaskID, Stimulus: rec.Stimulus,
			Layer: string(intake.LayerFull),
		}, rec.TaskID); ref != nil {
			t.Fatalf("commit: %s", ref.Reason)
		}
	}
	c := coverageOf(t, s)

	if c["tasks"] != float64(4) {
		t.Fatalf("tasks = %v, want 4", c["tasks"])
	}
	byLayer, ok := c["by_layer"].(map[string]any)
	if !ok {
		t.Fatalf("by_layer must be reported: a rate whose parts are missing does not say how the tasks were answered (%v)", c)
	}
	if byLayer["full"] != float64(4) {
		t.Fatalf("by_layer = %v, want full:4", byLayer)
	}
	// ★ 分项之和必须等于总数 —— 否则分项是第二份事实。
	sum := 0.0
	for _, n := range byLayer {
		sum += n.(float64)
	}
	if sum != c["tasks"].(float64) {
		t.Fatalf("by_layer sums to %v but tasks says %v", sum, c["tasks"])
	}
	// ★ 而那句话必须来自器官，不是在这里另造一句。
	scope, ok := c["scope"].(string)
	if !ok || scope == "" {
		t.Fatalf("scope must be reported: a rate without the words for its denominator is the claim nobody could check (%v)", c)
	}
	if scope != intake.ScopeArrived {
		t.Fatalf("scope = %q, want the organ's own sentence %q", scope, intake.ScopeArrived)
	}
	// ★ 而 scope 不是 source：一个说从哪来，一个说除的是什么。
	if src, _ := c["source"].(string); src == scope {
		t.Fatal("scope and source must not be the same string: one says where the number came from, the other says what it is over")
	}
}

// 空登记表：分项为空、scope 仍在，而 defined 为 false。
func TestCoverageOnAnEmptyIntakeStillCarriesTheScope(t *testing.T) {
	s := metricsServer(t)
	c := coverageOf(t, s)
	if c["defined"] != false {
		t.Fatalf("an intake with no arrivals has no rate: %v", c["defined"])
	}
	if scope, _ := c["scope"].(string); scope == "" {
		t.Fatal("the scope sentence is not a fact about the data — it is always true of this intake")
	}
	if !strings.Contains(c["reason"].(string), "denominator does not exist") {
		t.Fatalf("the reason must say why, got %v", c["reason"])
	}
}
