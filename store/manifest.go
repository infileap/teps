package store

import (
	"encoding/json"

	"teps/core"
	"teps/evidence"
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
	// KindCompose is not an execution form at all: it is a *structure*.
	//
	// A composition runs no process of its own. Its children each execute in
	// their own declared form, and the composition's form is the graph that
	// connects them. That is why it is the one kind with no interpreter and no
	// payload: there is nothing for a sandbox to run.
	KindCompose Kind = "compose"
)

var kinds = map[Kind]bool{
	KindAlgorithm: true, KindCode: true, KindScript: true,
	KindLLMAPI: true, KindRLPolicy: true, KindHuman: true,
	KindCompose: true,
}

// Evidence is the trust level of a claim, and it travels **with** the claim.
//
// The ladder itself is defined in package evidence — see that package for why
// it is not defined here. These are **aliases, not copies**: a copy would
// compile, and would then be free to drift from the ladder the memory organ
// filters with.
type Evidence = evidence.Level

const (
	E0 = evidence.L0 // model-generated, unverified — never enters core memory
	E1 = evidence.L1 // ran once in a sandbox and succeeded
	E2 = evidence.L2 // confirmed by a second, independent source
	E3 = evidence.L3 // external authority, or a human
	E4 = evidence.L4 // succeeded N times with no failure
)

// MinEvidence and MinEvidenceAll keep their names as forwarders, so the store's
// tests keep exercising the ladder through the path its callers use.
func MinEvidence(a, b Evidence) Evidence     { return evidence.Min(a, b) }
func MinEvidenceAll(evs []Evidence) Evidence { return evidence.MinAll(evs) }

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
	if !evidence.Known(m.Evidence) {
		return bad("unknown evidence level: " + string(m.Evidence))
	}
	if m.Kind == KindCompose && len(m.Verify) != 0 {
		// A composition runs no process, so there is no observation of *its own*
		// for an assertion to be checked against. Accepting one would be
		// accepting a claim nothing reads.
		//
		// The follow-up that would make this field meaningful: check the
		// composition's assertion against its final observable, which for a
		// serial composition is the last step's stdout. That is not implemented
		// yet, and until it is, an assertion here would be decoration.
		return bad("kind=compose declares no verify_assert: a composition produces no observation of its own, and the children already carry theirs")
	}
	if m.Kind != KindCompose && len(m.Verify) == 0 {
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
	if m.Kind != KindCompose && m.Env.Interpreter == "" {
		// A composition runs no process, so it has no interpreter to declare.
		// Every other kind does: exec.Command resolves a bare name against the
		// *server's* PATH, so an undeclared interpreter means the same ref runs
		// a different program on a different machine.
		return bad("env.interpreter is required")
	}
	if m.Kind == KindCompose && m.Env.Interpreter != "" {
		// Same rule as the payload: a composition runs no process, so an
		// interpreter here is a claim nothing checks.
		return bad("kind=compose declares no interpreter: it runs no process of its own, so this field would never be read")
	}
	if m.Env.Network {
		return bad("env.network must be false: a skill that fetches at runtime is not reproducible")
	}
	if m.Provenance.Source == "" {
		return bad("provenance.source is required")
	}
	if m.Kind == KindCompose && m.Compose == nil {
		return bad("kind=compose requires a compose declaration: the kind says how a skill runs, and this one runs as a graph")
	}
	if m.Compose != nil {
		if ref := m.Compose.validate(); ref != nil {
			return ref
		}
		if m.Kind != KindCompose {
			return bad("a compose declaration is only meaningful on kind=compose: on kind=" + string(m.Kind) + " it would be a graph nothing ever walks")
		}
		// Every child must be declared as a dependency. A composition whose
		// closure does not name what it calls cannot be checked for licences,
		// and an unchecked licence closure is how a GPL file gets shipped in a
		// proprietary bundle.
		declared := map[string]bool{}
		for _, d := range m.Deps {
			declared[d] = true
		}
		for i, child := range m.Compose.Children {
			if !declared[child] {
				return bad("compose child " + itoa(i) + " is not in deps: a composition must declare its closure, and " + child + " is missing")
			}
		}
	}
	if m.Kind == KindCompose && len(m.Deps) == 0 {
		return bad("kind=compose needs deps: a composition of nothing would succeed by having done nothing")
	}
	return nil
}

// itoa is strconv.Itoa without the import, so this file's import list stays a
// statement about what the manifest actually needs.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
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
	return evidence.AtLeast(m.Evidence, E1)
}
