package main

import (
	"encoding/json"
	"sort"
	"strconv"

	"sansheng/core"
	"sansheng/organs/executor"
	"sansheng/store"
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
	if f.T != "skill.compose" {
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

	comp := executor.Composition{
		Op:            req.Composition.Op,
		Children:      req.Composition.Children,
		MaxAttempts:   req.Composition.MaxAttempts,
		MaxIterations: req.Composition.MaxIterations,
	}
	for _, w := range req.Composition.Wires {
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

	// The composition's intent, before anything runs.
	seq, head, open, refusal := s.observe(req.Attempt+keyIntent, observeBody{
		Attempt: req.Attempt, Skill: composeSkillName(graph.Op), TaskClass: req.TaskClass,
		Strategy: strategyCompose,
	})
	if refusal != nil {
		out.Err = refusal
		return true
	}
	res := composeResult{
		Attempt: req.Attempt, Op: graph.Op,
		Intent: &chainRef{Seq: seq, Head: head},
		Steps:  []composeStep{},
		Open:   open,
	}

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
				Latency: float64(obs.LatencyMS),
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
		seq, head, open, refusal := s.observe(req.Attempt+keyResolution, observeBody{
			Attempt: req.Attempt, Skill: composeSkillName(graph.Op), TaskClass: req.TaskClass,
			Strategy: strategyCompose, Outcome: outcome, Verified: total.VerifiedBy,
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
