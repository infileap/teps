package main

import (
	"fmt"
	"strings"
	"testing"

	"teps/organs/executor"

	"teps/evidence"
	"teps/store"
)

// expandChildren 的深度上限在 curl 层面测不到（要套九层组合），
// 所以它在这里测：它是唯一一条「拒绝的理由是资源而不是正确性」的规则，
// 而没有被测过的界限只是注释。
func TestExpansionRefusesPastTheDepthBound(t *testing.T) {
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	srv := &server{skills: st}

	leaf := registerLeaf(t, st)
	prev := leaf
	for i := 0; i < maxComposeDepth+2; i++ {
		m := &store.Manifest{
			SkillID:    fmt.Sprintf("nest.%d", i),
			Version:    "1.0.0",
			Kind:       store.KindCompose,
			Evidence:   evidence.L1,
			License:    "MIT",
			Provenance: store.Provenance{Source: "test"},
			Deps:       []string{prev},
			Compose:    &store.Composition{Op: store.OpSerial, Children: []string{prev}},
		}
		res, ref := st.Register(store.Registration{Manifest: m, Files: map[string][]byte{}})
		if ref != nil {
			t.Fatalf("layer %d must register: %s", i, ref.Reason)
		}
		prev = res.Ref
	}

	_, ref := srv.expandChildren([]string{prev}, 0)
	if ref == nil {
		t.Fatal("nesting past the bound must be refused")
	}
	if !strings.Contains(ref.Reason, "nested deeper") {
		t.Fatalf("want the resource bound to be named, got %q", ref.Reason)
	}
	// 拒绝理由必须说清这是资源账而不是环检测：
	// 环早已不可能，因为 manifest 装的是孩子的包哈希。
	if !strings.Contains(ref.Reason, "already impossible") {
		t.Fatalf("the reason must not read like a cycle check, got %q", ref.Reason)
	}
}

// 浅一层必须仍然过，否则上面那条测的只是「所有嵌套都被拒」。
func TestShallowNestingStillExpands(t *testing.T) {
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	srv := &server{skills: st}
	leaf := registerLeaf(t, st)

	inner := &store.Manifest{
		SkillID: "in", Version: "1.0.0", Kind: store.KindCompose,
		Evidence: evidence.L1, License: "MIT",
		Provenance: store.Provenance{Source: "test"},
		Deps:       []string{leaf},
		Compose:    &store.Composition{Op: store.OpSerial, Children: []string{leaf}},
	}
	innerRes, ref := st.Register(store.Registration{Manifest: inner, Files: map[string][]byte{}})
	if ref != nil {
		t.Fatalf("inner: %s", ref.Reason)
	}

	got, ref := srv.expandChildren([]string{innerRes.Ref}, 0)
	if ref != nil {
		t.Fatalf("one level of nesting must expand: %s", ref.Reason)
	}
	if len(got) != 1 || got[0] != leaf {
		t.Fatalf("want [%s], got %v", leaf, got)
	}
}

// 并行组合的产物是集合不是流，所以它不能顶替单步。
func TestAParallelChildIsRefusedByName(t *testing.T) {
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	srv := &server{skills: st}
	a := registerLeaf(t, st)
	b := registerLeafNamed(t, st, "second")

	par := &store.Manifest{
		SkillID: "par", Version: "1.0.0", Kind: store.KindCompose,
		Evidence: evidence.L1, License: "MIT",
		Provenance: store.Provenance{Source: "test"},
		Deps:       []string{a, b},
		Compose:    &store.Composition{Op: store.OpParallel, Children: []string{a, b}},
	}
	res, ref := st.Register(store.Registration{Manifest: par, Files: map[string][]byte{}})
	if ref != nil {
		t.Fatalf("parallel: %s", ref.Reason)
	}
	_, ref = srv.expandChildren([]string{res.Ref}, 0)
	if ref == nil {
		t.Fatal("a parallel composition cannot stand in for a single step")
	}
	if !strings.Contains(ref.Reason, "silent data loss") {
		t.Fatalf("the reason must say what the risk is, got %q", ref.Reason)
	}
}

// ---- helpers ----

func registerLeaf(t *testing.T, st *store.Store) string {
	t.Helper()
	return registerLeafNamed(t, st, "leaf")
}

func registerLeafNamed(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	m := &store.Manifest{
		SkillID:    "script." + id,
		Version:    "1.0.0",
		Kind:       store.KindScript,
		Evidence:   evidence.L1,
		License:    "MIT",
		Provenance: store.Provenance{Source: "test"},
		Env:        store.Env{Interpreter: "/bin/sh"},
		Verify:     []string{"exit == 0"},
	}
	res, ref := st.Register(store.Registration{
		Manifest: m,
		Files:    map[string][]byte{"run.sh": []byte("exit 0\n")},
	})
	if ref != nil {
		t.Fatalf("leaf %s: %s", id, ref.Reason)
	}
	return res.Ref
}

