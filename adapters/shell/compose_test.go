package main

import (
	"fmt"
	"strings"
	"testing"

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
