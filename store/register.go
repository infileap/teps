package store

import (
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sansheng/core"
)

// Package is what a ref points at, and therefore what a `skill_ref` names.
//
// Two hashes, two jobs:
//
//   - the **manifest hash** is what a skill *declares* — environment, deps,
//     verify assertions, license. It is an internal address.
//   - the **package hash** is the whole *artifact*: that manifest, the payload,
//     and the resolved license closure, in one record. This is the `skill_ref`.
//
// A ref points at a package; `deps` hold package hashes. The manifest lives
// inside the package, so the package transitively covers everything the
// manifest says — including the environment closure, which is the reason a
// skill's identity is not just its script file.
//
// ★ There is deliberately **no timestamp here**. A timestamp would make the
// same skill hash differently on every registration, which would break
// deduplication and make "same content, same name" false. Time belongs to the
// ref history, which lives outside the content.
type Package struct {
	Manifest string            `json:"manifest"`
	Files    map[string]string `json:"files"`
	License  *Closure          `json:"license"`
	Evidence Evidence          `json:"evidence"`
}

// Store is the whole skill store: an immutable CAS plus the mutable refs that
// point into it.
type Store struct {
	CAS  *CAS
	Refs *Refs
	Pol  Policy
}

// OpenStore opens a store rooted at dir (dir/cas and dir/refs).
func OpenStore(dir string) (*Store, error) {
	cas, err := OpenCAS(filepath.Join(dir, "cas"))
	if err != nil {
		return nil, err
	}
	refs, err := OpenRefs(filepath.Join(dir, "refs"))
	if err != nil {
		return nil, err
	}
	return &Store{CAS: cas, Refs: refs, Pol: DefaultPolicy()}, nil
}

// Registration is one skill submitted to the store.
type Registration struct {
	Manifest *Manifest
	Files    map[string][]byte // payload path -> bytes
}

