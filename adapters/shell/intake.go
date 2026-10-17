package main

import (
	"encoding/json"
	"strings"

	"teps/core"
	"teps/organs/intake"
	"teps/organs/reflex"
)

// task.intake is the front door.
//
// It exists for one reason: the design note claims the reflex layer answers
// 60–80% of requests with no context, and until requests are *recorded* there is
// no denominator, so the claim cannot be checked. An arrival that is not
// recorded is an arrival that did not happen as far as the rate is concerned.
//
// The routing policy lives in organs/intake, not here. This file only adapts the
// reflex table to the interface that policy asks for, so the router never learns
// what a reflex rule is.

type taskRecord struct {
	Rec      string `json:"rec"`
	TaskID   string `json:"task_id"`
	Stimulus string `json:"stimulus"`
	Layer    string `json:"layer"`
	By       string `json:"by,omitempty"`
	Action   string `json:"action,omitempty"`
}

type taskStore struct {
	mu      chan struct{}
	log     *core.Log
	records []intake.Record
	applied uint64
}

func newTaskStore(lg *core.Log) (*taskStore, *core.Refusal) {
	st := &taskStore{mu: make(chan struct{}, 1), log: lg}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *taskStore) lock()   { <-st.mu }
func (st *taskStore) unlock() { st.mu <- struct{}{} }

func (st *taskStore) absorb(e core.Entry) error {
	st.applied = e.Seq
	var rec taskRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "task" {
		return nil
	}
	st.records = append(st.records, intake.Record{
		TaskID: rec.TaskID, Stimulus: rec.Stimulus,
		Layer: intake.Layer(rec.Layer), By: rec.By,
	})
	return nil
}

func (st *taskStore) commit(rec taskRecord, key string) (core.Entry, *core.Refusal) {
	canon, ref := core.Canonical(mustJSON(rec))
	if ref != nil {
		return core.Entry{}, ref
	}
	entry, ref := st.log.Append(key, canon)
	if ref != nil {
		return core.Entry{}, ref
	}
	if entry.Seq > st.applied {
		st.absorb(entry)
	}
	return entry, nil
}

// reflexLayer adapts the reflex table to intake.Answerer.
//
// The adapter is where two organs are allowed to meet: the router asks "will you
// answer this with no context", and this type is the only place that knows the
// answer comes from a rule table.
type reflexLayer struct{ rules []reflex.Rule }

func (reflexLayer) Name() string { return string(intake.LayerReflex) }

func (r reflexLayer) Answer(stimulus string) (string, bool) {
	rule, ok := reflex.Match(stimulus, r.rules)
	if !ok {
		return "", false
	}
	return rule.Action, true
}

// handleIntake dispatches task.* frames.
func (s *server) handleIntake(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "task.") {
		return false
	}
	if s.tasks == nil {
		out.Err = core.Refuse(core.CodeBadRequest, "no task intake is configured on this server")
		return true
	}
	switch f.T {
	case "task.intake":
		s.taskIntake(f, out)
	case "task.stats":
		s.taskStats(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) taskIntake(f core.Frame, out *core.Frame) {
	var body struct {
		TaskID   string `json:"task_id"`
		Stimulus string `json:"stimulus"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	rec := intake.Record{TaskID: body.TaskID, Stimulus: body.Stimulus}
	if ref := intake.Validate(rec); ref != nil {
		out.Err = ref
		return
	}

	// Ask the reflex layer, under the lock, because the rule table is shared.
	s.reflexes.lock()
	decision := intake.Route(body.Stimulus, []intake.Answerer{reflexLayer{rules: s.reflexes.rules}})
	s.reflexes.unlock()

	rec.Layer = decision.Layer
	rec.By = decision.By
	entry, ref := s.tasks.commit(taskRecord{
		Rec: "task", TaskID: rec.TaskID, Stimulus: rec.Stimulus,
		Layer: string(rec.Layer), By: rec.By, Action: decision.Action,
	}, f.Key)
	if ref != nil {
		out.Err = ref
		return
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{
		"task_id": rec.TaskID, "decision": decision,
		"head": entry.Head, "stats": s.tasks.summarise(),
	})
}

// taskRecentLimit 是 task.stats 的 recent 窗口。它是一个**被报出来**的上限：
// 一个不说的上限与没有上限在回答里长得一样。
const taskRecentLimit = 20

func (s *server) taskStats(f core.Frame, out *core.Frame) {
	s.tasks.lock()
	stats := s.tasks.summarise()
	recent := make([]intake.Record, 0, len(s.tasks.records))
	for _, r := range s.tasks.records {
		recent = append(recent, r)
	}
	s.tasks.unlock()
	// ★★★ 一个被截断的列表必须说它被截断了。
	//
	// 此前这里是 `if len(recent) > 20 { recent = recent[len(recent)-20:] }`
	// 然后只回 `{"stats": stats, "recent": recent}`。实测（取进 25 个任务）：
	//
	//   {"recent":[…20 条…], "stats":{"total":25,"by_layer":{"full":25},…}}
	//
	// `total` 是 25，`recent` 有 20 条，而**没有一处说这件事**；也没有一处说
	// 那 20 条是**最新**的（`t6`…`t25`）而不是最早的。读的人只能做减法，
	// 还得猜方向。
	//
	// 这与 `audit.tail` 那一轮是同一条（那里现在报
	// `limit`/`returned`/`first_seq`/`last_seq`/`truncated`/`basis`），
	// 只是换了一处：**一个数的窗口是它的一部分。**
	limit := taskRecentLimit
	if len(recent) > limit {
		recent = recent[len(recent)-limit:]
	}
	body := map[string]any{
		"stats": stats, "recent": recent,
		// ★ 窗口本身：上限、实际给了几条。
		"limit": limit, "returned": len(recent),
		// ★ 被截断了吗。
		"truncated": len(recent) < stats.Total,
		"basis": "recent is the newest arrivals, oldest first within the window; " +
			"a truncated list means stats.total counts more arrivals than recent holds",
	}
	// ★ 端点必须点名，否则「那 20 条是哪 20 条」只能靠数。
	if len(recent) > 0 {
		body["first_task_id"] = recent[0].TaskID
		body["last_task_id"] = recent[len(recent)-1].TaskID
	}
	out.Body = mustJSON(body)
}

func (st *taskStore) summarise() intake.Stats {
	return intake.Summarise(st.records)
}
