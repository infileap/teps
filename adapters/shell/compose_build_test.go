package main

import (
	"encoding/json"
	"strings"
	"testing"

	"teps/core"
)

// ★★★ 梯子上没有的证据等级必须被**拒**，不许被折到 E0。
//
// 此前 `handleCompose` 是
//
//	rank := 0.0
//	if r, ok := evidence.Rank(evidence.Level(c.Evidence)); ok {
//	    rank = float64(r) / 4.0
//	}
//
// —— `ok` 被检查了，然后**未知的那一种照样落到 0.0**，也就是 E0。
// 而 `evidence.Rank` 的注释逐字写着那件事不许发生：
//
//	An unknown level is deliberately **not** mapped to zero. Zero is a real
//	level (E0), and folding "E9" onto it would turn a typo into a trust decision.
//
// 实测（两条只有 evidence 不同的候选，预算只够留一条）：
//
//	a=E4 b=E0 -> kept=["a"]      a=E0 b=E4 -> kept=["b"]
//	a=E4 b=E9 -> kept=["a"]      a=E9 b=E4 -> kept=["b"]
//
// 第二行与第一行逐字相同：`E9` 与 `E0` 在这里没有区别。而同一份代码库里
// 另一扇门不这样：`memory.retrieve` 对 `min_evidence:"E9"` 回 `bad_request`。
// **一个事实两扇门两个答案**，而这里是那个更糟的答案：它不报错，它按最弱的
// 等级静默处理。
func buildBody(t *testing.T, evA, evB string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"class":  "task_exec",
		"budget": 40,
		"candidates": []map[string]any{
			{"id": "a", "section": "retrieval", "text": strings.Repeat("a", 10),
				"similarity": 1, "evidence": evA, "recency": 1, "task_relevance": 1},
			{"id": "b", "section": "retrieval", "text": strings.Repeat("z", 10),
				"similarity": 1, "evidence": evB, "recency": 1, "task_relevance": 1},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func buildOnce(t *testing.T, evA, evB string) *core.Frame {
	t.Helper()
	s := &server{}
	var out core.Frame
	s.handleCompose(core.Frame{T: "compose.build", V: 1, Body: buildBody(t, evA, evB)}, &out)
	return &out
}

func TestAnUnreadableEvidenceLevelIsRefusedNotFoldedOntoE0(t *testing.T) {
	out := buildOnce(t, "E4", "E9")
	if out.Err == nil {
		t.Fatal("E9 is not on the ladder, so this must be refused rather than read as E0")
	}
	if out.Err.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", out.Err.Code)
	}
	for _, want := range []string{"candidate 1 (b)", "E9", "not a level on the ladder", "turn a typo into a trust decision"} {
		if !strings.Contains(out.Err.Reason, want) {
			t.Fatalf("the reason must contain %q, got %q", want, out.Err.Reason)
		}
	}
}

// ★ 反向对照：两个都在梯子上时，等级**真的**在决定答案。
// 没有这一条，上面那条可以被「反正永远拒」满足。
func TestTheLadderStillDecidesBetweenTwoRealLevels(t *testing.T) {
	hi := buildOnce(t, "E4", "E0")
	if hi.Err != nil {
		t.Fatalf("E4/E0 are both on the ladder: %s", hi.Err.Reason)
	}
	lo := buildOnce(t, "E0", "E4")
	if lo.Err != nil {
		t.Fatalf("E0/E4 are both on the ladder: %s", lo.Err.Reason)
	}
	var a, b map[string]any
	if err := json.Unmarshal(hi.Body, &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := json.Unmarshal(lo.Body, &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	keptA := a["kept"].([]any)
	keptB := b["kept"].([]any)
	if len(keptA) != 1 || keptA[0] != "a" {
		t.Fatalf("a is E4, so it must be the one kept, got %v", keptA)
	}
	if len(keptB) != 1 || keptB[0] != "b" {
		t.Fatalf("b is E4, so it must be the one kept, got %v", keptB)
	}
	// ★ 这两次的结果必须**不同** —— 如果等级被折成同一个值，两次会给出同一个
	// 答案（由 id 或输入顺序决定），而那条断言才是「等级真的在起作用」。
	if keptA[0] == keptB[0] {
		t.Fatalf("swapping which candidate is E4 must swap the answer, got %v twice", keptA)
	}
}

// 空字符串不是梯子上的一级。
func TestAnEmptyEvidenceLevelIsRefusedToo(t *testing.T) {
	out := buildOnce(t, "E1", "")
	if out.Err == nil {
		t.Fatal("an absent evidence level is not E0")
	}
	if !strings.Contains(out.Err.Reason, "not a level on the ladder") {
		t.Fatalf("got %q", out.Err.Reason)
	}
}
