package store

import (
	"encoding/json"
	"strings"

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
	Interpreter string `json:"interpreter"`
	// DEAD-API: 解释器版本由 Env 的其它字段决定，这一张表只被写、从没被读
	Packages map[string]string `json:"packages,omitempty"`
	// Network is true when the skill reaches **another host**.
	//
	// It is refused, and the reason is reproducibility rather than safety: an
	// answer that depends on what a remote machine says today is not
	// reproducible, while the content hash still claims the ref names one thing.
	//
	// This field used to be the whole story, and that was wrong in one
	// direction. "Talks to a socket" covers two situations that differ in the
	// only way that matters here:
	//
	//   - a remote host: not pinned, so not reproducible -> refused
	//   - a socket on this machine: the thing behind it is part of this
	//     machine's state, and a model file on this disk is pinned by its own
	//     bytes -> reproducible, and refusing it made a local model
	//     unpublishable
	//
	// Reaches is where the second case goes.
	Network bool `json:"network"`
	// Reaches names the local endpoints this skill talks to, each pinned by the
	// hash of the thing behind it.
	//
	// "Talks to a local socket" without saying which one is a claim nothing
	// checks, so each entry carries the artifact's digest. The store cannot
	// verify that the file exists — it is outside the store — so this is a
	// declaration like provenance.source, and it is treated as one: written
	// down, required, and never mistaken for a check.
	Reaches []Reach `json:"reaches,omitempty"`
}

// Reach is one local endpoint, pinned by what is behind it.
type Reach struct {
	// Socket is the endpoint: "unix:<path>", or "tcp:" on a loopback address.
	Socket string `json:"socket"`
	// Artifact is the content hash of the thing served there — a model file,
	// most often. It is what makes a local reach reproducible instead of merely
	// nearby.
	Artifact string `json:"artifact"`
}

// validate refuses a reach that is not actually local, or not actually pinned.
//
// Both refusals are about the same thing: a manifest that says "local" while
// naming something reachable, or names a socket without naming what is behind
// it, is a claim nothing can check.
func (e Env) validate() *core.Refusal {
	seen := map[string]bool{}
	for i, r := range e.Reaches {
		if r.Socket == "" {
			return bad("env.reaches[" + itoa(i) + "].socket is empty: a local reach with nothing named is a claim nothing checks")
		}
		if !isLoopback(r.Socket) {
			return bad("env.reaches[" + itoa(i) + "].socket names " + r.Socket +
				": that is not a loopback endpoint, so this is a remote reach wearing the local label — set env.network instead, and expect it to be refused")
		}
		if seen[r.Socket] {
			return bad("env.reaches[" + itoa(i) + "].socket repeats " + r.Socket + ": one endpoint, one entry")
		}
		seen[r.Socket] = true
		if r.Artifact == "" {
			return bad("env.reaches[" + itoa(i) + "].artifact is empty: without the hash of what is behind the socket, \"local\" is a claim nothing checks")
		}
		if _, _, ok := ValidDigest(r.Artifact); !ok {
			return bad("env.reaches[" + itoa(i) + "].artifact is not a content hash: " + r.Artifact)
		}
	}
	return nil
}

