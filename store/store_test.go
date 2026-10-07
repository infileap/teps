package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sansheng/core"
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
