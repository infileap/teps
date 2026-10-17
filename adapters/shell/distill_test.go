package main

import (
	"encoding/json"
	"strings"
	"testing"

	"teps/core"
)

// ★★★ note 必须由状态推出来，不能是一句常量。
//
// 此前 `distillApply` 无条件回同一句话：「established, not confirmed: it will
// answer only after the same stimulus is seen working once more」。而蒸馏**同一个
// 刺激两次**会让 `applyRecord` 对已有的那一行再 `Reinforce` 一次：
// `0.3 + 0.2 = 0.5` 变成 `0.7`，越过 `FireThreshold = 0.6`。
//
// 实测第二次蒸馏的同一份 JSON：
//
//	{"strength": 0.7, "fires": true,
//	 "note": "established, not confirmed: it will answer only after the same
//	          stimulus is seen working once more"}
//
// —— **`fires: true` 旁边放着一句「它现在还不会回答」。** 与 Round 41 那个
// `fires: false` 配 `firings: 1` 是同一个形状：同一份 JSON 里两个字段说的是
// 相反的事。
//
// 而 `drive.sh` 那条断言（`expect "★ 现在它会回答了" "$r" '"fires":true'`）
// 只查了 `fires`，**从来没有查过那句话** —— 一句话只要不随状态变，就没有
// 任何东西会让它变红。
func distillServer(t *testing.T) *server {
	t.Helper()
	s := testServer(t)
	rs, ref := newReflexStore(s.log)
	if ref != nil {
		t.Fatalf("open reflex store: %s", ref.Reason)
	}
	s.reflexes = rs
	return s
}

func distillOnce(t *testing.T, s *server, key, taskID string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"task_id": taskID, "stimulus": "translate this paragraph",
		"layer": "full", "outcome": "ok", "action": "translate.run",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out core.Frame
	s.distillApply(core.Frame{T: "distill.apply", V: 1, Key: key, Body: body}, &out)
	if out.Err != nil {
		t.Fatalf("distill.apply refused: %s", out.Err.Reason)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestTheDistillNoteFollowsTheState(t *testing.T) {
	s := distillServer(t)

	first := distillOnce(t, s, "k1", "t1")
	if first["fires"] != false {
		t.Fatalf("a freshly distilled rule is below the threshold, got fires=%v strength=%v",
			first["fires"], first["strength"])
	}
	firstNote, _ := first["note"].(string)
	if !strings.Contains(firstNote, "not confirmed") {
		t.Fatalf("a candidate must say it is a candidate, got %q", firstNote)
	}

	second := distillOnce(t, s, "k2", "t2")
	if second["fires"] != true {
		t.Fatalf("the second distillation crosses the threshold, got fires=%v strength=%v",
			second["fires"], second["strength"])
	}
	secondNote, _ := second["note"].(string)
	// ★ 这条断言才是这一轮的东西：**同一份 JSON 里两个字段不许说相反的事。**
	if strings.Contains(secondNote, "not confirmed") {
		t.Fatalf("fires is true and the note says it is not confirmed yet: %q", secondNote)
	}
	if !strings.Contains(secondNote, "confirmed") {
		t.Fatalf("the note must say it is confirmed now, got %q", secondNote)
	}
	if firstNote == secondNote {
		t.Fatal("these are two different states, so the note must differ")
	}
}

// ★ 阈值也要报出来：没有它，`strength: 0.7` 与 `fires: true` 之间的那条线
// 只活在读的人的印象里。
func TestTheDistillAnswerReportsTheThreshold(t *testing.T) {
	s := distillServer(t)
	got := distillOnce(t, s, "k1", "t1")
	if got["threshold"] != 0.6 {
		t.Fatalf("threshold = %v, want 0.6", got["threshold"])
	}
	// ★ 而且它与 fires 必须一致 —— 报出来的那条线要真的在判。
	above := got["strength"].(float64) >= got["threshold"].(float64)
	if above != got["fires"].(bool) {
		t.Fatalf("strength %v vs threshold %v says fires=%v, but the answer says %v",
			got["strength"], got["threshold"], above, got["fires"])
	}
}