// isLoopback reports whether a socket spec names this machine only.
//
// It is deliberately a whitelist. A blacklist would let "tcp:0.0.0.0:8080" and
// every future spelling of a routable address through, and the failure it
// guards is one where the manifest says local and the bytes go somewhere else.
func isLoopback(spec string) bool {
	if rest, ok := strings.CutPrefix(spec, "unix:"); ok {
		return rest != ""
	}
	host, ok := strings.CutPrefix(spec, "tcp:")
	if !ok {
		return false
	}
	// Strip the port; the host is what decides.
	i := strings.LastIndex(host, ":")
	if i < 0 {
		return false
	}
	port := host[i+1:]
	if port == "" {
		return false
	}
	host = host[:i]
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// Provenance records where a skill came from.
//
// "Copying from GitHub" is a five-step process — find, judge, adapt, verify,
// fix in place — and this is the durable record of the first two. It is
// required, because a skill whose origin is unknown cannot be audited later.
type Provenance struct {
	Source string `json:"source"`
	// DEAD-API: 出处只被写、从没被读 —— 一条没人读的出处不是出处
	URL string `json:"url,omitempty"`
	// DEAD-API: 同上
	Commit string `json:"commit,omitempty"`
	// DEAD-API: 同上
	CapturedAt string `json:"captured_at,omitempty"`
}

// Manifest is the Skill ABI (docs/CONTRACT.md §5).
type Manifest struct {
	SkillID string          `json:"skill_id"`
	Version string          `json:"version"`
	Kind    Kind            `json:"kind"`
	Input   json.RawMessage `json:"input_schema,omitempty"`
	Output  json.RawMessage `json:"output_schema,omitempty"`
	// ★★★ Pre 与 Post 是**契约点名过、而这一层没有定义过**的两个字段。
	//
	// `docs/CONTRACT.md:153` 的 Skill ABI 那一行写着 `preconditions  postconditions`，
	// 而实测：这两个名字在整个仓库里**只出现在这一行结构体声明里** ——
	// 没有文档注释、没有 `Validate` 判据、没有任何地方读它们。
	// 它们会被 `skill.resolve` 报出去（整个清单都被序列化），仅此而已。
	//
	// ★ 一个接入方读着 ABI 看见 `preconditions`，**无从知道它是什么形状、
	// 更不知道它会不会被检查**。而这一行旁边的每一个字段都有注释 ——
	// 缺的那两个正好是没人执行的那两个。
	//
	// ★ 处置与 `docs/CONTRACT.md` 里「契约承诺但尚未实现的帧」那一张表一致：
	// **明确标出来的缺口不是静默漂移**。所以它们留在这里，但带着这句话。
	Pre    []string `json:"preconditions,omitempty"`
	Post   []string `json:"postconditions,omitempty"`
	Verify []string `json:"verify_assert"`
	// Refute is the other half of the evidence: cases where the skill must
	// **not** pass.
	//
	// verify_assert alone is one-sided, and one-sided evidence cannot tell
	// "correctly passes" from "always passes". A skill whose assertion is
	// `exit == 0` and whose payload always exits 0 has been shown to pass and
	// has not been shown to be able to fail — and "cannot fail" is not a
	// property anybody wants in a skill they are about to trust.
	//
	// So E1 requires both halves. This field is what makes the second half a
	// claim the verifier can check rather than a sentence in a README.
	Refute []Refutation `json:"verify_refute,omitempty"`
	// ★ OnFailure 与 Effects 同上：契约点名（`on_failure` / `effects[]`），
	// 清单里带着，`skill.resolve` 报得出来，而**没有一处会读它们**。
	//
	// ★ `effects` 尤其值得说一句：它声明的是「这个技能会碰什么」（网络、文件系统、
	// 进程…），而那正是 `Env.Network` / `Env.Reaches` 在做的事 ——
	// **同一个事实的两个入口，而只有后一个被检查**。
	OnFailure  string       `json:"on_failure,omitempty"`
	Budget     core.Budget  `json:"budget"`
	Effects    []string     `json:"effects,omitempty"`
	Evidence   Evidence     `json:"evidence"`
	Idempotent bool         `json:"idempotent"`
	License    string       `json:"license"`
	Deps       []string     `json:"deps,omitempty"` // content hashes, never version numbers
	Provenance Provenance   `json:"provenance"`
	Env        Env          `json:"env"`
	Compose    *Composition `json:"compose,omitempty"`
}

// Refutation is one must-fail case.
//
// The skill is run with Input, and Assert must **not** hold on that run. If it
// does hold, the skill passed a case it was supposed to fail — which means the
// assertion does not discriminate, and the skill has not been verified.
type Refutation struct {
	Input  json.RawMessage `json:"input,omitempty"`
	Assert string          `json:"assert"`
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
	// ★★★ 一个**声明了却没人执行**的字段是一句装饰。
	//
	// `Effects` 与 `Idempotent` 在 `docs/CONTRACT.md:157` 的 Skill ABI 那一行里
	// 是契约承诺过的字段（`effects[]  evidence  idempotent  license`）。
	// 实测（`skill.publish` 带 `idempotent:false` 与 `effects:["network","filesystem"]`，
	// 再 `skill.resolve`）：**两个都被报出来了** —— 所以它们不是「没人读」，
	// 它们是「**被读出来、却从来不被执行**」。
	//
	// ★ 而 `idempotent` 尤其刺眼：契约 A2 说「所有变更幂等」，内核是靠 `attempt`
	// 那把键做到的 —— 也就是说**一个声明 `idempotent: false` 的技能，在声称一件
	// 内核不会兑现的事**，而系统照样发布它、照样按幂等的方式跑它。
	// **声明与行为不一致，而不一致是静默的。**
	//
	// 上面那条 `kind=compose` 的判据逐字写着同一个理由：
	// 「a claim nothing reads」/「would be decoration」。
	if !m.Idempotent {
		return bad("idempotent must be true: the ABI makes every change idempotent by the attempt key (A2), so a skill declaring otherwise is claiming something the kernel will not honour — the declaration would be decoration")
	}
	if m.Kind == KindCompose && len(m.Refute) != 0 {
		// Same reason as verify_assert: a composition produces no observation
		// of its own, so a must-fail case here would be a claim nothing reads.
		return bad("kind=compose declares no verify_refute: a composition produces no observation of its own")
	}
	for i, r := range m.Refute {
		if strings.TrimSpace(r.Assert) == "" {
			return bad("verify_refute[" + itoa(i) + "] has no assert: a must-fail case with nothing to fail has no content")
		}
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
		return bad("env.network must be false: a skill that reaches another host at runtime is not reproducible — the answer depends on what that host says today, and the content hash still claims this ref names one thing")
	}
	if ref := m.Env.validate(); ref != nil {
		return ref
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

// evidenceRank is the forwarder for evidence.Rank, so this file can compare two
// levels without importing the ladder directly — the store reads the ladder
// through exactly one door (manifest.go), which is what keeps the store and the
// memory from drifting apart on what E2 means.
//
// ★★★ 它此前**丢掉了 `Rank` 的第二个返回值**：`r, _ := evidence.Rank(l)`。
// 未知等级因此落到 0，而 0 正是 E0 的秩。`Rank` 的注释逐字写着那件事不许发生：
//
//	An unknown level is deliberately **not** mapped to zero. Zero is a real level
//	(E0), and folding "E9" onto it would turn a typo into a trust decision.
//
// 也就是说：**这个前向器把它所前向的那个函数的、写在注释里的唯一一条禁令，
// 恰好执行反了。** 它今天还不可达（`Validate` 拦住未知的声明值，孩子又都过了
// `Validate`），但一个「读起来是 A、底下是 B」的转发器不需要等到可达才算错：
// 下一个人加一条旁路，它就活了。
//
// 现在它返回 `(秩, 在梯子上吗)`，而调用方必须**两边都读得懂**才比较 ——
// 一个读不懂的等级必须让检查更严，不是更松。
func evidenceRank(l Evidence) (int, bool) {
	return evidence.Rank(l)
}
