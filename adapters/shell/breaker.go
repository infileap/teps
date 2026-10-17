package main

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"teps/core"
	"teps/organs/breaker"
)

// The circuit breaker, projected from the chain like every other table here.
//
// A breaker kept in memory would forget every trip on restart, which is exactly
// the failure it exists to prevent: a host that has been failing for an hour
// would look brand new. So a trip is a record, and the live table is what you
// get by reading them.
//
// One decision is worth stating: **a trip is written when it changes something.**
// A host failure is always written — it is the fact the breaker is made of. A
// host success is written only when that host already has a run of failures on
// record, because that is the only moment a success changes the state. Writing
// every success would make the chain grow with the traffic rather than with the
// history.

type breakerRecord struct {
	Rec  string `json:"rec"`
	Host string `json:"host"`
	OK   bool   `json:"ok"`
	At   int64  `json:"at"`
}

type breakerStore struct {
	mu chan struct{}
	lg *core.Log

	trips   []breaker.Trip
	applied uint64

	// probes 是**正在飞的那一个探针**：host -> 它的尝试 id 与租约到期时刻。
	//
	// ★★★ 半开的探针必须是一份**租约**，不是一个状态读数。
	//
	// 此前 `allowHost` 只问 `breaker.Allow`，而它在半开时无条件回 nil ——
	// 于是「半开只放一个探针」这句话靠的是「第一个探针的失败会被写进 trips、
	// 从而把状态推回 open」这个**副作用**。串行地发请求时它恰好成立；并发时
	// 不成立。实测（三次失败 → 等 31 秒进入 half-open → 同时发 5 次）：
	//
	//   d7 放行 · d5 放行 · d4 circuit_open · d8 circuit_open · d6 放行
	//
	// **放行了三次，而 README 写着「半开只放一个探针」。**
	//
	// 而那个本该拦住它的函数 `organs/breaker/breaker.go` 的 `HalfOpenProbe`
	// 一个调用点都没有（Round 55 把它标成了 DEAD-API），它自己的注释逐字写着
	// 这件事的后果：
	//
	//   A half-open breaker that admitted every attempt would take the full
	//   traffic of every skill behind it on the first probe.
	//
	// 与算力池那条「容量是租约不是计数」是同一条：**一个「现在有几个」的问题，
	// 读状态是答不对的 —— 状态说你到了半开，不说那个探针已经派出去了。**
	probes map[string]probeLease
}

// probeLease 是一次半开探针的占位。
//
// 它有到期时刻，因为持有它的那个执行可能崩掉、也可能永远不回来 ——
// 一份永远不放的租约与一个永远开着的熔断器是同一种坏。到期时刻取
// `CooldownMS`：那与「再等一个冷却期」是同一件事，不需要第二个常数。
type probeLease struct {
	attempt string
	expires int64
}

func newBreakerStore(lg *core.Log) (*breakerStore, *core.Refusal) {
	st := &breakerStore{mu: make(chan struct{}, 1), lg: lg, probes: map[string]probeLease{}}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *breakerStore) lock()   { <-st.mu }
func (st *breakerStore) unlock() { st.mu <- struct{}{} }

func (st *breakerStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec breakerRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "breaker" {
		return nil
	}
	st.trips = append(st.trips, breaker.Trip{Host: rec.Host, OK: rec.OK, At: rec.At})
	return nil
}

func (st *breakerStore) append(rec breakerRecord, key string) (core.Entry, *core.Refusal) {
	canon, ref := core.Canonical(mustJSON(rec))
	if ref != nil {
		return core.Entry{}, ref
	}
	entry, ref := st.lg.Append(key, canon)
	if ref != nil {
		return core.Entry{}, ref
	}
	if entry.Seq > st.applied {
		st.absorb(entry)
	}
	return entry, nil
}

// consecutive counts back from the end, the same way the organ does.
func (st *breakerStore) consecutive(host string) int {
	n := 0
	for i := len(st.trips) - 1; i >= 0; i-- {
		if st.trips[i].Host != host {
			continue
		}
		if st.trips[i].OK {
			break
		}
		n++
	}
	return n
}

// allowHost refuses a spawn when the host's breaker is open.
//
// The host is the interpreter, because that is what actually fails. Two skills
// that share a broken interpreter share the breaker, which is the point: the
// thing that is broken is the thing that runs them.
func (s *server) allowHost(host, attempt string) *core.Refusal {
	if s.breakers == nil || host == "" {
		return nil
	}
	now := time.Now().UnixMilli()
	s.breakers.lock()
	defer s.breakers.unlock()

	// 过期的探针先还回去：持有它的执行可能崩了。
	for h, p := range s.breakers.probes {
		if p.expires <= now {
			delete(s.breakers.probes, h)
		}
	}
	// ★ 半开：**占位**，不是读状态。占不到就不放行。
	if breaker.StateOf(s.breakers.trips, host, now) == breaker.HalfOpen {
		if p, held := s.breakers.probes[host]; held {
			return core.Refuse(core.CodeCircuitOpen,
				"host "+host+" is half-open and a probe ("+p.attempt+") is already in flight: the "+
					"cooldown admits exactly one probe, and admitting the full traffic of every skill "+
					"behind this host is the thing half-open exists to avoid")
		}
		s.breakers.probes[host] = probeLease{attempt: attempt, expires: now + breaker.CooldownMS}
	}
	return breaker.Allow(s.breakers.trips, host, now)
}

