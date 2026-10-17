package main

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"teps/core"
	"teps/organs/breaker"
)

// ★★★ 三个状态，三个计数。
//
// 此前 breaker.state 的 `open` 是 `if st != breaker.Closed { open++ }` ——
// 一个叫 open 的数数的是**不是闭的**，也就是 open + half-open。而这两个状态
// 在操作上是相反的：open 拒绝一切，half-open **放一个探针进去**。
// 一个读 `open: 1` 以为「不会有东西被尝试」的人，正好读反了。
// tripSeq 让每一条夹具记录拿到不同的幂等键。
var tripSeq int

func breakerServer(t *testing.T) *server {
	t.Helper()
	s := testServer(t)
	bs, ref := newBreakerStore(s.log)
	if ref != nil {
		t.Fatalf("open breaker store: %s", ref.Reason)
	}
	s.breakers = bs
	return s
}

// trip 直接写一条宿主失败的记录。绕过 recordTrip 是刻意的：那个函数用
// time.Now()，而这里要的是一个**受控的时刻**，否则「冷却过去了」这件事
// 只能靠真的等 30 秒来构造 —— 一条要等 30 秒的测试就是一条会被跳过的测试。
func trip(t *testing.T, s *server, host string, at int64, ok bool) {
	t.Helper()
	// ★ key 必须逐次不同：`core.Log.Append` 是按 key 幂等的，三次同一个 key
	// 只会写进 1 条 —— 而那样的夹具会让「连败到阈值」永远到不了阈值，
	// 测试报的是 open=0，看起来像被测的代码错了。
	tripSeq++
	if _, ref := s.breakers.append(breakerRecord{
		Rec: "breaker", Host: host, OK: ok, At: at,
	}, host+keyTrip+strconv.Itoa(tripSeq)); ref != nil {
		t.Fatalf("append trip: %s", ref.Reason)
	}
}

func breakerBody(t *testing.T, s *server) map[string]any {
	t.Helper()
	var out core.Frame
	s.breakerState(core.Frame{T: "breaker.state", V: 1}, &out)
	if out.Err != nil {
		t.Fatalf("breaker.state refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestBreakerCountsHalfOpenSeparatelyFromOpen(t *testing.T) {
	s := breakerServer(t)
	now := time.Now().UnixMilli()

	// 一个宿主：三次失败，就在此刻 ⇒ open（冷却还没过）。
	for i := 0; i < breaker.Threshold; i++ {
		trip(t, s, "host.open", now, false)
	}
	got := breakerBody(t, s)
	if got["open"] != float64(1) || got["half_open"] != float64(0) {
		t.Fatalf("just-tripped: open=%v half_open=%v, want 1 and 0", got["open"], got["half_open"])
	}
	if got["closed"] != float64(0) {
		t.Fatalf("closed = %v, want 0", got["closed"])
	}

	// 另一个宿主：三次失败，但发生在冷却之前 ⇒ half-open。
	old := now - breaker.CooldownMS - 1
	for i := 0; i < breaker.Threshold; i++ {
		trip(t, s, "host.half", old, false)
	}
	got = breakerBody(t, s)
	if got["open"] != float64(1) {
		t.Fatalf("open = %v, want 1 (only the one that is really open)", got["open"])
	}
	if got["half_open"] != float64(1) {
		t.Fatalf("half_open = %v, want 1: half-open is not open — it is the one state "+
			"in which something IS being tried", got["half_open"])
	}
	if got["hosts_total"] != float64(2) {
		t.Fatalf("hosts_total = %v, want 2", got["hosts_total"])
	}
	// ★ 三个计数必须**划分**那些宿主：一个宿主恰好落在一个计数里。
	if got["open"].(float64)+got["half_open"].(float64)+got["closed"].(float64) != got["hosts_total"].(float64) {
		t.Fatalf("the three counts do not partition the hosts: %v + %v + %v != %v",
			got["open"], got["half_open"], got["closed"], got["hosts_total"])
	}
	// ★ 而且每个计数必须与那一行自己的 state 对得上 —— 否则计数是第二份事实。
	byState := map[string]int{}
	for _, r := range got["hosts"].([]any) {
		byState[r.(map[string]any)["state"].(string)]++
	}
	if byState["open"] != int(got["open"].(float64)) || byState["half-open"] != int(got["half_open"].(float64)) {
		t.Fatalf("the counts disagree with the rows: rows=%v open=%v half_open=%v",
			byState, got["open"], got["half_open"])
	}
}

// 一个宿主从没失败过 ⇒ closed，而且它必须出现在 hosts 里。
func TestBreakerCountsClosedHosts(t *testing.T) {
	s := breakerServer(t)
	now := time.Now().UnixMilli()
	trip(t, s, "host.good", now, false)
	trip(t, s, "host.good", now, true) // 一次成功把连败清零
	got := breakerBody(t, s)
	if got["closed"] != float64(1) || got["open"] != float64(0) || got["half_open"] != float64(0) {
		t.Fatalf("open=%v half_open=%v closed=%v, want 0/0/1",
			got["open"], got["half_open"], got["closed"])
	}
}
