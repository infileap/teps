package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"teps/core"
)

// ★★★ 一个被截断的列表必须说它被截断了。
//
// `adapters/shell/intake.go` 的 `taskStats` 此前是
// `if len(recent) > 20 { recent = recent[len(recent)-20:] }` 然后只回
// `{"stats": stats, "recent": recent}`。实测（取进 25 个任务）：
//
//	{"recent":[…20 条…], "stats":{"total":25,"by_layer":{"full":25},…}}
//
// `total` 是 25、`recent` 有 20 条，而**没有一处说这件事**；也没有一处说那
// 20 条是**最新**的（`t6`…`t25`）而不是最早的。读的人只能做减法，还得猜方向。
//
// 这与 `audit.tail` 那一轮是同一条（那里现在报 limit/returned/first_seq/
// last_seq/truncated/basis），只是换了一处：**一个数的窗口是它的一部分。**
func taskServer(t *testing.T) *server {
	t.Helper()
	s := testServer(t)
	ts, ref := newTaskStore(s.log)
	if ref != nil {
		t.Fatalf("open task store: %s", ref.Reason)
	}
	s.tasks = ts
	return s
}

func statsBody(t *testing.T, s *server) map[string]any {
	t.Helper()
	var out core.Frame
	s.handleIntake(core.Frame{T: "task.stats", V: 1}, &out)
	if out.Err != nil {
		t.Fatalf("task.stats refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func takeIn(t *testing.T, s *server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		rec := taskRecord{Rec: "task", TaskID: fmt.Sprintf("t%d", i), Stimulus: "q"}
		s.tasks.lock()
		_, ref := s.tasks.commit(rec, fmt.Sprintf("k%d", i))
		s.tasks.unlock()
		if ref != nil {
			t.Fatalf("commit %d: %s", i, ref.Reason)
		}
	}
}

func TestATruncatedWindowSaysSo(t *testing.T) {
	s := taskServer(t)
	takeIn(t, s, taskRecentLimit+5)
	got := statsBody(t, s)

	recent := got["recent"].([]any)
	if len(recent) != taskRecentLimit {
		t.Fatalf("recent has %d entries, want the limit %d", len(recent), taskRecentLimit)
	}
	if got["limit"] != float64(taskRecentLimit) {
		t.Fatalf("limit = %v, want %d", got["limit"], taskRecentLimit)
	}
	if got["returned"] != float64(len(recent)) {
		t.Fatalf("returned = %v but recent holds %d", got["returned"], len(recent))
	}
	// ★ 而它必须**说**自己被截断了 —— 否则 total 与 recent 的差只能靠减法。
	if got["truncated"] != true {
		t.Fatalf("truncated = %v, want true: stats.total is %v and recent holds %d",
			got["truncated"], got["stats"].(map[string]any)["total"], len(recent))
	}
	// ★ 端点必须点名，且必须是**最新**的那一批。
	first := recent[0].(map[string]any)["task_id"]
	last := recent[len(recent)-1].(map[string]any)["task_id"]
	if got["first_task_id"] != first {
		t.Fatalf("first_task_id = %v but recent starts at %v", got["first_task_id"], first)
	}
	if got["last_task_id"] != last {
		t.Fatalf("last_task_id = %v but recent ends at %v", got["last_task_id"], last)
	}
	if last != fmt.Sprintf("t%d", taskRecentLimit+4) {
		t.Fatalf("the window must hold the NEWEST arrivals, it ends at %v", last)
	}
}

// ★ 没截断时也必须**说**没截断：沉默与「没截断」在回答里长得一样。
func TestAnUntruncatedWindowSaysThatToo(t *testing.T) {
	s := taskServer(t)
	takeIn(t, s, 3)
	got := statsBody(t, s)
	if got["truncated"] != false {
		t.Fatalf("truncated = %v, want false", got["truncated"])
	}
	if got["returned"] != float64(3) {
		t.Fatalf("returned = %v, want 3", got["returned"])
	}
	// ★ 而上限仍然报出来 —— 一个不说的上限与没有上限在回答里长得一样。
	if got["limit"] != float64(taskRecentLimit) {
		t.Fatalf("limit must be reported even when nothing was cut: %v", got["limit"])
	}
}
