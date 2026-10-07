package store

import (
	"encoding/json"

	"sansheng/core"
)

// Kind is a skill's execution form.
//
// It is a **scheduling key, not an identity**: "hot" is a state, not a type.
// A script becomes code once it is called often enough to pay for compiling,
// and the manifest's kind changes with it — the skill does not.
type Kind string

const (
	KindAlgorithm Kind = "algorithm" // built into the kernel's primitives
	KindCode      Kind = "code"      // compiled, sandboxed
	KindScript    Kind = "script"    // resident interpreter
	KindLLMAPI    Kind = "llm_api"   // outbound HTTP
	KindRLPolicy  Kind = "rl_policy" // inlined decision policy
	KindHuman     Kind = "human"     // async; never on the hot path
)

var kinds = map[Kind]bool{
	KindAlgorithm: true, KindCode: true, KindScript: true,
	KindLLMAPI: true, KindRLPolicy: true, KindHuman: true,
}

// Evidence is the trust level of a claim, and it travels **with** the claim.
//
// The levels are the whole defence of the固化 loop: generation → verification →
// freeze is a positive feedback loop, and a wrong answer that gets frozen is
// wrong forever, executed N times and recorded as a success each time.
type Evidence string

const (
	E0 Evidence = "E0" // model-generated, unverified — never enters core memory
	E1 Evidence = "E1" // ran once in a sandbox and succeeded
	E2 Evidence = "E2" // confirmed by a second, independent source
	E3 Evidence = "E3" // external authority, or a human
	E4 Evidence = "E4" // succeeded N times with no failure
)

var evidenceRank = map[Evidence]int{E0: 0, E1: 1, E2: 2, E3: 3, E4: 4}

// MinEvidence returns the weaker of two levels.
//
// Composition may lower trust and may never raise it. Stitching a pile of E1s
// into something that reads as E3 is the laundering route into the固化 loop,
// and this function is where that route is closed.
func MinEvidence(a, b Evidence) Evidence {
	if evidenceRank[a] <= evidenceRank[b] {
		return a
	}
	return b
}

// MinEvidenceAll folds MinEvidence over a set.
//
// It seeds from the first element rather than from E0 — because E0 *is* the
// minimum, and a fold that starts at the identity of `min` can only ever return
// that identity. An empty set still means E0: a composition with no verified
// children has no verified claim to make.
func MinEvidenceAll(evs []Evidence) Evidence {
	if len(evs) == 0 {
		return E0
	}
	out := evs[0]
	for _, e := range evs[1:] {
		out = MinEvidence(out, e)
	}
	return out
}

// Env is the environment closure a skill runs in.
//
// This is the whole reason a skill's identity is not its script file. The same
// .py under a different environment is a **different skill**, because it will
// produce different results. pip's dependency closure cannot be content
// addressed, so we pin the closure explicitly and hash that instead.
type Env struct {
	Interpreter string            `json:"interpreter"`
	Packages    map[string]string `json:"packages,omitempty"`
	Network     bool              `json:"network"`
}

// Provenance records where a skill came from.
//
// "Copying from GitHub" is a five-step process — find, judge, adapt, verify,
// fix in place — and this is the durable record of the first two. It is
// required, because a skill whose origin is unknown cannot be audited later.
type Provenance struct {
	Source     string `json:"source"`
	URL        string `json:"url,omitempty"`
	Commit     string `json:"commit,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`
}

// Manifest is the Skill ABI (docs/CONTRACT.md §5).
type Manifest struct {
	SkillID    string          `json:"skill_id"`
	Version    string          `json:"version"`
	Kind       Kind            `json:"kind"`
	Input      json.RawMessage `json:"input_schema,omitempty"`
	Output     json.RawMessage `json:"output_schema,omitempty"`
	Pre        []string        `json:"preconditions,omitempty"`
	Post       []string        `json:"postconditions,omitempty"`
	Verify     []string        `json:"verify_assert"`
	OnFailure  string          `json:"on_failure,omitempty"`
	Budget     core.Budget     `json:"budget"`
	Effects    []string        `json:"effects,omitempty"`
	Evidence   Evidence        `json:"evidence"`
	Idempotent bool            `json:"idempotent"`
	License    string          `json:"license"`
	Deps       []string        `json:"deps,omitempty"` // content hashes, never version numbers
	Provenance Provenance      `json:"provenance"`
	Env        Env             `json:"env"`
	Compose    *Composition    `json:"compose,omitempty"`
}

func bad(reason string) *core.Refusal {
	return &core.Refusal{Code: core.CodeBadRequest, Reason: reason}
}

// Validate refuses a manifest that cannot be trusted to mean one thing.
//
// It **fails closed**: anything it does not recognise is a refusal, never a
// default. Defaults are how a field that nobody filled in becomes a claim that
// nobody checked.
func (m *Manifest) Validate() *core.Refusal {
	if !ValidName(m.SkillID) {
		return bad("skill_id must match [a-z0-9][a-z0-9._-]*: " + m.SkillID)
	}
	if m.Version == "" {
		return bad("version is required")
	}
	if !kinds[m.Kind] {
		return bad("unknown kind: " + string(m.Kind))
	}
	if _, ok := evidenceRank[m.Evidence]; !ok {
		return bad("unknown evidence level: " + string(m.Evidence))
	}
	if len(m.Verify) == 0 {
		// Without an assertion there is nothing to verify against, and an
		// unverifiable skill can only ever report its own success.
		return bad("verify_assert is required: an unverifiable skill can only self-report")
	}
	if m.License == "" {
		return bad("license is required: unknown is not permissive")
	}
	if _, ok := licenseClass[m.License]; !ok {
		// Not bad(): an unrecognised license is a licensing refusal, and
		// SolveLicense reaches the same fact through the same constructor.
		// Two gates giving one fact two codes is how a client's retry loop
		// ends up working on one path and not the other.
		return UnknownLicenseRefusal(m.License)
	}
	for _, d := range m.Deps {
		if !ValidHash(d) {
			return bad("deps must be content hashes, not version numbers: " + d)
		}
	}
	if m.Env.Interpreter == "" {
		return bad("env.interpreter is required")
	}
	if m.Env.Network {
		return bad("env.network must be false: a skill that fetches at runtime is not reproducible")
	}
	if m.Provenance.Source == "" {
		return bad("provenance.source is required")
	}
	if m.Compose != nil {
		if ref := m.Compose.validate(); ref != nil {
			return ref
		}
	}
	return nil
}

// ManifestHash is the manifest's content address: its environment, its deps and
// its assertions, folded into one hash.
//
// This is the manifest's *address*, not the skill's identity. The `skill_ref`
// is the **package** hash (see Package in register.go), which covers this
// manifest, the payload and the resolved license closure. The manifest lives
// inside the package, so the package transitively covers everything here.
//
// Two manifests with the same hash declare the same thing, byte for byte, on
// any machine. That is the property the whole store rests on.
func (m *Manifest) ManifestHash() (string, *core.Refusal) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", bad(err.Error())
	}
	canon, ref := core.Canonical(raw)
	if ref != nil {
		return "", ref
	}
	return Hash(canon), nil
}

// Verified reports whether this skill's evidence is strong enough to be frozen
// into the stable channel. E0 is generation without verification, and it must
// never be promoted — that is the one rule the固化 loop cannot bend.
func (m *Manifest) Verified() bool {
	return evidenceRank[m.Evidence] >= evidenceRank[E1]
}
