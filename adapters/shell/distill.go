package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"teps/core"
	"teps/organs/distill"
	"teps/organs/reflex"
)

// distill.apply turns a completed experience into a reflex rule.
//
// This is the third of the design note's four distillation paths — L1 → the
// reflex network — and it is the one that can be built today, because it needs
// no model: the experience is already on the chain, and the artifact is a rule.
//
// # The loop it closes
//
// A stimulus arrives, the reflex layer will not take it, the full path answers
// it and succeeds. That success is an experience worth keeping. Distilling it
// establishes a rule — **below the firing threshold**, because the reflex layer
// already has the right mechanism for candidates: establish, then confirm.
//
// So the second arrival still pays for a context. The third does not. The
// intercept rate rises because something was learned, not because a number was
// set.
//
// # Why the rule id is derived from the normalised stimulus
//
// Two spellings of the same stimulus must land on the same rule. Deriving the id
// from the raw text would create a twin rule for "What  TIME is it?" and
// "what time is it" — and since the reflex layer matches on the normalised form,
// both would match, and the strength of one experience would be split across two
// rows.
//
// The normalisation belongs to the reflex layer, so this file asks it rather
// than repeating it. The adapter is where two organs are allowed to meet.
func ruleIDFor(stimulus string) string {
	sum := sha256.Sum256([]byte(reflex.Normalise(stimulus)))
	return "d-" + hex.EncodeToString(sum[:])[:12]
}

// handleDistill dispatches distill.* frames.
func (s *server) handleDistill(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "distill.") {
		return false
	}
	if s.reflexes == nil {
		out.Err = core.Refuse(core.CodeBadRequest, "no reflex table is configured on this server, so there is nowhere to distil to")
		return true
	}
	if f.T != "distill.apply" {
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
		return true
	}
	s.distillApply(f, out)
	return true
}

func (s *server) distillApply(f core.Frame, out *core.Frame) {
	var body struct {
		TaskID   string `json:"task_id"`
		Stimulus string `json:"stimulus"`
		Layer    string `json:"layer"`
		Outcome  string `json:"outcome"`
		Action   string `json:"action"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	trace := distill.Trace{
		TaskID: body.TaskID, Stimulus: body.Stimulus,
		Layer: body.Layer, Outcome: body.Outcome, Action: body.Action,
	}
	prop, ref := distill.Propose(trace)
	if ref != nil {
		out.Err = ref
		return
	}

	rec := reflexRecord{
		Rec: "reflex", ID: ruleIDFor(prop.Stimulus),
		Stimulus: prop.Stimulus, Action: prop.Action, Outcome: "ok",
	}
	s.reflexes.lock()
	// The shape check runs against a copy of the real table, never a fresh one:
	// a rule distilled twice must land on the existing row, and an empty table
	// could not tell the difference.
	probe := s.reflexes.clone()
	if ref := probe.applyRecord(rec); ref != nil {
		s.reflexes.unlock()
		out.Err = ref
		return
	}
	entry, ref := s.reflexes.commit(rec, f.Key)
	strength, fires := 0.0, false
	for _, r := range s.reflexes.rules {
		if r.ID == rec.ID {
			strength, fires = r.Strength, reflex.Fires(r)
		}
	}
	s.reflexes.unlock()
	if ref != nil {
		out.Err = ref
		return
	}
	// ★★★ note 必须由状态推出来，不能是一句常量。
	//
	// 此前它无条件写着「established, not confirmed: it will answer only after
	// the same stimulus is seen working once more」。而蒸馏**同一个刺激两次**
	// 会让 `applyRecord` 对已有的那一行再 `Reinforce` 一次：`0.3 + 0.2 = 0.5`
	// 变成 `0.7`，越过 `FireThreshold = 0.6`。实测第二次蒸馏的同一份 JSON：
	//
	//   {"strength": 0.7, "fires": true,
	//    "note": "established, not confirmed: it will answer only after the same
	//             stimulus is seen working once more"}
	//
	// **`fires: true` 旁边放着一句「它现在还不会回答」。** 与 Round 41 那个
	// `fires: false` 配 `firings: 1` 是同一个形状：同一份 JSON 里两个字段
	// 说的是相反的事。
	//
	// 而 `drive.sh` 那条断言（`expect "★ 现在它会回答了" "$r" '"fires":true'`）
	// 只查了 `fires`，**从来没有查过那句话** —— 一句话只要不随状态变，就没有
	// 任何东西会让它变红。
	note := "established, not confirmed: it will answer only after the same stimulus is seen working once more"
	if fires {
		note = "confirmed: this stimulus has now been seen working twice, so the rule answers with no context at all. Distilling it again would only raise its strength"
	}
	out.Seq, out.Rev = entry.Seq, entry.Seq
	out.Body = mustJSON(map[string]any{
		"proposal": prop, "rule_id": rec.ID,
		"strength": round(strength),
		// Reported so the caller can see the point: a distilled rule is a
		// candidate, and a candidate that already answers would be a promotion.
		"fires": fires,
		// ★ 阈值也在：没有它，`strength: 0.7` 与 `fires: true` 之间的那条线
		// 只活在读的人的印象里。
		"threshold": reflex.FireThreshold,
		"note":      note,
		"head":      entry.Head,
	})
}
