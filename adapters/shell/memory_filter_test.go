package main

import (
	"encoding/json"
	"strings"
	"testing"

	"teps/core"
	"teps/evidence"
)

// ★★★ 一次检索必须报出**它按什么滤的**，以及**每条命中的是什么**。
//
// `adapters/shell/knowledge.go` 的 `memory.retrieve` 此前只回
// `count` / `ids` / `texts` / `total`，而 `organs/memory/memory.go:276` 的
// `Retrieve` 正是按 `MinEvidence` 与 `Tags` 过滤的，`Record` 还有
// `Evidence` / `Tags` / `Source` / 三个时间字段。实测（三条记录 E0/E1/E2，
// 标签分别 x/x/y）：登记表里共 3 条，四次查询回了 2/1/1/2 —— 而**读的人分不出
// 某一条是因为等级不够被滤掉、还是因为标签不对、还是根本不存在**。
func memoryServer(t *testing.T) *server {
	t.Helper()
	s := testServer(t)
	ms, ref := newMemoryStore(s.log)
	if ref != nil {
		t.Fatalf("open memory: %s", ref.Reason)
	}
	s.memory = ms
	return s
}

func writeMemory(t *testing.T, s *server, key, id, text string, lvl evidence.Level, tags []string) {
	t.Helper()
	rec := memoryRecord{
		Op: "write", ID: id, Text: text, Evidence: string(lvl),
		RecordedAt: 100, Tags: tags,
	}
	s.memory.lock()
	defer s.memory.unlock()
	if _, ref := s.memory.commit(rec, key); ref != nil {
		t.Fatalf("write %s: %s", id, ref.Reason)
	}
}

func retrieveBody(t *testing.T, s *server, body string) map[string]any {
	t.Helper()
	f := core.Frame{T: "memory.retrieve", V: 1}
	if body == "" {
		body = "{}" // 空 body 是「没给」，而这一条要测的是「给了但没写 min_evidence」
	}
	f.Body = json.RawMessage(body)
	var out core.Frame
	s.handleMemory(f, &out)
	if out.Err != nil {
		t.Fatalf("memory.retrieve refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestRetrieveReportsTheRecordsAndTheFilter(t *testing.T) {
	s := memoryServer(t)
	writeMemory(t, s, "m0", "r0", "E0 的一条", evidence.L0, []string{"x"})
	writeMemory(t, s, "m1", "r1", "E1 的一条", evidence.L1, []string{"x"})
	writeMemory(t, s, "m2", "r2", "E2 的一条", evidence.L2, []string{"y"})

	got := retrieveBody(t, s, "")
	if got["total"] != float64(3) {
		t.Fatalf("total = %v, want 3 (the whole store)", got["total"])
	}
	// 缺省 floor 是 E1 ⇒ E0 被滤掉。
	if got["count"] != float64(2) {
		t.Fatalf("count = %v, want 2 (E0 is below the default floor)", got["count"])
	}
	// ★ 命中的记录本身带着判据读的那些字段。
	rows, ok := got["records"].([]any)
	if !ok {
		t.Fatalf("records must be reported: ids and texts alone do not say which records these are (%v)", got)
	}
	if len(rows) != 2 {
		t.Fatalf("records has %d entries, want 2", len(rows))
	}
	for _, r := range rows {
		row := r.(map[string]any)
		if row["evidence"] == nil || row["evidence"] == "" {
			t.Fatalf("a returned record must carry its evidence level: %v", row)
		}
	}
	// ★ 生效的过滤条件，且缺省值必须**报出来**。
	f, ok := got["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter must be reported: without it count<total is unexplained (%v)", got)
	}
	if f["min_evidence"] != string(evidence.L1) {
		t.Fatalf("an omitted min_evidence must be reported as the effective default E1, got %v", f["min_evidence"])
	}
	if !strings.Contains(got["basis"].(string), "passed the filter") {
		t.Fatalf("the basis must say what count and total mean, got %v", got["basis"])
	}
}

// 每一次命中都必须**真的**满足它自己报出来的过滤条件 —— 否则 filter 是第二份事实。
func TestEveryReturnedRecordSatisfiesTheReportedFilter(t *testing.T) {
	s := memoryServer(t)
	writeMemory(t, s, "m0", "r0", "E0 的一条", evidence.L0, []string{"x"})
	writeMemory(t, s, "m1", "r1", "E1 的一条", evidence.L1, []string{"x"})
	writeMemory(t, s, "m2", "r2", "E2 的一条", evidence.L2, []string{"y"})

	got := retrieveBody(t, s, `{"min_evidence":"E1","tags":["x"]}`)
	f := got["filter"].(map[string]any)
	floor := evidence.Level(f["min_evidence"].(string))
	rows := got["records"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want exactly r1, got %d records", len(rows))
	}
	for _, r := range rows {
		row := r.(map[string]any)
		lvl := evidence.Level(row["evidence"].(string))
		if !evidence.AtLeast(lvl, floor) {
			t.Fatalf("%v came back although the filter says %s", row, floor)
		}
		tags, _ := row["tags"].([]any)
		found := false
		for _, tg := range tags {
			if tg == "x" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%v came back although the filter wants tag x", row)
		}
	}
	if got["count"] != float64(len(rows)) {
		t.Fatalf("count %v disagrees with records %d", got["count"], len(rows))
	}
}

// ★ 报出来的 filter 必须是**生效的**那一份，不是请求原文：不传与传 E1
// 在回答里必须长得一样。
func TestAnOmittedFloorIsReportedAsTheEffectiveOne(t *testing.T) {
	s := memoryServer(t)
	writeMemory(t, s, "m1", "r1", "E1 的一条", evidence.L1, nil)

	omitted := retrieveBody(t, s, "")
	explicit := retrieveBody(t, s, `{"min_evidence":"E1"}`)
	a := omitted["filter"].(map[string]any)["min_evidence"]
	b := explicit["filter"].(map[string]any)["min_evidence"]
	if a != b {
		t.Fatalf("omitting min_evidence reported %v while passing E1 reported %v: those are two names for one thing", a, b)
	}
	if a != string(evidence.L1) {
		t.Fatalf("the effective floor is E1, got %v", a)
	}
}
