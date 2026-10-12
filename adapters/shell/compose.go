package main

import (
	"encoding/json"
	"sort"
	"strconv"

	"teps/core"
	"teps/organs/executor"
	"teps/organs/planner"
	"teps/store"
)

// skill.compose runs a composition: it resolves the children, runs them in the
// order the planner produced, feeds each step's stdout into the next step's
// stdin, judges every child independently, and then reads the whole thing.
//
// The split is the same one as skill.invoke. organs/executor plans, wires and
// judges (pure); this file spawns processes, spends budget and writes the chain.
//
// Two things here are deliberately not optimised:
//
//   - The composition's own intent is written BEFORE the first child runs. If
//     the process dies halfway, the chain shows a composition that was started
//     and never concluded. That is the honest record, and it is the same reason
//     an unjudgeable invoke leaves its intent open.
//   - Every child is resolved and planned before any child runs. A composition
//     whose third child does not exist must not run its first two and then
//     discover the typo.

// 组合与 invoke 共用一组 attempt 键，一字不差。
//
// 这不是省事：s.lookupAttempt(attempt) 找的是 attempt+keyIntent 与
// attempt+keyResolution。组合若用自己的后缀，回放这道守卫就永远找不到
// 组合的意图，于是「同一次尝试」会被跑第二遍——而第二次会撞上子步骤
// 已经闭合的 attempt 才被发现。共用命名空间就必须共用键的约定。
const strategyCompose = "skill.compose"

type composeWire struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type composeBody struct {
	Composition struct {
		Op            string        `json:"op"`
		Children      []string      `json:"children"`
		Wires         []composeWire `json:"wires"`
		MaxAttempts   int           `json:"max_attempts"`
		MaxIterations int           `json:"max_iterations"`
	} `json:"composition"`
	// Ref names a *published* composition. It is the reason composition is
	// recursive rather than one level deep: a composition that has been
	// registered is an ordinary skill with an ordinary content address, so it
	// can be a child of another composition.
	Ref       string          `json:"ref"`
	Attempt   string          `json:"attempt"`
	TaskClass string          `json:"task_class"`
	Input     json.RawMessage `json:"input"`
	Elevate   bool            `json:"elevate"`
}

