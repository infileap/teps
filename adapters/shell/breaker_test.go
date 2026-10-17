package main

import (
	"encoding/json"
	"strconv"
	"strings"
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

// ★★★ 半开的探针必须是一份**租约**，不是一个状态读数。
//
// `organs/breaker/breaker.go` 的 `Allow` 在 `case HalfOpen` 里无条件回 nil，
// 于是「半开只放一个探针」靠的是**副作用**：第一个探针的失败被写进 trips、
// 把状态推回 open。串行时恰好成立；**并发时不成立**。实测（三次宿主失败 →
// 等 31 秒进 half-open → 同时发 5 次 skill.invoke）：
//
//	d7 放行 · d5 放行 · d4 circuit_open · d8 circuit_open · d6 放行
//
// **放行了三次**，而 README 写着「半开只放一个探针」。而本该拦住它的
// `HalfOpenProbe` 一个调用点都没有，它自己的注释逐字写着后果：
//
//	A half-open breaker that admitted every attempt would take the full traffic
//	of every skill behind it on the first probe.
//
// 与算力池那条「容量是租约不是计数」是同一条：**一个「现在有几个」的问题，
// 读状态是答不对的 —— 状态说你到了半开，不说那个探针已经派出去了。**
func halfOpenServer(t *testing.T) *server {
	t.Helper()
	s := breakerServer(t)
	// 三次失败，且都发生在冷却之前 ⇒ 状态是 half-open。
	old := time.Now().UnixMilli() - breaker.CooldownMS - 1
	for i := 0; i < breaker.Threshold; i++ {
		trip(t, s, "host.x", old, false)
	}
	if got := breaker.StateOf(s.breakers.trips, "host.x", time.Now().UnixMilli()); got != breaker.HalfOpen {
		t.Fatalf("precondition: want half-open, got %s", got)
	}
	return s
}

func TestHalfOpenAdmitsExactlyOneProbe(t *testing.T) {
	s := halfOpenServer(t)

	if ref := s.allowHost("host.x", "probe-1"); ref != nil {
		t.Fatalf("the first probe must be admitted: %s", ref.Reason)
	}
	ref := s.allowHost("host.x", "probe-2")
	if ref == nil {
		t.Fatal("a second probe must be refused while the first is in flight")
	}
	if ref.Code != core.CodeCircuitOpen {
		t.Fatalf("want circuit_open, got %s", ref.Code)
	}
	// ★ 理由必须点名**那个还在飞的探针** —— 「已经有一个探针了」不说是哪一个，
	// 读的人没法去查它。
	if !strings.Contains(ref.Reason, "probe-1") {
		t.Fatalf("the reason must name the probe in flight, got %q", ref.Reason)
	}
	if !strings.Contains(ref.Reason, "already in flight") {
		t.Fatalf("the reason must say why, got %q", ref.Reason)
	}
}

// 探针回来了 ⇒ 占位要还掉，否则一个**成功**的探针会让这个 host 在下一个冷却期
// 里再也放不出探针 —— 那是半开要避免的另一半。
func TestAFinishedProbeGivesTheSlotBack(t *testing.T) {
	s := halfOpenServer(t)
	if ref := s.allowHost("host.x", "probe-1"); ref != nil {
		t.Fatalf("the first probe must be admitted: %s", ref.Reason)
	}
	// ★ 别的 attempt 不能替 probe-1 还这个占位 —— 否则任何一个碰巧结束在
	// 这个 host 上的执行都能把它解掉，而那个占位说的是**那一个**探针。
	s.recordTrip("host.x", "ok", "someone-else")
	if _, held := s.breakers.probes["host.x"]; !held {
		t.Fatal("a different attempt must not release this host's probe reservation")
	}
	// ★ 而它自己回来时必须还掉，否则一个**成功**的探针会让这个 host 在下一个
	// 冷却期里再也放不出探针 —— 那是半开要避免的另一半。
	s.recordTrip("host.x", "ok", "probe-1")
	if _, held := s.breakers.probes["host.x"]; held {
		t.Fatal("the reservation must be released when the probe comes back")
	}
}

// 持有探针的执行崩了 ⇒ 占位必须自己过期，否则熔断器会永远卡在半开。
func TestAProbeThatNeverComesBackExpires(t *testing.T) {
	s := halfOpenServer(t)
	if ref := s.allowHost("host.x", "probe-1"); ref != nil {
		t.Fatalf("the first probe must be admitted: %s", ref.Reason)
	}
	// 手动把它的租约推到过去 —— 等价于「等过了 CooldownMS」。
	p := s.breakers.probes["host.x"]
	p.expires = time.Now().UnixMilli() - 1
	s.breakers.probes["host.x"] = p
	// ★ 而状态必须仍然是 half-open，否则这条测的是别的东西。
	if got := breaker.StateOf(s.breakers.trips, "host.x", time.Now().UnixMilli()); got != breaker.HalfOpen {
		t.Fatalf("precondition: want half-open, got %s", got)
	}
	if ref := s.allowHost("host.x", "probe-2"); ref != nil {
		t.Fatalf("an expired reservation must not block forever: %s", ref.Reason)
	}
}

// 反向对照：**开着的**（冷却没过）宿主仍然一律拒绝，与探针无关。
func TestAnOpenHostStillRefusesEveryAttempt(t *testing.T) {
	s := breakerServer(t)
	now := time.Now().UnixMilli()
	for i := 0; i < breaker.Threshold; i++ {
		trip(t, s, "host.x", now, false)
	}
	if got := breaker.StateOf(s.breakers.trips, "host.x", now); got != breaker.Open {
		t.Fatalf("precondition: want open, got %s", got)
	}
	for i := 0; i < 3; i++ {
		ref := s.allowHost("host.x", "a"+strconv.Itoa(i))
		if ref == nil {
			t.Fatal("an open host refuses every attempt, probes included")
		}
		if ref.Code != core.CodeCircuitOpen {
			t.Fatalf("want circuit_open, got %s", ref.Code)
		}
	}
}
