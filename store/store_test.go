package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"teps/core"
)

// ---------- helpers ----------

func tmpStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	ref, ok := err.(*core.Refusal)
	if !ok {
		t.Fatalf("expected a structured refusal, got %T: %v", err, err)
	}
	return ref.Code
}

func goodManifest() *Manifest {
	return &Manifest{
		SkillID:    "read.file",
		Version:    "1.0.0",
		Kind:       KindCode,
		Verify:     []string{"exit == 0"},
		Evidence:   E1,
		Idempotent: true,
		License:    "MIT",
		Provenance: Provenance{Source: "handwritten"},
		Env:        Env{Interpreter: "builtin"},
	}
}

func regWith(m *Manifest, files map[string][]byte) Registration {
	if files == nil {
		files = map[string][]byte{"main.go": []byte("package main\n")}
	}
	return Registration{Manifest: m, Files: files}
}

func casBlobs(t *testing.T, s *Store) int {
	t.Helper()
	n := 0
	filepath.Walk(filepath.Join(s.CAS.root), func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// ---------- CAS ----------

func TestCASRoundTripAndDedup(t *testing.T) {
	c, err := OpenCAS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h1, err := c.Put([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := c.Put([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("same bytes must have the same name: %s vs %s", h1, h2)
	}
	got, err := c.Get(h1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestCASRefusesNonHashes(t *testing.T) {
	c, _ := OpenCAS(t.TempDir())
	bad := []string{
		"",
		"../../etc/passwd",
		"/etc/passwd",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("g", 64),
		strings.ToUpper(strings.Repeat("a", 64)),
	}
	for _, b := range bad {
		if _, err := c.Get(b); err == nil {
			t.Errorf("Get(%q) must be refused", b)
		}
		if c.Has(b) {
			t.Errorf("Has(%q) must be false", b)
		}
	}
}

func TestCASTamperIsRefusedOnRead(t *testing.T) {
	dir := t.TempDir()
	c, _ := OpenCAS(dir)
	h, err := c.Put([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, h[:2], h)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("goodbye"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(h)
	if err == nil {
		t.Fatal("a blob whose bytes no longer hash to its name must be refused")
	}
	if got := codeOf(t, err); got != core.CodeRecoveryRequired {
		t.Fatalf("want recovery_required, got %s", got)
	}
}

// ---------- manifest ----------

func TestManifestValidateFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Manifest)
	}{
		{"bad skill_id", func(m *Manifest) { m.SkillID = "../escape" }},
		{"empty version", func(m *Manifest) { m.Version = "" }},
		{"unknown kind", func(m *Manifest) { m.Kind = "magic" }},
		{"unknown evidence", func(m *Manifest) { m.Evidence = "E9" }},
		{"no verify", func(m *Manifest) { m.Verify = nil }},
		{"no license", func(m *Manifest) { m.License = "" }},
		{"unknown license", func(m *Manifest) { m.License = "WTFPL" }},
		{"dep is a version", func(m *Manifest) { m.Deps = []string{"1.2.3"} }},
		{"no interpreter", func(m *Manifest) { m.Env.Interpreter = "" }},
		{"network at runtime", func(m *Manifest) { m.Env.Network = true }},
		{"no provenance", func(m *Manifest) { m.Provenance.Source = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodManifest()
			tc.mut(m)
			if ref := m.Validate(); ref == nil {
				t.Fatalf("%s must be refused", tc.name)
			}
		})
	}
	if ref := goodManifest().Validate(); ref != nil {
		t.Fatalf("the good manifest must pass: %v", ref)
	}
}

func TestManifestHashIsStableAndSensitive(t *testing.T) {
	a, ref := goodManifest().ManifestHash()
	if ref != nil {
		t.Fatal(ref)
	}
	b, _ := goodManifest().ManifestHash()
	if a != b {
		t.Fatal("the same declaration must have the same address")
	}
	m := goodManifest()
	m.Env.Packages = map[string]string{"requests": "2.31.0"}
	c, _ := m.ManifestHash()
	if c == a {
		t.Fatal("the environment closure is part of the identity, so a different env must hash differently")
	}
}

func TestVerifiedRejectsE0(t *testing.T) {
	m := goodManifest()
	m.Evidence = E0
	if m.Verified() {
		t.Fatal("E0 is generation without verification and must never count as verified")
	}
	m.Evidence = E1
	if !m.Verified() {
		t.Fatal("E1 is a sandbox run that succeeded")
	}
}

// ---------- license ----------

func TestLicenseClosureIsTheMaximum(t *testing.T) {
	cl, ref := SolveLicense("MIT", []string{"Apache-2.0", "MPL-2.0"})
	if ref != nil {
		t.Fatal(ref)
	}
	if cl.Class != WeakCopyleft {
		t.Fatalf("want weak-copyleft, got %s", cl.Class)
	}
	if len(cl.Members) != 3 {
		t.Fatalf("want 3 members, got %d", len(cl.Members))
	}
	// The members must be in a deterministic order, or the same closure
	// serialises differently on different runs.
	if cl.Members[0].License != "Apache-2.0" || cl.Members[1].License != "MIT" {
		t.Fatalf("members must be sorted: %v", cl.Members)
	}
}

func TestOneGPLMakesTheWholeClosureGPL(t *testing.T) {
	cl, ref := SolveLicense("MIT", []string{"MIT", "GPL-3.0-only"})
	if ref != nil {
		t.Fatal(ref)
	}
	if cl.Class != StrongCopyleft {
		t.Fatalf("one GPL file in a permissive pile makes the pile GPL, got %s", cl.Class)
	}
	if ref := cl.CheckPolicy(DefaultPolicy()); ref == nil {
		t.Fatal("the default policy allows up to weak-copyleft, so this must be refused")
	} else if ref.Code != core.CodeLicenseConflict {
		t.Fatalf("want license_conflict, got %s", ref.Code)
	}
}

func TestUnknownLicenseIsNotPermissive(t *testing.T) {
	if _, ref := SolveLicense("MIT", []string{"WTFPL"}); ref == nil {
		t.Fatal("an unrecognised license must refuse the closure, not be assumed harmless")
	} else if ref.Code != core.CodeLicenseConflict {
		t.Fatalf("want license_conflict, got %s", ref.Code)
	}
}

// Two gates reach "unrecognised license": manifest validation and closure
// solving. A client reads the code to decide whether to retry, so if the gates
// disagree, the retry loop works on one path and not the other.
//
// drive.sh caught exactly this: the manifest gate said bad_request while the
// closure gate said license_conflict, for the same identifier.
func TestBothLicenseGatesGiveOneFactOneCode(t *testing.T) {
	m := goodManifest()
	m.License = "WTFPL"

	fromValidate := m.Validate()
	_, fromSolve := SolveLicense(m.License, nil)
	if fromValidate == nil || fromSolve == nil {
		t.Fatal("both gates must refuse an unrecognised license")
	}
	if fromValidate.Code != fromSolve.Code {
		t.Fatalf("one fact, two codes: validate=%s solve=%s", fromValidate.Code, fromSolve.Code)
	}
	if fromValidate.Code != core.CodeLicenseConflict {
		t.Fatalf("want license_conflict, got %s", fromValidate.Code)
	}
}

func TestPolicyCanBeWidenedAndNarrowed(t *testing.T) {
	cl, _ := SolveLicense("GPL-3.0-only", nil)
	if ref := cl.CheckPolicy(Policy{AllowUpTo: StrongCopyleft}); ref != nil {
		t.Fatalf("widening the policy is one line and must work: %v", ref)
	}
	mit, _ := SolveLicense("MIT", nil)
	if ref := mit.CheckPolicy(Policy{AllowUpTo: Permissive}); ref != nil {
		t.Fatalf("MIT must pass the strictest policy: %v", ref)
	}
}

// ---------- refs ----------

func TestRefsMoveAndRollBack(t *testing.T) {
	r, err := OpenRefs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	if ref := r.Set("x", ChannelStable, a); ref != nil {
		t.Fatal(ref)
	}
	if ref := r.Set("x", ChannelStable, b); ref != nil {
		t.Fatal(ref)
	}
	got, ref := r.Get("x", ChannelStable)
	if ref != nil {
		t.Fatal(ref)
	}
	if got != b {
		t.Fatalf("want %s, got %s", b, got)
	}
	hist, ref := r.History("x", ChannelStable)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(hist) != 2 || hist[0] != a || hist[1] != b {
		t.Fatalf("history must be oldest-first: %v", hist)
	}
	// Rollback is the same operation as forward motion.
	if ref := r.Set("x", ChannelStable, hist[0]); ref != nil {
		t.Fatal(ref)
	}
	if got, _ := r.Get("x", ChannelStable); got != a {
		t.Fatalf("rollback must land on the first hash, got %s", got)
	}
}

func TestRefsRefusePathsAndNonHashes(t *testing.T) {
	r, _ := OpenRefs(t.TempDir())
	h := strings.Repeat("a", 64)
	for _, name := range []string{"../escape", "a/b", "", ".hidden", "UPPER", "a b"} {
		if ref := r.Set(name, ChannelStable, h); ref == nil {
			t.Errorf("ref name %q must be refused", name)
		}
	}
	if ref := r.Set("ok", ChannelStable, "not-a-hash"); ref == nil {
		t.Error("a ref must point at a content hash")
	}
	if _, ref := r.Get("missing", ChannelStable); ref == nil {
		t.Error("a missing ref must be refused, not silently empty")
	}
}

// ---------- composition ----------

func TestReliabilityIsAProductAndSelectIsTheException(t *testing.T) {
	ten := make([]float64, 10)
	for i := range ten {
		ten[i] = 0.99
	}
	got := ReliabilityOf(&Composition{Op: OpSerial}, ten)
	if got > 0.905 || got < 0.904 {
		t.Fatalf("ten skills at 0.99 serialise to ~0.9044, got %f", got)
	}
	twenty := make([]float64, 20)
	for i := range twenty {
		twenty[i] = 0.99
	}
	if got := ReliabilityOf(&Composition{Op: OpSerial}, twenty); got > 0.82 || got < 0.81 {
		t.Fatalf("twenty skills at 0.99 serialise to ~0.8179, got %f", got)
	}
	// select is the only operator that improves reliability.
	sel := ReliabilityOf(&Composition{Op: OpSelect}, []float64{0.9, 0.9})
	if sel < 0.989 || sel > 0.991 {
		t.Fatalf("select over two 0.9s is 0.99, got %f", sel)
	}
	// retry is bounded, and the bound is what keeps it from becoming a loop.
	rt := ReliabilityOf(&Composition{Op: OpRetry, MaxAttempts: 3}, []float64{0.5})
	if rt < 0.874 || rt > 0.876 {
		t.Fatalf("retry x3 over 0.5 is 0.875, got %f", rt)
	}
}

func TestMaxDepthIsDerivedNotChosen(t *testing.T) {
	// The numbers in the design doc, checked rather than asserted.
	if got := MaxDepth(0.99, 0.9); got != 10 {
		t.Fatalf("0.99 with a floor of 0.9 gives 10, got %d", got)
	}
	if got := MaxDepth(0.95, 0.9); got != 2 {
		t.Fatalf("0.95 with a floor of 0.9 gives 2, got %d", got)
	}
	if got := MaxDepth(1.0, 0.9); got != maxDepthCeiling {
		t.Fatalf("a perfectly reliable skill never decays, got %d", got)
	}
	if got := MaxDepth(0.5, 0.9); got != 0 {
		t.Fatalf("a coin flip never clears a 0.9 floor, got %d", got)
	}
}

func TestPruneReportsWhatItDropped(t *testing.T) {
	cands := []Candidate{
		{Op: OpSerial, Rel: 0.95, Depth: 5},
		{Op: OpSerial, Rel: 0.85, Depth: 5},  // below the floor
		{Op: OpSerial, Rel: 0.99, Depth: 12}, // deeper than the floor allows
	}
	kept, dropped := Prune(cands, 0.9, 0.99)
	if len(kept) != 1 || len(dropped) != 2 {
		t.Fatalf("want 1 kept and 2 dropped, got %d and %d", len(kept), len(dropped))
	}
	if kept[0].Rel != 0.95 {
		t.Fatalf("the wrong candidate survived: %+v", kept[0])
	}
}

func TestEvidenceMayBeLoweredAndNeverRaised(t *testing.T) {
	if got := MinEvidence(E1, E3); got != E1 {
		t.Fatalf("min(E1,E3) must be E1, got %s", got)
	}
	if got := MinEvidenceAll([]Evidence{E3, E2, E4}); got != E2 {
		t.Fatalf("min over a set must be the weakest, got %s", got)
	}
	if got := MinEvidenceAll(nil); got != E0 {
		t.Fatalf("a composition with no verified children has no verified claim: want E0, got %s", got)
	}
}

// ---------- registration pipeline ----------

func TestRegisterHappyPath(t *testing.T) {
	s := tmpStore(t)
	res, ref := s.Register(regWith(goodManifest(), nil))
	if ref != nil {
		t.Fatalf("registration must succeed: %v", ref)
	}
	if !ValidHash(res.Ref) {
		t.Fatalf("skill_ref must be a content hash, got %q", res.Ref)
	}
	if res.Channel != ChannelCanary {
		t.Fatalf("a new skill lands in canary, not stable: got %s", res.Channel)
	}
	for _, st := range res.Steps {
		if !st.OK {
			t.Fatalf("step %s failed: %s", st.Name, st.Detail)
		}
	}
	if len(res.Steps) != 8 {
		t.Fatalf("want 8 pipeline steps, got %d", len(res.Steps))
	}
	// The ref must point at the package, and the package must resolve.
	got, ref2 := s.Refs.Get("read.file", ChannelCanary)
	if ref2 != nil {
		t.Fatal(ref2)
	}
	if got != res.Ref {
		t.Fatalf("ref %s != result %s", got, res.Ref)
	}
	pkg, m, ref3 := s.Resolve(res.Ref)
	if ref3 != nil {
		t.Fatal(ref3)
	}
	if pkg.Manifest != res.Manifest || m.SkillID != "read.file" {
		t.Fatalf("resolve returned the wrong thing: %+v", pkg)
	}
}

func TestRegisterIsDeduplicating(t *testing.T) {
	s := tmpStore(t)
	a, ref := s.Register(regWith(goodManifest(), nil))
	if ref != nil {
		t.Fatal(ref)
	}
	before := casBlobs(t, s)
	b, ref := s.Register(regWith(goodManifest(), nil))
	if ref != nil {
		t.Fatal(ref)
	}
	if a.Ref != b.Ref {
		t.Fatalf("the same content must produce the same skill_ref: %s vs %s", a.Ref, b.Ref)
	}
	if after := casBlobs(t, s); after != before {
		t.Fatalf("re-registering identical content wrote %d new blobs", after-before)
	}
}

func TestRegisterRefusesAndWritesNothing(t *testing.T) {
	s := tmpStore(t)

	// a payload that escapes the package
	m := goodManifest()
	if _, ref := s.Register(regWith(m, map[string][]byte{"../evil": []byte("x")})); ref == nil {
		t.Fatal("a traversing payload path must be refused")
	} else if ref.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", ref.Code)
	}
	if n := casBlobs(t, s); n != 0 {
		t.Fatalf("a refused registration left %d blobs behind", n)
	}

	// an unknown license
	m = goodManifest()
	m.License = "WTFPL"
	if _, ref := s.Register(regWith(m, nil)); ref == nil {
		t.Fatal("an unknown license must be refused")
	}

	// a dependency the store does not have
	m = goodManifest()
	m.Deps = []string{strings.Repeat("c", 64)}
	if _, ref := s.Register(regWith(m, nil)); ref == nil {
		t.Fatal("a missing dependency must be refused")
	}
}

func TestLicenseClosureFollowsDependencies(t *testing.T) {
	s := tmpStore(t)

	// Under a widened policy a GPL skill can exist in the store at all.
	s.Pol = Policy{AllowUpTo: StrongCopyleft}
	dep := goodManifest()
	dep.SkillID = "copyleft.helper"
	dep.License = "GPL-3.0-only"
	depRes, ref := s.Register(regWith(dep, nil))
	if ref != nil {
		t.Fatalf("the dep must register under a widened policy: %v", ref)
	}

	// Back to the default policy: a permissive skill that depends on it inherits
	// the obligation. This is the whole point of computing a closure instead of
	// checking one field.
	s.Pol = DefaultPolicy()
	m := goodManifest()
	m.SkillID = "uses.copyleft"
	m.Deps = []string{depRes.Ref}
	_, ref = s.Register(regWith(m, nil))
	if ref == nil {
		t.Fatal("a permissive skill depending on GPL must be refused")
	}
	if ref.Code != core.CodeLicenseConflict {
		t.Fatalf("want license_conflict, got %s", ref.Code)
	}
}

func TestPromoteRefusesUnverifiedWork(t *testing.T) {
	s := tmpStore(t)
	m := goodManifest()
	m.Evidence = E0
	if _, ref := s.Register(regWith(m, nil)); ref != nil {
		t.Fatal(ref)
	}
	if ref := s.Promote("read.file", ChannelCanary, ChannelStable); ref == nil {
		t.Fatal("E0 is generation without verification; freezing it must be refused")
	}
	m.Evidence = E1
	if _, ref := s.Register(regWith(m, nil)); ref != nil {
		t.Fatal(ref)
	}
	if ref := s.Promote("read.file", ChannelCanary, ChannelStable); ref != nil {
		t.Fatalf("verified work must promote: %v", ref)
	}
	got, ref := s.Refs.Get("read.file", ChannelStable)
	if ref != nil {
		t.Fatal(ref)
	}
	if !ValidHash(got) {
		t.Fatalf("stable must hold a hash, got %q", got)
	}
}

// ---------- 组合作为一种技能 ----------

func h64(c byte) string { return strings.Repeat(string(c), 64) }

func composeOf(children ...string) *Manifest {
	m := goodManifest()
	m.SkillID = "comb.pair"
	m.Kind = KindCompose
	m.Env = Env{}  // 组合不跑进程，所以没有解释器可声明
	m.Verify = nil // 组合没有自己的观测，断言无可对证
	m.Deps = children
	m.Compose = &Composition{Op: OpSerial, Children: children}
	return m
}

func TestComposeManifestFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		make func() *Manifest
		want string
	}{
		{"kind=compose 没有 compose 声明", func() *Manifest {
			m := composeOf(h64('a'))
			m.Compose = nil
			return m
		}, "kind=compose requires a compose declaration"},
		{"非 compose 的 kind 带着一张没人走的图", func() *Manifest {
			m := goodManifest()
			m.Compose = &Composition{Op: OpSerial, Children: []string{h64('a')}}
			return m
		}, "only meaningful on kind=compose"},
		{"孩子没进 deps", func() *Manifest {
			m := composeOf(h64('a'), h64('b'))
			m.Deps = []string{h64('a')}
			return m
		}, "is not in deps"},
		{"组合了空", func() *Manifest {
			m := composeOf()
			m.Deps = nil
			return m
		}, "needs at least one child"},
		{"组合声明了一个不会被读的解释器", func() *Manifest {
			m := composeOf(h64('a'))
			m.Env = Env{Interpreter: "builtin"}
			return m
		}, "declares no interpreter"},
		{"组合声明了一条自己无从对证的断言", func() *Manifest {
			m := composeOf(h64('a'))
			m.Verify = []string{"exit == 0"}
			return m
		}, "no observation of its own"},
	}
	for _, c := range cases {
		ref := c.make().Validate()
		if ref == nil {
			t.Fatalf("%s: must be refused", c.name)
		}
		if !strings.Contains(ref.Reason, c.want) {
			t.Fatalf("%s: want a reason containing %q, got %q", c.name, c.want, ref.Reason)
		}
	}
}

// 组合是唯一不需要解释器的 kind：它不跑任何进程，跑的是它连起来的那张图。
func TestOnlyACompositionMayOmitTheInterpreter(t *testing.T) {
	m := composeOf(h64('a'))
	if ref := m.Validate(); ref != nil {
		t.Fatalf("a composition declares no interpreter and that is correct, got %v", ref)
	}
	leaf := goodManifest()
	leaf.Env = Env{}
	if ref := leaf.Validate(); ref == nil {
		t.Fatal("every other kind must declare an interpreter: a bare name would be resolved against the server's own PATH")
	}
}

// regLeaf 注册一个普通的脚本技能，返回它的 ref。
func regLeaf(t *testing.T, s *Store, id string) string {
	t.Helper()
	m := goodManifest()
	m.SkillID = id
	res, ref := s.Register(regWith(m, map[string][]byte{"run.sh": []byte("echo -n " + id)}))
	if ref != nil {
		t.Fatalf("registering %s: %v", id, ref)
	}
	return res.Ref
}

func TestACompositionRegistersResolvesAndIsJustAnotherRef(t *testing.T) {
	s := tmpStore(t)
	a := regLeaf(t, s, "one")
	b := regLeaf(t, s, "two")

	m := composeOf(a, b)
	res, ref := s.Register(regWith(m, map[string][]byte{}))
	if ref != nil {
		t.Fatalf("a composition of two registered skills must register, got %v", ref)
	}
	if !ValidHash(res.Ref) {
		t.Fatalf("a composition gets an ordinary content address, got %q", res.Ref)
	}
	if res.Ref == a || res.Ref == b {
		t.Fatal("the composition's ref must differ from its children's")
	}
	pkg, back, ref := s.Resolve(res.Ref)
	if ref != nil {
		t.Fatalf("a composition must resolve like anything else, got %v", ref)
	}
	if back.Kind != KindCompose {
		t.Fatalf("want kind=compose, got %s", back.Kind)
	}
	// 许可证闭包沿 deps 传播：两个 MIT 孩子 ⇒ 整堆仍是 permissive。
	if pkg.License.Class != Permissive {
		t.Fatalf("a composition's licence closure is its children's, want permissive, got %s", pkg.License.Class)
	}
	if len(pkg.Files) != 0 {
		t.Fatalf("a composition has no payload of its own, got %v", pkg.Files)
	}
}

// ★ 组合不可能引用自己，而且**不需要任何环检测**。
//
// 一个 manifest 装的是它孩子的**包哈希**。要引用自己，它的哈希就得先于它自己
// 被确定——那是一个不存在的固定点。间接环同理：每一步都要求对方的哈希已知。
// 内容寻址把无环性白送了。深度上限因此只是资源账，不是正确性账。
func TestACompositionCannotReferenceItself(t *testing.T) {
	s := tmpStore(t)
	a := regLeaf(t, s, "one")

	// 想引用自己，就得先知道自己的包哈希。不知道，所以写不出来；
	// 能写出来的只是「引用了某个不在店里的东西」，而那会被拒。
	m := composeOf(a, h64('f'))
	_, ref := s.Register(regWith(m, map[string][]byte{}))
	if ref == nil {
		t.Fatal("a child that is not in the store must be refused")
	}
	if !strings.Contains(ref.Reason, "not a skill_ref in this store") {
		t.Fatalf("the refusal must say the child is missing, got %q", ref.Reason)
	}
}

// ★ 组合的可靠性是孩子可靠性的乘积，而且组合**本身**也有一个后验——
// 因为它现在有自己的 ref 了。
func TestACompositionHasItsOwnRefSoItHasItsOwnReliability(t *testing.T) {
	s := tmpStore(t)
	a := regLeaf(t, s, "one")
	b := regLeaf(t, s, "two")
	first, ref := s.Register(regWith(composeOf(a, b), map[string][]byte{}))
	if ref != nil {
		t.Fatal(ref)
	}
	// 同样的形状、同样的孩子顺序 ⇒ 同一个 ref，因此共用同一个后验。
	again, ref2 := s.Register(regWith(composeOf(a, b), map[string][]byte{}))
	if ref2 != nil {
		t.Fatal(ref2)
	}
	if first.Ref != again.Ref {
		t.Fatal("the same composition must have the same address")
	}
	// 换一个孩子 ⇒ 不同的 ref ⇒ 不同的后验。这正是按形状分组做不到的事。
	other := regLeaf(t, s, "three")
	perm, ref3 := s.Register(regWith(composeOf(b, a), map[string][]byte{}))
	if ref3 != nil {
		t.Fatal(ref3)
	}
	if perm.Ref == first.Ref {
		t.Fatal("children order is part of the graph, so A;B and B;A are different skills")
	}
	third, ref4 := s.Register(regWith(composeOf(a, other), map[string][]byte{}))
	if ref4 != nil {
		t.Fatal(ref4)
	}
	if third.Ref == first.Ref {
		t.Fatal("a different child is a different composition")
	}
}

func TestACompositionCarriesNoFiles(t *testing.T) {
	s := tmpStore(t)
	a := regLeaf(t, s, "one")
	_, ref := s.Register(regWith(composeOf(a), map[string][]byte{"run.sh": []byte("echo hi")}))
	if ref == nil {
		t.Fatal("a composition's payload is its graph; files here would be bytes nothing runs")
	}
	if !strings.Contains(ref.Reason, "carries no files") {
		t.Fatalf("the refusal must say what the files would have been, got %q", ref.Reason)
	}
}

// ★ A content hash is a triple — algorithm, version, normalization — and the
// string is only one of the three.
//
// The concrete cost of leaving the algorithm implicit: a digest produced by
// some other 64-hex algorithm passes a shape check, the recomputation disagrees,
// and the caller is told `recovery_required` — "these bytes are corrupt". The
// truth is "this store does not implement that algorithm". Two different facts,
// one code, and the second one sends the reader looking for damage that is not
// there.
func TestAnUnknownAlgorithmIsNotCorruption(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCAS(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("payload")
	h, err := c.Put(data)
	if err != nil {
		t.Fatal(err)
	}

	// Same bytes, same length, different algorithm named.
	foreign := "blake3:" + h
	_, err = c.Get(foreign)
	if err == nil {
		t.Fatal("a digest in an algorithm this store does not have must be refused")
	}
	ref, ok := err.(*core.Refusal)
	if !ok {
		t.Fatalf("want a refusal, got %T", err)
	}
	if ref.Code != core.CodeBadRequest {
		t.Fatalf("an unimplemented algorithm is a caller error, not damage: got %s", ref.Code)
	}
	if !strings.Contains(ref.Reason, "blake3") || !strings.Contains(ref.Reason, "not corrupt") {
		t.Fatalf("the refusal must name the algorithm and say the bytes are fine: %s", ref.Reason)
	}

	// And the fact it must NOT be confused with: real corruption. The blob has
	// to exist for this to be a claim about its bytes — an absent file is a
	// third answer again, and it comes back as a path error.
	bad := strings.Repeat("0", 64)
	if err := os.MkdirAll(filepath.Dir(c.path(bad)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path(bad), []byte("tampered"), 0o444); err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(bad)
	ref2, ok := err.(*core.Refusal)
	if !ok {
		t.Fatalf("want a refusal, got %T", err)
	}
	if ref2.Code != core.CodeRecoveryRequired {
		t.Fatalf("a sha256 digest whose bytes disagree IS damage: got %s", ref2.Code)
	}
	if ref.Code == ref2.Code {
		t.Fatal("two different facts must not share one code")
	}
}

func TestADigestMayNameItsAlgorithmAndBothFormsNameTheSameBlob(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCAS(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Put([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	// Bare hex is the stored form and means sha256.
	for _, form := range []string{h, AlgoSHA256 + ":" + h} {
		got, err := c.Get(form)
		if err != nil {
			t.Fatalf("Get(%s): %s", form, err)
		}
		if string(got) != "payload" {
			t.Fatalf("Get(%s) returned %q", form, got)
		}
		// ★ Get and Has must agree about what a name is. A store whose two
		// halves disagree would deny a blob it can read.
		if !c.Has(form) {
			t.Fatalf("Has(%s) must agree with Get(%s)", form, form)
		}
	}
	if c.Has("blake3:" + h) {
		t.Fatal("Has must not claim a blob under an algorithm this store does not have")
	}
	if c.Has(strings.Repeat("0", 64)) {
		t.Fatal("Has reports presence, not validity, but a well-formed absent hash is still absent")
	}
}

func TestSplitDigestReadsBothForms(t *testing.T) {
	h := strings.Repeat("a", 64)
	cases := []struct{ in, algo, hex string }{
		{h, AlgoSHA256, h},
		{AlgoSHA256 + ":" + h, AlgoSHA256, h},
		{"blake3:" + h, "blake3", h},
	}
	for _, c := range cases {
		algo, hex := SplitDigest(c.in)
		if algo != c.algo || hex != c.hex {
			t.Fatalf("SplitDigest(%q) = (%q,%q), want (%q,%q)", c.in, algo, hex, c.algo, c.hex)
		}
	}
	if _, _, ok := ValidDigest("blake3:" + h); ok {
		t.Fatal("an algorithm this store does not implement is not valid here")
	}
	if algo, _, _ := ValidDigest("blake3:" + h); algo != "blake3" {
		t.Fatalf("the algorithm must still be reported so the caller can say which one: %q", algo)
	}
	if _, _, ok := ValidDigest("sha256:nothex"); ok {
		t.Fatal("a malformed hex is not valid")
	}
}

// ★ The third leg: normalization.
//
// A manifest's digest is the hash of its canonical bytes. Bytes written to a
// different recipe are the same *value* under a different *identity*, and the
// bytes are intact — which is again a different answer from corruption.
func TestABlobWrittenToADifferentRecipeIsNotCorrupt(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCAS(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately not canonical: keys out of order and a space.
	h, err := c.Put([]byte(`{"b":1, "a":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	err = c.GetJSON(h, &v)
	if err == nil {
		t.Fatal("a non-canonical manifest blob must be reported")
	}
	ref, ok := err.(*core.Refusal)
	if !ok {
		t.Fatalf("want a refusal, got %T", err)
	}
	if !strings.Contains(ref.Reason, NormCanonJSON) {
		t.Fatalf("the refusal must name the normalization it wanted: %s", ref.Reason)
	}
	if !strings.Contains(ref.Reason, "intact") {
		t.Fatalf("the bytes are fine and the refusal has to say so: %s", ref.Reason)
	}
	// The canonical form of the same value reads back cleanly.
	ch, canon, err := c.PutJSON(map[string]int{"a": 2, "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != `{"a":2,"b":1}` {
		t.Fatalf("unexpected canonical form: %s", canon)
	}
	if err := c.GetJSON(ch, &v); err != nil {
		t.Fatalf("canonical bytes must read back: %s", err)
	}
}

// ★ Resolve must not flatten the CAS's answers into one sentence.
//
// Four different facts reach it, and only the first means "you asked for
// something that is not here":
//
//	absent          -> not a skill_ref in this store
//	corrupt bytes   -> recovery_required
//	unknown algo    -> bad_request, naming the algorithm
//	wrong recipe    -> recovery_required, naming the normalization
//
// A reader sent down the wrong one of those four looks for damage that is not
// there, or for a missing thing that is present.
func TestResolveKeepsTheFourAnswersApart(t *testing.T) {
	s := tmpStore(t)

	// 1. Absent.
	if _, _, ref := s.Resolve(strings.Repeat("a", 64)); ref == nil ||
		!strings.Contains(ref.Reason, "not a skill_ref in this store") {
		t.Fatalf("an absent ref must say so: %v", ref)
	}

	// 2. Unknown algorithm — the bytes may be perfectly fine.
	if _, _, ref := s.Resolve("blake3:" + strings.Repeat("a", 64)); ref == nil ||
		ref.Code != core.CodeBadRequest || !strings.Contains(ref.Reason, "blake3") {
		t.Fatalf("an unimplemented algorithm must be a caller error naming it: %v", ref)
	}

	// 3. Corrupt bytes: the blob exists and does not match its own name.
	bad := strings.Repeat("b", 64)
	if err := os.MkdirAll(filepath.Dir(s.CAS.path(bad)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.CAS.path(bad), []byte("tampered"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, _, ref := s.Resolve(bad); ref == nil || ref.Code != core.CodeRecoveryRequired {
		t.Fatalf("a corrupt blob must be a recovery event: %v", ref)
	}

	// 4. Right bytes, wrong recipe.
	raw := strings.Repeat("c", 64)
	if err := os.MkdirAll(filepath.Dir(s.CAS.path(raw)), 0o755); err != nil {
		t.Fatal(err)
	}
	// A package blob, but not in canonical form.
	body := []byte(`{"manifest":"` + strings.Repeat("d", 64) + `", "files":{}}`)
	h := Hash(body)
	if err := os.MkdirAll(filepath.Dir(s.CAS.path(h)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.CAS.path(h), body, 0o444); err != nil {
		t.Fatal(err)
	}
	_, _, ref := s.Resolve(h)
	if ref == nil || !strings.Contains(ref.Reason, NormCanonJSON) {
		t.Fatalf("a blob written to another recipe must name the normalization: %v", ref)
	}
}

// ★ 组合不许洗白孩子的证据等级。
//
// 组合本身不产生任何观测 —— 它没有自己的进程、没有自己的断言。所以它不能
// 声称比它所由组成的最弱那一部分更高的信任。这一条在文档里写了很久，而在
// 代码里从来没被检查过：一份由两个 E0 孩子组成的组合可以声明 E1 并被接受。
//
// ★ 而 `min(children)` 是**天花板**，不是等级。注册时组合从未作为一条链跑过，
// 所以它的等级仍然是它声明的那个。用孩子的等级顶上去，就是用「零件都行」
// 冒充「整条链行」—— 那正是证据阶梯存在的理由，只不过方向相反。
func TestACompositionCannotLaunderItsChildrensEvidence(t *testing.T) {
	s := tmpStore(t)
	child := func(id string, lvl Evidence) string {
		m := &Manifest{
			SkillID: id, Version: "1.0.0", Kind: KindScript,
			Verify: []string{"exit == 0"}, Budget: core.Budget{MS: 1000},
			Evidence: lvl, Idempotent: true, License: "MIT",
			Provenance: Provenance{Source: "handwritten"},
			Env:        Env{Interpreter: "/bin/sh"},
		}
		res, ref := s.Register(regWith(m, map[string][]byte{"run.sh": []byte("exit 0\n")}))
		if ref != nil {
			t.Fatalf("child %s: %s", id, ref.Reason)
		}
		return res.Ref
	}
	e0a, e0b := child("laun.a", E0), child("laun.b", E0)
	e1a := child("laun.c", E1)

	compose := func(id string, lvl Evidence, kids ...string) (*Result, *core.Refusal) {
		m := &Manifest{
			SkillID: id, Version: "1.0.0", Kind: KindCompose,
			Evidence: lvl, License: "MIT", Deps: kids,
			Idempotent: true,
			Provenance: Provenance{Source: "handwritten"},
			Compose:    &Composition{Op: OpSerial, Children: kids},
		}
		return s.Register(regWith(m, map[string][]byte{}))
	}

	// 两个 E0 孩子组成的组合声明 E1：拒绝。
	_, ref := compose("laun.liar", E1, e0a, e0b)
	if ref == nil {
		t.Fatal("a composition of E0 children must not be allowed to declare E1")
	}
	if !strings.Contains(ref.Reason, "cannot claim more trust") {
		t.Fatalf("the refusal must say what is wrong, got %q", ref.Reason)
	}

	// 声明 E0：接受，而且**入账是 E0** —— 它从未作为一条链跑过。
	res, ref := compose("laun.honest", E0, e0a, e0b)
	if ref != nil {
		t.Fatalf("declaring the truth must be accepted: %s", ref.Reason)
	}
	if res.Evidence != E0 {
		t.Fatalf("a composition that has never run is E0, got %q", res.Evidence)
	}

	// ★ 天花板不是等级：孩子的等级**不会**顶上去。
	// 组合声明 E0 而孩子是 E1 —— 入账仍是 E0。
	res2, ref := compose("laun.ceiling", E0, e1a)
	if ref != nil {
		t.Fatalf("E0 is below the ceiling: %s", ref.Reason)
	}
	if res2.Evidence != E0 {
		t.Fatalf("the ceiling must not be promoted to the level: got %q, want E0", res2.Evidence)
	}

	// 而声明等于天花板是允许的（存储层面它不超过上限）。
	// 它是否**真的**跑过是链的事，不是店的事 —— 店只核它看得见的那一半。
	if _, ref := compose("laun.atceiling", E1, e1a); ref != nil {
		t.Fatalf("declaring exactly the ceiling is not a lie about the children: %s", ref.Reason)
	}
}

// ★ `min` 与「取第一个」只在**等级不同**时才分得出来。
//
// 上面那组夹具里孩子的等级都相同（E0+E0、E1+E1），于是「上限取第一个孩子」
// 这条变异活了下来 —— 一个检查只在它的输入有差异时才与它的变异不同，而我只
// 用没有差异的输入测过它。这是这个项目反复栽的同一个形状：**用所有答案都
// 一样的输入，测不出一个会分辨答案的检查。**
//
// 所以这里的孩子等级必须**不同**，而且两种顺序都要测 —— 否则「取第一个」
// 在一个顺序上碰巧是对的。
func TestTheCeilingIsTheMinimumNotTheFirst(t *testing.T) {
	s := tmpStore(t)
	child := func(id string, lvl Evidence) string {
		m := &Manifest{
			SkillID: id, Version: "1.0.0", Kind: KindScript,
			Verify: []string{"exit == 0"}, Budget: core.Budget{MS: 1000},
			Evidence: lvl, Idempotent: true, License: "MIT",
			Provenance: Provenance{Source: "handwritten"},
			Env:        Env{Interpreter: "/bin/sh"},
		}
		res, ref := s.Register(regWith(m, map[string][]byte{"run.sh": []byte("exit 0\n")}))
		if ref != nil {
			t.Fatalf("child %s: %s", id, ref.Reason)
		}
		return res.Ref
	}
	weak, strong := child("mix.weak", E0), child("mix.strong", E1)

	for _, order := range [][]string{{weak, strong}, {strong, weak}} {
		m := &Manifest{
			SkillID: "mix.combo", Version: "1.0.0", Kind: KindCompose,
			Evidence: E1, License: "MIT", Deps: order,
			Idempotent: true,
			Provenance: Provenance{Source: "handwritten"},
			Compose:    &Composition{Op: OpSerial, Children: order},
		}
		_, ref := s.Register(regWith(m, map[string][]byte{}))
		if ref == nil {
			t.Fatalf("a mixed pair whose minimum is E0 must not carry E1, order %v", order)
		}
		if !strings.Contains(ref.Reason, "minimum is E0") {
			t.Fatalf("the refusal must name the minimum, not whichever child came first: %q", ref.Reason)
		}
	}

	// 对照：整组都是 E1 时，E1 是允许的 —— 否则上一条会被「一律拒绝」满足。
	m := &Manifest{
		SkillID: "mix.allstrong", Version: "1.0.0", Kind: KindCompose,
		Evidence: E1, License: "MIT", Deps: []string{strong},
		Idempotent: true,
		Provenance: Provenance{Source: "handwritten"},
		Compose:    &Composition{Op: OpSerial, Children: []string{strong}},
	}
	if _, ref := s.Register(regWith(m, map[string][]byte{})); ref != nil {
		t.Fatalf("all-strong children permit the ceiling: %s", ref.Reason)
	}
}

// ★ Two rules that look like one, and only one of them is enforced here.
//
// `tools/check_nomodel.py` says content may call a model — it only forbids the
// *engine* from depending on one. This says a *skill* may not fetch at all.
// They are different rules about different things, and conflating them produces
// a plan that cannot be carried out: "rent an API and ship it as a skill" is
// exactly the path this refusal forbids.
//
// The reason is not security, it is reproducibility: a content-addressed skill
// whose behaviour depends on what a remote server says today is not the same
// skill tomorrow, and the hash would still claim it is.
func TestASkillThatFetchesAtRuntimeIsRefused(t *testing.T) {
	m := goodManifest()
	m.Env.Network = true
	ref := m.Validate()
	if ref == nil {
		t.Fatal("env.network=true must be refused: a skill that fetches at runtime is not reproducible")
	}
	if ref.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", ref.Code)
	}
	if !strings.Contains(ref.Reason, "reproducible") {
		t.Fatalf("the refusal must say why, got: %s", ref.Reason)
	}
	// The consequence, stated where somebody changing this will read it:
	// a model reached over the network cannot be a skill. A model in this
	// system is a local program.
	if !strings.Contains(ref.Reason, "env.network must be false") {
		t.Fatalf("the refusal must name the field, got: %s", ref.Reason)
	}
}

// ★ "Talks to a socket" covers two situations that differ in the only way that
// matters here: a remote host is not pinned, and a socket on this machine is.
//
// The old boolean could not tell them apart, so it refused both — which made a
// local model unpublishable, and that is the exact case this project needs.
func TestALocalReachIsPublishableWhenItIsPinned(t *testing.T) {
	s := tmpStore(t)
	m := goodManifest()
	m.Env.Reaches = []Reach{{
		Socket:   "unix:/run/teps/model.sock",
		Artifact: "9f2b1c" + strings.Repeat("0", 58),
	}}
	if _, ref := s.Register(regWith(m, nil)); ref != nil {
		t.Fatalf("a loopback reach pinned by a digest must be publishable: %s", ref.Reason)
	}
}

func TestARemoteReachIsStillRefused(t *testing.T) {
	s := tmpStore(t)
	m := goodManifest()
	m.Env.Network = true
	_, ref := s.Register(regWith(m, nil))
	if ref == nil {
		t.Fatal("reaching another host is not reproducible and must be refused")
	}
	// The reason must name reproducibility, not safety: the rule is about the
	// ref naming one thing, and that is what a reader needs to know.
	if !strings.Contains(ref.Reason, "not reproducible") {
		t.Fatalf("the reason should be about reproducibility, got: %s", ref.Reason)
	}
	if !strings.Contains(ref.Reason, "another host") {
		t.Fatalf("the reason should name the remote case, got: %s", ref.Reason)
	}
}

// ★ A reach that names something routable is a remote reach wearing the local
// label. The check is a whitelist on purpose: a blacklist would let every
// future spelling of a routable address through.
func TestAReachThatIsNotLoopbackIsRefused(t *testing.T) {
	// ★★★ 而这条测试此前把**两件不同的事**混在一张表里。
	//
	// `http://127.0.0.1:8080` 与 `127.0.0.1:8080` **不是一个端点**（没有
	// `unix:`/`tcp:` 前缀），`tcp:127.0.0.1` **没有端口** —— 它们不是「远端」，
	// 它们是「根本不是端点」。而这张表要求它们全都给出**远端**那个理由：
	//
	//	"that is not a loopback endpoint, so this is a remote reach wearing the
	//	 local label — set env.network instead, and expect it to be refused"
	//
	// 照那句话去改会撞上另一条拒绝 —— **读的人被指了一个圈**。
	// 而这张表之所以能通过，是因为**旧判据把两者都归进了同一条拒绝**：
	// 一条分不清两种拒绝的测试，与一条分不清两种事实的判据，是同一件事的两面。
	for _, spec := range []string{
		"http://127.0.0.1:8080",
		"127.0.0.1:8080",
		"tcp:127.0.0.1",
		// ★ 而这三条是加上端口判据之后才进来的：端口是「最后一个冒号之后的一切」时，
		// 它们都被当成合法的本机端点收下了。
		"tcp:localhost:not-a-port",
		"tcp:localhost:80@evil.com",
		"tcp:127.0.0.1:0",
	} {
		s := tmpStore(t)
		m := goodManifest()
		m.Env.Reaches = []Reach{{Socket: spec, Artifact: strings.Repeat("a", 64)}}
		_, ref := s.Register(regWith(m, nil))
		if ref == nil {
			t.Fatalf("%q names no endpoint at all and must be refused", spec)
		}
		if !strings.Contains(ref.Reason, "not a well-formed endpoint") {
			t.Fatalf("%q: want the malformed-endpoint reason, got: %s", spec, ref.Reason)
		}
		// ★ 而它**不许**给出远端那个理由 —— 那会把读的人指向一个错的修法。
		if strings.Contains(ref.Reason, "remote reach wearing the local label") {
			t.Fatalf("%q is not a remote reach, and saying it is sends the publisher to env.network: %s", spec, ref.Reason)
		}
	}
	// ★ 而**真的是远端**的那些，仍然必须给出远端那个理由（那是可执行的修法方向）。
	for _, spec := range []string{
		"tcp:0.0.0.0:8080",
		"tcp:10.0.0.5:11434",
		"tcp:example.com:443",
	} {
		s := tmpStore(t)
		m := goodManifest()
		m.Env.Reaches = []Reach{{Socket: spec, Artifact: strings.Repeat("a", 64)}}
		_, ref := s.Register(regWith(m, nil))
		if ref == nil {
			t.Fatalf("%q is not a loopback endpoint and must be refused", spec)
		}
		if !strings.Contains(ref.Reason, "remote reach wearing the local label") {
			t.Fatalf("%q: want the local-label reason, got: %s", spec, ref.Reason)
		}
	}
	// And the loopback spellings that ARE local.
	for _, spec := range []string{
		"unix:/run/teps/model.sock",
		"tcp:127.0.0.1:11434",
		"tcp:localhost:11434",
		"tcp:[::1]:11434",
	} {
		s := tmpStore(t)
		m := goodManifest()
		m.Env.Reaches = []Reach{{Socket: spec, Artifact: strings.Repeat("a", 64)}}
		if _, ref := s.Register(regWith(m, nil)); ref != nil {
			t.Fatalf("%q is loopback and must be accepted: %s", spec, ref.Reason)
		}
	}
}

// ★ Without the artifact's hash, "local" is a claim nothing checks.
func TestALocalReachMustNameWhatIsBehindIt(t *testing.T) {
	cases := []struct{ name, socket, artifact, want string }{
		{"no socket", "", strings.Repeat("a", 64), "a claim nothing checks"},
		{"no artifact", "unix:/run/x.sock", "", "a claim nothing checks"},
		{"artifact is not a hash", "unix:/run/x.sock", "nothex", "is not a content hash"},
	}
	for _, c := range cases {
		s := tmpStore(t)
		m := goodManifest()
		m.Env.Reaches = []Reach{{Socket: c.socket, Artifact: c.artifact}}
		_, ref := s.Register(regWith(m, nil))
		if ref == nil {
			t.Fatalf("%s: should have been refused", c.name)
		}
		if !strings.Contains(ref.Reason, c.want) {
			t.Fatalf("%s: want %q, got: %s", c.name, c.want, ref.Reason)
		}
	}
}

func TestTheSameSocketTwiceIsRefused(t *testing.T) {
	s := tmpStore(t)
	m := goodManifest()
	m.Env.Reaches = []Reach{
		{Socket: "unix:/run/x.sock", Artifact: strings.Repeat("a", 64)},
		{Socket: "unix:/run/x.sock", Artifact: strings.Repeat("b", 64)},
	}
	_, ref := s.Register(regWith(m, nil))
	if ref == nil {
		t.Fatal("one endpoint, one entry")
	}
	if !strings.Contains(ref.Reason, "one endpoint, one entry") {
		t.Fatalf("got: %s", ref.Reason)
	}
}

// ★★★ 一个**声明了却没人执行**的字段是一句装饰。
//
// `Effects` 与 `Idempotent` 在 `docs/CONTRACT.md:157` 的 Skill ABI 那一行里是
// 契约承诺过的字段（`effects[]  evidence  idempotent  license`）。实测
// （`skill.publish` 带 `idempotent:false` 与 `effects:["network","filesystem"]`，
// 再 `skill.resolve`）：**两个都被报出来了** —— 所以它们不是「没人读」，
// 它们是「**被读出来、却从来不被执行**」。
//
// ★ 而 `idempotent` 尤其刺眼：契约 A2 说「所有变更幂等」，内核是靠 `attempt`
// 那把键做到的 —— 一个声明 `idempotent: false` 的技能，在声称一件内核不会
// 兑现的事，而系统照样发布它、照样按幂等的方式跑它。
// **声明与行为不一致，而不一致是静默的。**
//
// 上面那条 `kind=compose` 的判据逐字写着同一个理由：
// 「a claim nothing reads」/「would be decoration」。
func TestAManifestMustDeclareItselfIdempotent(t *testing.T) {
	good := Manifest{
		SkillID: "x.y", Version: "1", Kind: KindScript, Evidence: Evidence("E1"), License: "MIT",
		Idempotent: true, Provenance: Provenance{Source: "test"},
		Env: Env{Interpreter: "/bin/sh"},
		// ★ 一条能失败、也能通过的断言 —— `Validate` 要求它（
		// 「an unverifiable skill can only self-report」）。
		Verify: []string{"exit == 0"},
		Refute: []Refutation{{Input: []byte(`{}`), Assert: "exit == 1"}},
	}
	if ref := good.Validate(); ref != nil {
		t.Fatalf("the good manifest must pass: %s", ref.Reason)
	}
	bad := good
	bad.Idempotent = false
	ref := bad.Validate()
	if ref == nil {
		t.Fatal("a skill declaring idempotent:false claims something the kernel will not honour (A2 makes every change idempotent by the attempt key)")
	}
	// ★ 而理由必须**点名 A2 与 attempt 键** —— 只说「不许」读者无从下手。
	for _, want := range []string{"attempt key", "A2"} {
		if !strings.Contains(ref.Reason, want) {
			t.Fatalf("the reason must mention %q, got %q", want, ref.Reason)
		}
	}
	// ★ 而「装饰」这件事必须说出来 —— 那才是这条判据的理由。
	if !strings.Contains(ref.Reason, "decoration") {
		t.Fatalf("the reason must say why a false declaration is refused, got %q", ref.Reason)
	}
}

// ★★★ 一条为了「不可复现」立下的规矩，换个字段写就能绕过去。
//
// `effects[]` 与 `Env` 说的是同一件事（这个技能会碰什么），而实测：
//
//	① env.network:true     -> 被拒：「…is not reproducible…」
//	② effects:["network"]  -> **通过了**（ref=8ed1eed1f32a5359）
//
// 同一句话、两个字段、相反的判决 —— 而**被拒的那一个是有人检查的那一个**。
// 于是 `Validate` 里那条 `env.network` 的判据有**第二扇门，而那扇门是开着的**。
//
// ★ 与 `ShouldEscalate` 那条「两扇门对同一个字段给了相反的答案」是同一条，
// 只是这里更糟：那扇没人查的门**通往同一件被禁止的事**。
func TestEffectsCannotNameWhatEnvAlreadyGoverns(t *testing.T) {
	base := Manifest{
		SkillID: "x.y", Version: "1", Kind: KindScript, Evidence: Evidence("E1"),
		License: "MIT", Idempotent: true, Provenance: Provenance{Source: "test"},
		Env:    Env{Interpreter: "/bin/sh"},
		Verify: []string{"exit == 0"},
		Refute: []Refutation{{Input: []byte(`{}`), Assert: "exit == 1"}},
	}
	// ★ 而**对照**：同一个事实写成 `env.network` 必须被拒 —— 否则这一条测的是别的东西。
	viaEnv := base
	viaEnv.Env.Network = true
	if ref := viaEnv.Validate(); ref == nil {
		t.Fatal("precondition: env.network:true is refused")
	}
	// ★ 而写成 effects 也必须被拒。
	for _, eff := range []string{"network", " NETWORK ", "Socket", "remote"} {
		m := base
		m.Effects = []string{eff}
		ref := m.Validate()
		if ref == nil {
			t.Fatalf("effects %q is the same claim as env.network, and that one is refused: a second, unchecked spelling of a refused claim is a door around the rule", eff)
		}
		// ★ 而理由必须说清它撞上了谁 —— 只说「不许」读者会去猜该写成什么。
		for _, want := range []string{"Env already governs", "env.network"} {
			if !strings.Contains(ref.Reason, want) {
				t.Fatalf("the reason must mention %q, got %q", want, ref.Reason)
			}
		}
	}
	// ★ 而**描述性**的那些照旧放行：`effects` 本来就没有定义过词汇表，
	// 这一条判据堵的是「Env 已经管着的那几个」，不是「所有 effects」。
	for _, eff := range []string{"filesystem", "process", "cpu"} {
		m := base
		m.Effects = []string{eff}
		if ref := m.Validate(); ref != nil {
			t.Fatalf("effects %q is descriptive and must stay allowed: %s", eff, ref.Reason)
		}
	}
}

// ★★★ 一个**没人读得到的写**不是一条记录，是一次丢弃。
//
// store/refs.go:16-20 声明了这个存储恰好有两个通道（canary / stable），而
// register.go:399-402 的解析**只看这两个**（先 stable、再 canary）。
// 而 Set / Get / History 此前用 ValidName 校验通道 —— 它接受任何
// [a-z0-9][a-z0-9._-]*。实测（E2 的技能）：
//
//	② 通道列表： ['canary']
//	   promote canary -> 'stable'     -> 成功了
//	   promote canary -> 'whatever'   -> **成功了**
//	   promote canary -> 'prod'       -> **成功了**
//	   promote canary -> 'a.b-c_d'    -> **成功了**
//	③ 通道列表现在： ['a.b-c_d', 'canary', 'prod', 'stable', 'whatever']
//
// promote 写了一个指针与一条历史，而**没有任何一次读能够到达它**。
//
// ★ 这比死代码更坏：skill.list 会把这些通道**报出来**，于是读的人看见
// channels: ["a.b-c_d", "prod", "whatever"] 并相信它们有意义。
func TestOnlyDeclaredChannelsCanBeWritten(t *testing.T) {
	// ★ 先钉住这个存储定义了哪两个 —— 否则这一条会在常量改名之后静静失效。
	if !ValidChannel(ChannelCanary) || !ValidChannel(ChannelStable) {
		t.Fatal("precondition: the two declared channels must be valid")
	}
	refs, err := OpenRefs(t.TempDir())
	if err != nil {
		t.Fatalf("open refs: %v", err)
	}
	h := strings.Repeat("a", 64)
	for _, ch := range []string{ChannelCanary, ChannelStable} {
		if ref := refs.Set("x.y", ch, h); ref != nil {
			t.Fatalf("the declared channel %q must be writable: %s", ch, ref.Reason)
		}
	}
	// ★ 而**两件事要分开**：名字不合法（`Canary` / `CANARY` / 空串）与
	// 名字合法但这个存储没有这个通道（`whatever` / `prod` / `a.b-c_d`）。
	// 这与 Round 70 的 `wellFormedEndpoint` / `isLoopback` 是同一条：
	// **一条分不清两种拒绝的表，与一条分不清两种事实的判据，是同一件事的两面。**
	for _, ch := range []string{"Canary", "CANARY", ""} {
		ref := refs.Set("x.y", ch, h)
		if ref == nil {
			t.Fatalf("channel %q is not a legal path element and must be refused", ch)
		}
		if !strings.Contains(ref.Reason, "bad ref name") {
			t.Fatalf("%q is a malformed name, not an unknown channel: %s", ch, ref.Reason)
		}
	}
	for _, ch := range []string{"whatever", "prod", "a.b-c_d", "canary2"} {
		ref := refs.Set("x.y", ch, h)
		if ref == nil {
			t.Fatalf("channel %q is not one this store defines, and a pointer written to it is a write no read can reach", ch)
		}
		// ★ 而理由必须说清**这个存储定义了哪两个** —— 只说「未知通道」读者
		// 会去猜该写什么。
		for _, want := range []string{"unknown channel", ChannelCanary, ChannelStable} {
			if !strings.Contains(ref.Reason, want) {
				t.Fatalf("the reason must mention %q, got %q", want, ref.Reason)
			}
		}
		// ★ 而它**不许**说成名字不合法 —— 那会把读的人指向一个错的修法。
		if strings.Contains(ref.Reason, "bad ref name") {
			t.Fatalf("%q is a well-formed name and an undeclared channel; saying it is malformed sends the reader to the wrong fix: %s", ch, ref.Reason)
		}
	}
	// ★ 而读也要拒 —— 一个能读到的未知通道意味着它可能已经被写过了。
	//
	// ★★ 而这里必须断言**理由**，不能只断言「被拒了」：`whatever` 从来没有
	// 被写成功过，所以就算 `Get` 不检查通道，它也会因为「no such ref」而被拒 ——
	// **一条只断言「有错」的检查，分不出「因为未知通道」与「因为文件不存在」。**
	// 实测：删掉 `Get` 里那条判据，这一条照样通过（变异报「漏网」）。
	if _, ref := refs.Get("x.y", "whatever"); ref == nil {
		t.Fatal("reading an undeclared channel must be refused")
	} else if !strings.Contains(ref.Reason, "unknown channel") {
		t.Fatalf("reading an undeclared channel must be refused AS an undeclared channel, "+
			"not as a missing file: %s", ref.Reason)
	}
	// ★ 而写下去的那两个真的读得回来。
	for _, ch := range []string{ChannelCanary, ChannelStable} {
		got, ref := refs.Get("x.y", ch)
		if ref != nil || got != h {
			t.Fatalf("the declared channel %q must read back: %v %v", ch, got, ref)
		}
	}
}

// ★★★ 同一扇门有三处，只堵两处不叫堵上。
//
// Round 71 给 `Refs.Set` 与 `Refs.Get` 加上了「通道必须是声明过的」，而
// **`History` 漏了**：`History("x", "whatever")` 此前回的是
// 「no history for x/whatever」—— 一个**关于文件不存在**的理由，而真正发生的
// 事是「这个存储没有这个通道」。
//
// ★ 而 `Channels` 是另一处：它列的是「目录里有什么」（只按后缀滤掉 `.log`/`.tmp`），
// 而 `skill.list` 把它直接报出去 —— 于是一个**手工建出来的目录**会在回答里
// 读起来像一个真的通道。`Set` 现在写不进去了，但「列出来的东西必须是这个存储
// 定义过的」不该依赖「写不进去」。
func TestEveryChannelDoorKnowsWhichChannelsExist(t *testing.T) {
	root := t.TempDir()
	refs, err := OpenRefs(root)
	if err != nil {
		t.Fatalf("open refs: %v", err)
	}
	h := strings.Repeat("a", 64)
	if ref := refs.Set("x.y", ChannelCanary, h); ref != nil {
		t.Fatalf("publish: %s", ref.Reason)
	}
	// ① History 的拒绝必须是「未知通道」，不是「没有历史」。
	if _, ref := refs.History("x.y", "whatever"); ref == nil {
		t.Fatal("History must refuse an undeclared channel")
	} else if !strings.Contains(ref.Reason, "unknown channel") {
		t.Fatalf("History must say the channel is undeclared, not that the file is missing: %s", ref.Reason)
	}
	// ★ 而声明过的那个通道有历史 —— 否则这一条测的是别的东西。
	hist, ref := refs.History("x.y", ChannelCanary)
	if ref != nil || len(hist) != 1 || hist[0] != h {
		t.Fatalf("the declared channel must have its history: %v %v", hist, ref)
	}
	// ② 手工建出来的目录不许被列出来。
	//
	// 这正是「目录里有什么就列什么」与「这个存储定义过什么」的区别：
	// 前者把**磁盘的状态**当成**契约的一部分**报了出去。
	d := filepath.Join(root, "x.y")
	if err := os.MkdirAll(filepath.Join(d, "prod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, "prod", "HEAD"), []byte(h+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := refs.Channels("x.y")
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	for _, c := range got {
		if c == "prod" {
			t.Fatalf("a directory somebody made by hand is not a channel this store defines: %v", got)
		}
	}
	if len(got) != 1 || got[0] != ChannelCanary {
		t.Fatalf("want exactly the declared channel, got %v", got)
	}
}
