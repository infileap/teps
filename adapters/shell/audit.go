package main

import (
	"encoding/json"
	"strings"

	"teps/core"
)

// audit.tail exposes the chain itself, not a summary of it.
//
// The contract has promised this frame since the contract was written, and the
// frame guard has been reporting it as "documented but nobody dispatches it"
// since the guard existed. A promise nobody implements is worse than no
// promise: a client writes against it and finds out at runtime.
//
// It is also the only frame that answers "what actually happened" without
// asking any organ. Every other op reports a projection — sessions, memory,
// posteriors — and every projection is somebody's reading. This one returns the
// records those readings are readings of.

type tailEntry struct {
	Seq  uint64          `json:"seq"`
	Key  string          `json:"key"`
	Prev string          `json:"prev"`
	Head string          `json:"head"`
	Body json.RawMessage `json:"body"`
}

// handleAudit dispatches audit.* frames.
func (s *server) handleAudit(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "audit.") {
		return false
	}
	switch f.T {
	case "audit.tail":
		s.auditTail(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) auditTail(f core.Frame, out *core.Frame) {
	var body struct {
		Limit int `json:"limit"`
	}
	// decodeBody refuses an absent body, which is right for an op that needs
	// fields. Every field here is optional, so requiring a body would make
	// "all fields are optional" a claim the API contradicts — and clients would
	// send {"limit":0} forever to satisfy it.
	if len(f.Body) > 0 {
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return
		}
	}
	// A default limit is not a silent decision: reading a tail without saying
	// how much of it is a request whose size depends on who is asking.
	if body.Limit == 0 {
		body.Limit = 20
	}
	entries, ref := s.log.Tail(body.Limit)
	if ref != nil {
		out.Err = ref
		return
	}
	rows := make([]tailEntry, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, tailEntry{Seq: e.Seq, Key: e.Key, Prev: e.Prev, Head: e.Head, Body: e.Body})
	}
	head, seq := s.log.Head()
	// ★★★ 一个窗口必须报出它的窗口。
	//
	// 此前这里只回 `entries` / `head` / `seq` / `epoch`。链上有 25 条、没传
	// limit 时它回 20 条，而回答里**没有任何一处说这件事**：调用方分不出
	// 「这 20 条是我要的」还是「服务端替我决定的」，也分不出「这就是链」还是
	// 「这是链的尾巴」。`seq` 是链头序号，它在那里，但它不回答这个问题 ——
	// 一个 25 与一个 20 并排出现，读的人得自己做减法，而**它本来就是要被
	// 说出来的那个事实**。
	//
	// 与 `metrics.accuracy` 报 `traced`/`chains_run`/`unrun`、与 `waste` 报
	// 它的两个计数是同一条：**一个数是某个窗口里的数时，窗口是它的一部分。**
	//
	// 那句注释「A default limit is not a silent decision」此前只在代码里成立：
	// 默认值确实是被**做出**的，只是**没有被说出去**。而 drive.sh 那条断言
	// 的标签写着「没给 limit 时用默认值，不是无声地全给」，它查的却是
	// `len(entries) == 20` —— **一条查行数的断言，配了一句承诺报默认值的
	// 标签**。两件事，两句话。
	first, last := uint64(0), uint64(0)
	if len(rows) > 0 {
		first, last = rows[0].Seq, rows[len(rows)-1].Seq
	}
	out.Body = mustJSON(map[string]any{
		"entries": rows,
		// 实际生效的 limit：0 表示请求没给，服务端用了默认值。它必须出现，
		// 否则「默认值不是无声的决定」这句话在回答里是假的。
		"limit": body.Limit,
		// 这一窗口里有多少条。与 `len(entries)` 是同一个数，但它在这里是
		// **被报出来的**，而不是要调用方自己去数一个数组。
		"returned": len(rows),
		// 窗口的两端。有了它们，调用方可以接着往回翻，而不必猜。
		"first_seq": first,
		"last_seq":  last,
		// ★ 链比窗口长 ⇒ 这是一条尾巴，不是整条链。
		"truncated": seq > uint64(len(rows)),
		"head":      head,
		"seq":       seq,
		"epoch":     s.log.Epoch(),
		"basis": "entries is a window over the chain, not the chain: limit is the limit " +
			"that was applied (a request that omits it gets the server default), returned " +
			"is how many records are in this window, first_seq and last_seq are its ends, " +
			"and truncated says whether the chain is longer than the window",
	})
}