// ★ A composition inherits its two-sidedness from its children.
//
// A composition has no verify_assert of its own — the contract forbids one,
// because its judgement is a composition of its children's judgements. So the
// only thing that could have been shown to be able to fail is the chain, and a
// chain containing a one-sided child has never been shown that either.
//
// One one-sided child is enough, and the refusal has to name it: "some child is
// not good enough" is not actionable.
func TestOneOneSidedChildBlocksACompositionsElevation(t *testing.T) {
	s := testServer(t)
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	s.skills = st
	childA := registerTestChild(t, st, "child.a")
	childB := registerTestChild(t, st, "child.b")

	self := &store.Manifest{SkillID: "comb.two", Version: "1", Kind: store.KindCompose,
		Evidence: evidence.L0, License: "MIT",
		Provenance: store.Provenance{Source: "test"},
		Deps:       []string{childA, childB},
		Compose:    &store.Composition{Op: "serial", Children: []string{childA, childB}}}

	// Every child two-sided: the chain verified, so it reaches its ceiling.
	e := s.elevateCompose(self, evidence.L1, executor.VerdictOK, nil, "att-a")
	// To is set only by an elevation that happened: a refusal leaves it empty,
	// so this is the assertion that separates "it refused" from "it worked".
	if e.To != string(evidence.L1) || e.Ref == "" {
		t.Fatalf("a chain of two-sided children must be elevatable, got to=%q ref=%q: %s", e.To, e.Ref, e.Reason)
	}

	// One one-sided child: the whole chain is one-sided.
	e = s.elevateCompose(self, evidence.L1, executor.VerdictOK, []string{"child.one"}, "att-b")
	if e.To != "" || e.Ref != "" {
		t.Fatalf("one one-sided child must block the elevation, got to=%q ref=%q", e.To, e.Ref)
	}
	if !strings.Contains(e.Reason, "one-sided") {
		t.Fatalf("the refusal must say the chain is one-sided, got: %s", e.Reason)
	}
	if !strings.Contains(e.Reason, "child.one") {
		t.Fatalf("the refusal must name the child, got: %s", e.Reason)
	}
}

// The other three refusals each name a different fact, so a test that only
// checked "it refused" would pass for the wrong one.
func TestEachComposeElevationRefusalNamesADifferentFact(t *testing.T) {
	s := testServer(t)
	st, err := store.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	s.skills = st
	childA := registerTestChild(t, st, "child.a")
	childB := registerTestChild(t, st, "child.b")
	self := &store.Manifest{SkillID: "comb.two", Version: "1", Kind: store.KindCompose,
		Evidence: evidence.L0, License: "MIT",
		Provenance: store.Provenance{Source: "test"},
		Deps:       []string{childA, childB},
		Compose:    &store.Composition{Op: "serial", Children: []string{childA, childB}}}

	// 1. An inline composition has nothing to re-register.
	if e := s.elevateCompose(nil, evidence.L1, executor.VerdictOK, nil, "a"); e.Refused != nil ||
		!strings.Contains(e.Reason, "inline composition has no manifest") {
		t.Fatalf("nil self must say there is no manifest, got: %s", e.Reason)
	}
	// 2. A chain that did not verify earned nothing.
	if e := s.elevateCompose(self, evidence.L1, executor.VerdictFailed, nil, "a"); e.Refused != nil ||
		!strings.Contains(e.Reason, "did not verify") {
		t.Fatalf("a failed chain must say it did not verify, got: %s", e.Reason)
	}
	// 3. Already at the ceiling is not a failure — it is nothing to do.
	e := s.elevateCompose(self, evidence.L0, executor.VerdictOK, nil, "a")
	if e.Refused != nil {
		t.Fatalf("already at the ceiling is not a refusal: %s", e.Reason)
	}
	if !strings.Contains(e.Reason, "already at its ceiling") {
		t.Fatalf("want the already-at-ceiling reason, got: %s", e.Reason)
	}
}

// registerTestChild publishes a minimal two-sided script and returns its ref.
func registerTestChild(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	m := &store.Manifest{
		SkillID: id, Version: "1", Kind: store.KindScript, Evidence: evidence.L1, License: "MIT",
		Provenance: store.Provenance{Source: "test"},
		Env:        store.Env{Interpreter: "/bin/sh"},
		Verify:     []string{"exit == 0"},
		Refute:     []store.Refutation{{Input: []byte(`{}`), Assert: "exit == 0"}},
	}
	res, refusal := st.Register(store.Registration{Manifest: m,
		Files: map[string][]byte{"run.sh": []byte("exit 0\n")}})
	if refusal != nil {
		t.Fatalf("register %s: %s", id, refusal.Reason)
	}
	return res.Ref
}