type composeStep struct {
	Index     int    `json:"index"`
	Ref       string `json:"ref"`
	Attempt   string `json:"attempt"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
	LatencyMS int64  `json:"latency_ms"`
	// FedTruncated records that this step's input was cut short upstream. It is
	// reported rather than hidden: a step fed half a stream may well have
	// "succeeded" at something other than what was asked.
	FedTruncated bool `json:"fed_truncated,omitempty"`
}

type composeResult struct {
	Attempt    string        `json:"attempt"`
	Op         string        `json:"op"`
	Replayed   bool          `json:"replayed,omitempty"`
	Intent     *chainRef     `json:"intent,omitempty"`
	Resolution *chainRef     `json:"resolution,omitempty"`
	Outcome    string        `json:"outcome"`
	VerifiedBy string        `json:"verified_by"`
	Reason     string        `json:"reason"`
	Steps      []composeStep `json:"steps"`
	Open       int           `json:"open"`
	// Ceiling is the most this composition could ever claim: the minimum of its
	// children's levels. It is reported rather than left implicit because
	// "why is this still E0" is a question a reader should not have to answer
	// by reading four manifests.
	Ceiling string `json:"ceiling,omitempty"`
}

// composeSkillName names a composition in the posterior table.
//
// A composition is not published yet, so it has no ref and no identity of its
// own; its reliability is grouped by shape. That is a real limitation — two
// different serial compositions share one posterior. It goes away the day a
// composition can be registered as a skill and get a ref of its own, and it is
// written down here so that day is not missed.
func composeSkillName(op string) string { return "compose:" + op }

// handleSkillCompose claims skill.compose.
func (s *server) handleSkillCompose(f core.Frame, out *core.Frame) bool {
	switch f.T {
	case "compose.propose":
		if s.skills == nil {
			out.Err = core.Refuse(core.CodeBadRequest, "no skill store is attached")
			return true
		}
		s.handlePropose(f, out)
		return true
	case "skill.compose":
	default:
		return false
	}
	if s.skills == nil {
		out.Err = core.Refuse(core.CodeBadRequest, "no skill store is attached")
		return true
	}
	var req composeBody
	if refusal := decodeBody(f.Body, &req); refusal != nil {
		out.Err = refusal
		return true
	}
	if req.Attempt == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"a composition needs an attempt id: without it the execution can never be resolved")
		return true
	}
	if req.TaskClass == "" {
		out.Err = core.Refuse(core.CodeBadRequest,
			"a composition needs a task_class: an execution with no task class cannot be priced or compared")
		return true
	}

	// A reused attempt is replayed, never re-run.
	if intent, resolution, refusal := s.lookupAttempt(req.Attempt); refusal != nil {
		out.Err = refusal
		return true
	} else if intent != nil {
		if resolution == nil {
			out.Err = core.Refuse(core.CodeBadRequest,
				"attempt "+req.Attempt+" has an intent on the chain and no resolution (the contract calls that unknown): adjudicate it explicitly, then use a new attempt id")
			return true
		}
		out.Seq, out.Rev = resolution.Seq, resolution.Seq
		out.Body = mustJSON(composeResult{
			Attempt: req.Attempt, Replayed: true, Outcome: "replayed", VerifiedBy: executor.VerifierName,
			Intent:     &chainRef{Seq: intent.Seq, Head: intent.Head},
			Resolution: &chainRef{Seq: resolution.Seq, Head: resolution.Head},
			Reason:     "this attempt already has a resolution on the chain; it is replayed rather than run a second time",
			Steps:      []composeStep{},
		})
		return true
	}

	spec := req.Composition
	// identity is what the posterior is grouped by, and it is a **name**, not a
	// ref — the same rule skill.invoke follows.
	//
	// Keying by ref looks more precise and is worse: elevation re-registers the
	// same payload as evidence=E1, which produces a *new* ref. A ref-keyed
	// posterior would therefore reset exactly when a skill was promoted, and the
	// runs that justified the promotion would be the ones erased. The chain
	// still records the ref on every observation, so "which implementation was
	// this" stays answerable; the posterior answers a different question, which
	// is "how does this capability behave".
	//
	// The cost is real and worth naming: two unrelated implementations that
	// share a skill_id share one posterior. That is a naming problem, and the
	// audit trail is where it shows up.
	//
	// An inline composition has no name of its own, so it is grouped by shape.
	//
	// Shape used to mean "the operator", which folded two unrelated serial
	// chains into one posterior. That is the same defect as averaging two rule
	// sets: 0.5 can mean "one chain that works half the time" or "two chains,
	// one that always works and one that never does". The shape identity was
	// already available and already used by the planner, so the cost that note
	// described is not a cost any more.
	//
	// The identity is computed from the children's **skill ids**, not their
	// refs: a ref changes when a child is re-published, and an identity that
	// moves on re-publication is the ref-keying mistake wearing a different hat.
	identity := composeSkillName(req.Composition.Op)
	if req.Ref != "" {
		if len(req.Composition.Children) != 0 {
			out.Err = core.Refuse(core.CodeBadRequest,
				"pass either ref or an inline composition, not both: with both present there is no way to tell which one ran")
			return true
		}
		_, man, refusal := s.skills.Resolve(req.Ref)
		if refusal != nil {
			out.Err = refusal
			return true
		}
		if man.Kind != store.KindCompose || man.Compose == nil {
			out.Err = core.Refuse(core.CodeBadRequest,
				"ref "+req.Ref+" is kind="+string(man.Kind)+" and carries no graph: skill.compose runs a graph, and this ref has none")
			return true
		}
		spec.Op = string(man.Compose.Op)
		spec.Children = man.Compose.Children
		spec.Wires = nil
		for _, w := range man.Compose.Wires {
			spec.Wires = append(spec.Wires, composeWire{From: w.From, To: w.To})
		}
		spec.MaxAttempts = man.Compose.MaxAttempts
		spec.MaxIterations = man.Compose.MaxIterations
		// Grouped by the composition's own name, exactly as skill.invoke groups
		// by skill_id — see the note on `identity` below.
		identity = man.SkillID
	}

	// A composition is a skill, so it can be a child. Expand any child that is
	// itself a composition into its own children, recursively.
	children, refusal := s.expandChildren(spec.Children, 0)
	if refusal != nil {
		out.Err = refusal
		return true
	}

	comp := executor.Composition{
		Op:            spec.Op,
		Children:      children,
		MaxAttempts:   spec.MaxAttempts,
		MaxIterations: spec.MaxIterations,
	}
	for _, w := range spec.Wires {
		comp.Wires = append(comp.Wires, executor.Wire{From: w.From, To: w.To})
	}
	graph, refusal := executor.PlanComposition(comp)
	if refusal != nil {
		out.Err = refusal
		return true
	}

	// Resolve and plan every child before running any of them. A plan built with
	// a placeholder input is enough to catch a declaration that cannot run at
	// all; the real input only exists once the previous step has produced it.
	type prepared struct {
		pkg  *store.Package
		man  *store.Manifest
		decl executor.Declaration
	}
	preps := make([]prepared, 0, len(graph.Steps))
	for _, step := range graph.Steps {
		pkg, man, refusal := s.skills.Resolve(step.Ref)
		if refusal != nil {
			out.Err = core.Refuse(refusal.Code,
				"child "+strconv.Itoa(step.Index)+" cannot be resolved: "+refusal.Reason)
			return true
		}
		decl := executor.Declaration{
			SkillID:     man.SkillID,
			Kind:        string(man.Kind),
			Interpreter: man.Env.Interpreter,
			Files:       payloadNames(pkg),
			Assert:      man.Verify,
			BudgetMS:    man.Budget.MS,
			Input:       req.Input,
			Raw:         step.StdinFrom == "prev",
		}
		if _, refusal := executor.NewPlan(decl); refusal != nil {
			out.Err = core.Refuse(refusal.Code,
				"child "+strconv.Itoa(step.Index)+" cannot be planned: "+refusal.Reason)
			return true
		}
		preps = append(preps, prepared{pkg: pkg, man: man, decl: decl})
	}

	// ★ 组合的等级天花板：孩子里最弱的那个。
	//
	// 它在这里算出来，是为了让下面那条拒绝能说出**具体**的数，而不是一句
	// 「组合不能提级」——一个说不出上限的拒绝，读者不知道该往哪儿走。
	ceiling := store.E0
	{
		evs := make([]store.Evidence, 0, len(preps))
		for _, pr := range preps {
			evs = append(evs, pr.man.Evidence)
		}
		ceiling = store.MinEvidenceAll(evs)
	}

	// ★ `elevate` 曾经在这里被接受然后**静默忽略**。
	//
	// composeBody 有 Elevate 字段，而 composeResult 里没有 elevation，代码里也
	// 没有任何地方读它 —— 一个调用方传了 elevate: true，拿到 200，什么都没发生。
	// 那是最坏的一种 API 谎言：不是拒绝，也不是做到，是**看起来做到了**。
	//
	// 真话是：组合没有自己的断言。它的判定是 JudgeComposition 对孩子们判定的
	// 合成，而不是对组合自己输出的核验 —— 所以没有一条「应该不成立」的断言
	// 可以拿来给它自己挣证据。它的等级就是孩子们给它设的天花板。
	//
	// 拒绝要说清三件事：为什么、上限是多少、以及真正的路在哪。
	if req.Elevate {
		out.Err = core.Refuse(core.CodeBadRequest,
			"elevate is not something a composition can do: a composition declares no verify_assert and no verify_refute, "+
				"because its judgement is a composition of its children's judgements rather than a verification of its own output — "+
				"there is no assertion of its own for a run to earn evidence against. Its level is the ceiling its children set, which for this "+
				strconv.Itoa(len(preps))+" children is "+string(ceiling)+". Raise the children, and this composition's ceiling rises with them.")
		return true
	}

	if req.Ref == "" {
		// Inline: the shape is the only identity available, so make it a real
		// one — and compute it here, after the children have been resolved, so
		// an unresolvable child is refused by the guard above with its own
		// message rather than by this.
		//
		// The children's **skill ids**, not their refs: a ref changes when a
		// child is re-published, and an identity that moves on re-publication is
		// the ref-keying mistake wearing a different hat.
		ids := make([]string, 0, len(preps))
		for _, pr := range preps {
			ids = append(ids, pr.man.SkillID)
		}
		identity = composeSkillName(spec.Op) + ":" + planner.ShapeID(spec.Op, ids)[:12]
	}

	// The composition's intent, before anything runs.
	seq, head, open, refusal := s.observe(req.Attempt+keyIntent, observeBody{
		Attempt: req.Attempt, Skill: identity, TaskClass: req.TaskClass,
		Strategy: strategyCompose,
	})
	if refusal != nil {
		out.Err = refusal
		return true
	}
	res := composeResult{
		Attempt: req.Attempt, Op: graph.Op,
		Intent:  &chainRef{Seq: seq, Head: head},
		Steps:   []composeStep{},
		Open:    open,
		Ceiling: string(ceiling),
	}

	// stepRules 收集每一步自己的规则身份。组合没有自己的断言，
	// 它被判定所依据的规则就是孩子们各自的那组断言。
	var stepRules []string

	// Run the graph.
	var prevOut []byte
	prevTruncated := false
	judgements := make([]executor.Judgement, 0, len(graph.Steps))
	for i, step := range graph.Steps {
		attempt := req.Attempt + "#" + strconv.Itoa(i)
		input := req.Input
		fedTruncated := false
		if step.StdinFrom == "prev" {
			input = prevOut
			fedTruncated = prevTruncated
		}
		prep := preps[i]
		decl := prep.decl
		decl.Input = input
		// ★ 串行步的输入是上游进程的原始字节，不是 API 输入。
		// 拿 JSON 规范化器去处理它，会把第一个 JSON 值之后的一切静默丢掉。
		decl.Raw = step.StdinFrom == "prev"

		plan, refusal := executor.NewPlan(decl)
		if refusal != nil {
			out.Err = refusal
			return true
		}
		stepRules = append(stepRules, plan.Rules)
		if refusal := s.acct.Spend(core.Budget{MS: plan.TimeoutMS}); refusal != nil {
			out.Err = refusal
			return true
		}

		if _, _, _, refusal := s.observe(attempt+keyIntent, observeBody{
			Attempt: attempt, Skill: prep.man.SkillID, TaskClass: req.TaskClass,
			Strategy: strategyInvoke, Latency: 0,
		}); refusal != nil {
			out.Err = refusal
			return true
		}

		obs := s.runSandbox(plan, prep.pkg)
		j := executor.Judge(plan.Assert, obs)
		row := composeStep{
			Index: i, Ref: step.Ref, Attempt: attempt,
			Outcome: string(j.Verdict), Reason: j.Reason,
			LatencyMS: obs.LatencyMS, FedTruncated: fedTruncated,
		}
		// A step fed a truncated stream cannot be judged: it may have succeeded
		// at something other than what was asked. This is the input-side twin of
		// the rule that a truncated observation may not be used to judge false.
		if fedTruncated {
			j = executor.Judgement{
				Verdict:    executor.VerdictUnknown,
				VerifiedBy: executor.VerifierName,
				Reason:     "this step's input was cut short upstream: no assertion can speak for a stream that is not the whole one",
			}
			row.Outcome = string(j.Verdict)
			row.Reason = j.Reason
		}
		if outcome, write := j.Verdict.Outcome(); write {
			if _, _, _, refusal := s.observe(attempt+keyResolution, observeBody{
				Attempt: attempt, Skill: prep.man.SkillID, TaskClass: req.TaskClass,
				Strategy: strategyInvoke, Outcome: outcome, Verified: j.VerifiedBy,
				Latency: float64(obs.LatencyMS), Rules: plan.Rules,
			}); refusal != nil {
				out.Err = refusal
				return true
			}
		} else {
			row.Reason = j.Reason + " — this step is left open on the chain on purpose"
		}
		judgements = append(judgements, j)
		res.Steps = append(res.Steps, row)

		prevOut = []byte(obs.Stdout)
		prevTruncated = obs.StdoutTruncated
	}

	total := executor.JudgeComposition(judgements)
	res.Outcome = string(total.Verdict)
	res.VerifiedBy = total.VerifiedBy
	res.Reason = total.Reason
	if outcome, write := total.Verdict.Outcome(); write {
		// identity, not composeSkillName: the intent and the resolution must name
		// the same thing, or the posterior is opened under one key and closed
		// under another — and only the resolution is what the table counts.
		// 组合没有自己的断言（契约禁止 compose 声明 verify_assert），
		// 它被判定所依据的规则**就是孩子们各自的那组断言**。
		// 用孩子们的身份算一个身份出来：孩子换了判定规则，组合的规则也就换了。
		seq, head, open, refusal := s.observe(req.Attempt+keyResolution, observeBody{
			Attempt: req.Attempt, Skill: identity, TaskClass: req.TaskClass,
			Strategy: strategyCompose, Outcome: outcome, Verified: total.VerifiedBy,
			Rules: executor.RuleID(stepRules),
		})
		if refusal != nil {
			out.Err = refusal
			return true
		}
		res.Resolution = &chainRef{Seq: seq, Head: head}
		res.Open = open
		out.Seq, out.Rev = seq, seq
	} else {
		res.Reason = total.Reason + " — the composition is left open on the chain on purpose"
		out.Seq, out.Rev = seq, seq
	}
	out.Body = mustJSON(res)
	return true
}

// payloadNames returns a payload's file names in a stable order.
//
// map iteration is random in Go, and a plan built from a randomly ordered file
// list is a plan that differs between two runs of the same ref.
func payloadNames(pkg *store.Package) []string {
	names := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// maxComposeDepth bounds how deep an expansion may go.
//
// Content addressing already makes a cycle impossible: a manifest carries its
// children's *package hashes*, so a self-reference would need its own hash to be
// known before it exists — a fixed point that does not exist. An indirect cycle
// fails for the same reason at every step.
//
// So this bound is a resource account, not a correctness one. Each level
// multiplies the work, and refusing is a better failure than exhausting the
// machine.
const maxComposeDepth = 8

// expandChildren replaces a child that is itself a composition with its own
// children, recursively.
//
// Only a **serial** composition can stand in for a single step. A parallel one's
// output is a collection rather than a stream, and splicing a collection into a
// stream is exactly the silent data loss the wire check exists to prevent. So it
// is refused by name instead of approximated.
func (s *server) expandChildren(refs []string, depth int) ([]string, *core.Refusal) {
	if depth > maxComposeDepth {
		return nil, core.Refuse(core.CodeBadRequest,
			"composition nested deeper than "+strconv.Itoa(maxComposeDepth)+
				" levels: this bound is a resource account, not a cycle check (a cycle is already impossible, because a manifest carries its children's hashes)")
	}
	out := make([]string, 0, len(refs))
	for i, ref := range refs {
		_, man, refusal := s.skills.Resolve(ref)
		if refusal != nil {
			return nil, core.Refuse(refusal.Code,
				"child "+strconv.Itoa(i)+" cannot be resolved: "+refusal.Reason)
		}
		if man.Kind != store.KindCompose || man.Compose == nil {
			out = append(out, ref)
			continue
		}
		if man.Compose.Op != store.OpSerial {
			return nil, core.Refuse(core.CodeBadRequest,
				"child "+strconv.Itoa(i)+" is a "+string(man.Compose.Op)+
					" composition: only a serial one can stand in for a single step, because a "+
					string(man.Compose.Op)+
					" composition produces a collection, and flattening a collection into a stream is silent data loss")
		}
		sub, refusal := s.expandChildren(man.Compose.Children, depth+1)
		if refusal != nil {
			return nil, refusal
		}
		out = append(out, sub...)
	}
	return out, nil
}
