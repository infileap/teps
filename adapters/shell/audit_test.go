package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"teps/core"
)

// ★★★ 一个窗口必须报出它的窗口。
//
// 此前 audit.tail 只回 entries / head / seq / epoch：链上有 25 条、没传 limit
// 时它回 20 条，而回答里没有任何一处说这件事。调用方分不出「这 20 条是我要的」
// 还是「服务端替我决定的」，也分不出「这就是链」还是「这是链的尾巴」。
//
// 与 metrics.accuracy 报 traced/chains_run/unrun、与 waste 报它的两个计数是
// 同一条：**一个数是某个窗口里的数时，窗口是它的一部分。**
func tailBody(t *testing.T, s *server, limit int) map[string]any {
	t.Helper()
	var body []byte
	if limit > 0 {
		body = []byte(`{"limit":` + strconv.Itoa(limit) + `}`)
	}
	var out core.Frame
	s.auditTail(core.Frame{T: "audit.tail", V: 1, Body: body}, &out)
	if out.Err != nil {
		t.Fatalf("audit.tail refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func seed(t *testing.T, s *server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		// ★ key 必须不同：`Log.Append` 是按 key 幂等的，25 条同一个 key
		// 只会写进 1 条 —— 而那样的夹具会让「窗口」的断言全部落在一个
		// 只有一条记录的链上，什么也证不出来。
		if _, ref := s.log.Append(fmt.Sprintf("k%d", i), []byte(`{"n":1}`)); ref != nil {
			t.Fatalf("append: %s", ref.Reason)
		}
	}
}

func TestTailReportsTheWindowItRead(t *testing.T) {
	s := testServer(t)
	seed(t, s, 25)

	got := tailBody(t, s, 0)
	if got["limit"] != float64(20) {
		t.Fatalf("limit = %v, want 20 (the server default must be visible)", got["limit"])
	}
	if got["returned"] != float64(20) {
		t.Fatalf("returned = %v, want 20", got["returned"])
	}
	if got["seq"] != float64(25) {
		t.Fatalf("seq = %v, want 25", got["seq"])
	}
	// 6..25 —— 25 条里取最后 20 条。
	if got["first_seq"] != float64(6) || got["last_seq"] != float64(25) {
		t.Fatalf("window ends = %v..%v, want 6..25", got["first_seq"], got["last_seq"])
	}
	if got["truncated"] != true {
		t.Fatal("the chain is longer than the window, so truncated must be true")
	}
	entries := got["entries"].([]any)
	if len(entries) != 20 {
		t.Fatalf("entries = %d, want 20", len(entries))
	}
	// ★ 报出来的那个数必须与**真的回来的那些行**一致。分开算就是两处，
	// 而两处就会有机会说不同的年份。
	if got["returned"] != float64(len(entries)) {
		t.Fatalf("returned = %v but %d rows came back", got["returned"], len(entries))
	}
	last := entries[len(entries)-1].(map[string]any)
	if got["last_seq"] != last["seq"] {
		t.Fatalf("last_seq = %v but the last row is seq %v", got["last_seq"], last["seq"])
	}
}

// 反过来：窗口就是整条链时 truncated 必须是 false —— 否则这个字段只是
// 「总是 true」，而一个恒真的字段不是一个读数。
func TestTailSaysWhenTheWindowIsTheWholeChain(t *testing.T) {
	s := testServer(t)
	seed(t, s, 25)

	got := tailBody(t, s, 30)
	if got["limit"] != float64(30) {
		t.Fatalf("limit = %v, want the requested 30", got["limit"])
	}
	if got["returned"] != float64(25) {
		t.Fatalf("returned = %v, want 25 (the whole chain)", got["returned"])
	}
	if got["truncated"] != false {
		t.Fatal("the window is the whole chain, so truncated must be false")
	}
	if got["first_seq"] != float64(1) {
		t.Fatalf("first_seq = %v, want 1", got["first_seq"])
	}
}

// 空链：两端没有定义，但必须是一个数而不是 null —— 一个缺席的序号
// 会在客户端那边变成一个未定义的比较。
func TestTailOnAnEmptyChainStillReportsNumbers(t *testing.T) {
	s := testServer(t)
	got := tailBody(t, s, 0)
	if got["returned"] != float64(0) {
		t.Fatalf("returned = %v, want 0", got["returned"])
	}
	if got["truncated"] != false {
		t.Fatal("an empty chain is not a truncated window")
	}
	if got["first_seq"] != float64(0) || got["last_seq"] != float64(0) {
		t.Fatalf("ends = %v..%v, want 0..0", got["first_seq"], got["last_seq"])
	}
}