// Step is one stage of the pipeline with its verdict.
//
// The report is the artifact. A registration that fails must be able to say
// **which gate** refused it — "it was refused" is not a diagnosis, and a
// pipeline whose refusals are indistinguishable is a pipeline nobody can debug.
type Step struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`
}

// Result is what a successful registration produced.
type Result struct {
	Ref      string   `json:"ref"`      // package hash: the skill_ref
	Manifest string   `json:"manifest"` // manifest hash
	Files    []string `json:"files"`    // payload hashes, in name order
	Channel  string   `json:"channel"`
	Evidence Evidence `json:"evidence"`
	License  *Closure `json:"license"`
	Steps    []Step   `json:"steps"`
}

// maxPayload bounds one skill's payload.
const maxPayload = 8 << 20

// checkPayload refuses a payload that could write outside the package.
//
// Same boundary as ValidHash and ValidName, for the same reason: these strings
// arrive from the wire, and a package that joins them into paths is a package
// that unpacks wherever the sender says.
func checkPayload(files map[string][]byte, kind Kind) *core.Refusal {
	if kind == KindCompose {
		// A composition's payload is its graph, and the graph lives in the
		// manifest. Files here would be bytes that nothing ever runs — so they
		// are refused rather than ignored, because an ignored field is a claim
		// nobody checked.
		if len(files) != 0 {
			return &core.Refusal{
				Code:   core.CodeBadRequest,
				Reason: "kind=compose carries no files: its graph is its payload, and these " + itoa(len(files)) + " would be bytes nothing runs",
			}
		}
		return nil
	}
	if len(files) == 0 {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: "a skill needs at least one file"}
	}
	total := 0
	for name, body := range files {
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return &core.Refusal{
				Code:   core.CodeBadRequest,
				Reason: "payload path must be relative and slash-separated: " + name,
			}
		}
		if path.Clean(name) != name || strings.HasPrefix(path.Clean(name), "..") {
			return &core.Refusal{
				Code:   core.CodeBadRequest,
				Reason: "payload path escapes the package: " + name,
			}
		}
		total += len(body)
	}
	if total > maxPayload {
		return &core.Refusal{Code: core.CodeFrameTooLarge, Reason: "payload exceeds 8 MiB"}
	}
	return nil
}

// Register runs the pipeline and returns both the result and every step it took.
//
// **The result is returned even when registration fails**, carrying the steps
// taken up to the refusal. "It was refused" is not a diagnosis; the caller needs
// to know which gate said no.
//
// Order is not cosmetic. Deps are resolved *before* the license closure, because
// the closure is computed from the deps' licenses; the payload is checked
// *before* anything is written, so a rejected skill leaves no bytes in the CAS;
// and the ref moves **last**, so a failure anywhere leaves the store exactly as
// it was.
func (s *Store) Register(reg Registration) (*Result, *core.Refusal) {
	res := &Result{}
	fail := func(name string, ref *core.Refusal) (*Result, *core.Refusal) {
		res.Steps = append(res.Steps, Step{Name: name, OK: false, Detail: ref.Reason, Code: ref.Code})
		return res, ref
	}
	if reg.Manifest == nil {
		return fail("validate", &core.Refusal{Code: core.CodeBadRequest, Reason: "no manifest"})
	}
	m := reg.Manifest

	// 1. validate
	if ref := m.Validate(); ref != nil {
		return fail("validate", ref)
	}
	res.Steps = append(res.Steps, Step{
		Name: "validate", OK: true,
		Detail: string(m.Kind) + " / " + string(m.Evidence) + " / env " + m.Env.Interpreter,
	})

	// 2. deps must be present. A skill cannot depend on something the store
	// does not have, and a dep that cannot be read is worse than a missing one.
	var depLicenses []string
	for _, d := range m.Deps {
		_, dm, ref := s.Resolve(d)
		if ref != nil {
			return fail("deps", ref)
		}
		depLicenses = append(depLicenses, dm.License)
	}
	res.Steps = append(res.Steps, Step{
		Name: "deps", OK: true, Detail: strconv.Itoa(len(m.Deps)) + " resolved",
	})

	// 3. license closure
	cl, ref := SolveLicense(m.License, depLicenses)
	if ref != nil {
		return fail("license", ref)
	}
	if ref := cl.CheckPolicy(s.Pol); ref != nil {
		return fail("license", ref)
	}
	res.License = cl
	res.Steps = append(res.Steps, Step{
		Name: "license", OK: true,
		Detail: cl.Class.String() + " — " + cl.ObligationSummary(),
	})

	// 4. payload, checked before anything is written
	if ref := checkPayload(reg.Files, reg.Manifest.Kind); ref != nil {
		return fail("payload", ref)
	}
	names := make([]string, 0, len(reg.Files))
	for n := range reg.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	res.Steps = append(res.Steps, Step{
		Name: "payload", OK: true, Detail: strconv.Itoa(len(names)) + " files",
	})

	// 5. manifest blob
	mh, _, err := s.CAS.PutJSON(m)
	if err != nil {
		return fail("manifest", asRefusal(err))
	}
	declared, ref := m.ManifestHash()
	if ref != nil {
		return fail("manifest", ref)
	}
	if mh != declared {
		// The hash used as an address and the hash used as an identity must be
		// the same number. If they diverge, every ref in the store points at
		// something that is not what it claims.
		return fail("manifest", &core.Refusal{
			Code:   core.CodeRecoveryRequired,
			Reason: "declared manifest hash " + declared + " != stored hash " + mh,
		})
	}
	res.Manifest = mh
	res.Steps = append(res.Steps, Step{Name: "manifest", OK: true, Detail: mh})

	// 6. payload blobs
	idx := make(map[string]string, len(names))
	res.Files = make([]string, 0, len(names))
	for _, n := range names {
		h, err := s.CAS.Put(reg.Files[n])
		if err != nil {
			return fail("files", asRefusal(err))
		}
		idx[n] = h
		res.Files = append(res.Files, h)
	}
	res.Steps = append(res.Steps, Step{Name: "files", OK: true, Detail: strconv.Itoa(len(idx)) + " stored"})

	// 7. the package — the skill_ref
	pkg := &Package{Manifest: mh, Files: idx, License: cl, Evidence: m.Evidence}
	ph, _, err := s.CAS.PutJSON(pkg)
	if err != nil {
		return fail("package", asRefusal(err))
	}
	res.Ref = ph
	res.Evidence = m.Evidence
	res.Steps = append(res.Steps, Step{Name: "package", OK: true, Detail: ph})

	// 8. the ref moves last
	if ref := s.Refs.Set(m.SkillID, ChannelCanary, ph); ref != nil {
		return fail("ref", ref)
	}
	res.Channel = ChannelCanary
	res.Steps = append(res.Steps, Step{
		Name: "ref", OK: true, Detail: m.SkillID + "/" + ChannelCanary + " <- " + ph,
	})
	return res, nil
}

// Resolve reads a package and its manifest by skill_ref.
func (s *Store) Resolve(ref string) (*Package, *Manifest, *core.Refusal) {
	var pkg Package
	if err := s.CAS.GetJSON(ref, &pkg); err != nil {
		return nil, nil, &core.Refusal{
			Code:   core.CodeBadRequest,
			Reason: "not a skill_ref in this store: " + ref,
		}
	}
	var m Manifest
	if err := s.CAS.GetJSON(pkg.Manifest, &m); err != nil {
		return nil, nil, &core.Refusal{
			Code:   core.CodeRecoveryRequired,
			Reason: "package " + ref + " points at an unreadable manifest " + pkg.Manifest,
		}
	}
	return &pkg, &m, nil
}

// Promote moves a ref between channels, refusing to promote unverified work.
//
// E0 is generation without verification. Freezing it is the one thing the固化
// loop must not be allowed to do: a wrong skill that gets frozen is wrong
// forever, executed N times and recorded as a success each time.
func (s *Store) Promote(name, from, to string) *core.Refusal {
	ref, refusal := s.Refs.Get(name, from)
	if refusal != nil {
		return refusal
	}
	_, m, refusal := s.Resolve(ref)
	if refusal != nil {
		return refusal
	}
	if !m.Verified() {
		return &core.Refusal{
			Code: core.CodeBadRequest,
			Reason: "refusing to promote " + name + ": evidence is " + string(m.Evidence) +
				", and unverified work must not be frozen",
		}
	}
	return s.Refs.Set(name, to, ref)
}

// asRefusal preserves a structured refusal through an error return.
func asRefusal(err error) *core.Refusal {
	if r, ok := err.(*core.Refusal); ok {
		return r
	}
	return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
}
