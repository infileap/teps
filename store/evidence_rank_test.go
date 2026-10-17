package store

import (
	"testing"
)

// ★★★ 一个前向器把它所前向的那个函数、写在注释里的唯一一条禁令，恰好执行反了。
//
// `evidence.Rank` 的注释逐字写着：
//
//	An unknown level is deliberately **not** mapped to zero. Zero is a real level
//	(E0), and folding "E9" onto it would turn a typo into a trust decision.
//
// 而 `store/manifest.go` 的 `evidenceRank` 此前是 `r, _ := evidence.Rank(l)` ——
// 丢掉 `ok`，于是未知等级落到 0，也就是 E0 的秩。
//
// 它今天还不可达（`Manifest.Validate` 拦住未知的声明值，孩子又都过了
// `Validate`），但一个「读起来是 A、底下是 B」的转发器不需要等到可达才算错：
// 下一个人加一条旁路，它就活了。而且它与 `adapters/shell/knowledge.go` 的
// `compose.build` 是**同一个错**——那边是可达到的，同一个 E9 在那里被静默当成 E0。
func TestEvidenceRankReportsWhetherTheLevelIsOnTheLadder(t *testing.T) {
	if r, ok := evidenceRank(E4); !ok || r != 4 {
		t.Fatalf("E4 = (%d, %v), want (4, true)", r, ok)
	}
	if r, ok := evidenceRank(E0); !ok || r != 0 {
		t.Fatalf("E0 = (%d, %v), want (0, true)", r, ok)
	}
	// ★ 未知必须**不是** E0 的秩。`ok` 是那个区分。
	r, ok := evidenceRank(Evidence("E9"))
	if ok {
		t.Fatal("E9 is not on the ladder, so ok must be false")
	}
	if r != 0 {
		t.Fatalf("Rank returns zero for an unknown level as a placeholder, got %d", r)
	}
	// 这条断言的要点不是「r 等于几」，而是**调用方拿不到可以当秩用的东西**：
	// 一个只读第一个返回值的调用方会把 E9 当成 E0。
	if _, ok := evidenceRank(Evidence("")); ok {
		t.Fatal("an empty level is not on the ladder either")
	}
}

// 反向对照：真的 E0 与未知**必须**能被分开，否则上面那条断言只是在说
// 「函数返回了 false」。
func TestAKnownE0IsDistinguishableFromAnUnreadableLevel(t *testing.T) {
	_, known := evidenceRank(E0)
	_, unknown := evidenceRank(Evidence("E9"))
	if known == unknown {
		t.Fatal("E0 and E9 must not be distinguishable by rank alone — that is the whole bug")
	}
}