// recordTrip writes the attempt's effect on the breaker.
//
// It is called after every spawn that got as far as a verdict. A host failure is
// always written; a success is written only when it is the one that resets a
// run, because that is the only time it changes the answer.
func (s *server) recordTrip(host, verdict, attempt string) {
	if s.breakers == nil || host == "" {
		return
	}
	ok := !breaker.IsHostFailure(verdict)
	s.breakers.lock()
	defer s.breakers.unlock()
	// ★ 探针回来了（无论成败）：把占位还掉。不还的话，一个成功的探针会让这个
	// host 在下一个冷却期里再也放不出探针 —— 而那正是半开要避免的另一半。
	if p, held := s.breakers.probes[host]; held && p.attempt == attempt {
		delete(s.breakers.probes, host)
	}
	if ok && s.breakers.consecutive(host) == 0 {
		return
	}
	s.breakers.append(breakerRecord{
		Rec: "breaker", Host: host, OK: ok, At: time.Now().UnixMilli(),
	}, attempt+keyTrip)
}

const keyTrip = ":trip"

func (s *server) handleBreaker(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "breaker.") {
		return false
	}
	switch f.T {
	case "breaker.state":
		s.breakerState(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) breakerState(f core.Frame, out *core.Frame) {
	now := time.Now().UnixMilli()
	s.breakers.lock()
	trips := append([]breaker.Trip(nil), s.breakers.trips...)
	// ★★★ 探针占位也要一起取出来 —— 它此前**一个字都没报**。
	//
	// Round 56 把半开的探针做成了一份**租约**（`s.breakers.probes`），
	// `allowHost` 在占不到位时拒绝。而 `breaker.state` 留在原地。实测
	// （同一个宿主 `/bin/sh`，预算 800ms、脚本 sleep 5）：
	//
	//   ② 冷却过去、还没放探针： [{"host":"/bin/sh","state":"half-open"}]
	//   ③ 探针正在飞：           [{"host":"/bin/sh","state":"half-open"}]   ← 与 ② **逐字相同**
	//   ③b 而此刻另一次尝试：      err=circuit_open
	//      "host /bin/sh is half-open and a probe (p1) is already in flight: …"
	//
	// **同一个回答，两件相反的事**：② 里槽是空的（新尝试会被放行），③ 里槽被占了
	// （新尝试会被拒）。而 `breaker.state` 的 `basis` 那句逐字写着
	// 「half-open admits exactly one probe」—— **一个解释规则的帧，看不出规则此刻
	// 有没有被用上。**
	//
	// ★ 这与 Round 45 的 `open` 数 `st != Closed` 是同一条（一个名字读作 A 而
	// 值是 B），也与这一整轮的「补一扇门不叫修好一个缺陷」是同一条。
	probes := make(map[string]probeLease, len(s.breakers.probes))
	for h, p := range s.breakers.probes {
		probes[h] = p
	}
	s.breakers.unlock()

	rows := make([]map[string]any, 0)
	open, halfOpen, closed := 0, 0, 0
	inFlight := 0
	for _, h := range breaker.Hosts(trips) {
		st := breaker.StateOf(trips, h, now)
		// ★★★ 三个状态、三个计数。
		//
		// 此前这里是 `if st != breaker.Closed { open++ }` —— 一个叫 `open` 的数
		// 数的是**不是闭的**，也就是 `open + half-open`。实测（同一个宿主，
		// 三次失败之后）：
		//
		//   冷却 30s 内  -> {"open":1, hosts:[{"state":"open"}]}
		//   冷却过去之后 -> {"open":1, hosts:[{"state":"half-open"}]}
		//
		// **同一个 1，两个状态。** 而这两个状态在操作上是相反的：`open` 拒绝一切，
		// `half-open` **放一个探针进去**。一个读 `open: 1` 以为「不会有东西被尝试」
		// 的人，在第二种情况下正好读反了。
		switch st {
		case breaker.Open:
			open++
		case breaker.HalfOpen:
			halfOpen++
		default:
			closed++
		}
		row := map[string]any{"host": h, "state": string(st)}
		if st == breaker.Open {
			if ref := breaker.Allow(trips, h, now); ref != nil {
				row["reason"] = ref.Reason
			}
		}
		// ★ 半开的宿主必须报出「那一个探针现在在不在飞」。
		if st == breaker.HalfOpen {
			if p, held := probes[h]; held && p.expires > now {
				inFlight++
				row["probe_in_flight"] = true
				row["probe_attempt"] = p.attempt
				row["probe_expires_at"] = p.expires
			} else {
				// ★ 槽是空的 —— 这与「有探针在飞」是**相反**的两件事，
				// 而它们此前在回答里长得一样。
				row["probe_in_flight"] = false
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["host"].(string) < rows[j]["host"].(string) })
	out.Body = mustJSON(map[string]any{
		"hosts": rows,
		// ★ 三个状态三个计数。`open` 只数真的开着的那些；半开的**不是**开着的 ——
		// 它正在放一个探针，那是这一层唯一在做的事。
		"open":        open,
		"half_open":   halfOpen,
		"closed":      closed,
		"hosts_total": len(rows),
		// ★ 而半开的那几个里，有几个**此刻正拿着那一个探针**。
		// `half_open: 1` 说的是「有一个宿主在半开」，`probes_in_flight` 说的是
		// 「那一个探针已经派出去了」—— 两件事两个数。
		"probes_in_flight": inFlight,
		// The two numbers the state is made of, reported next to it so a reader
		// can check the state rather than take it.
		"threshold":   breaker.Threshold,
		"cooldown_ms": breaker.CooldownMS,
		"trips":       len(trips),
		// 一句话说清这三个计数与 hosts 里那些 state 是同一批事实：
		// 否则读的人会把 `open` 当成「有多少宿主不闭」。
		"basis": "open / half_open / closed partition the hosts in this answer, one host each: " +
			"open refuses every attempt, half-open admits exactly one probe, closed admits all. " +
			"half-open is not open — a count named open that also counted half-open would " +
			"read as \"nothing will be tried\" at the one moment something is being tried",
	})
}
