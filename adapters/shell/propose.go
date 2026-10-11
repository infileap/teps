package main

import (
	"encoding/json"
	"sort"
	"strings"

	"teps/core"
	"teps/organs/arbiter"
	"teps/organs/planner"
)

// compose.propose searches for compositions nobody wrote.
//
// ★ It proposes; it does not publish. A proposal is registered with
// skill.publish like any other skill, because a composition **is** a skill —
// and giving it its own publishing op would be a second way to do one thing.
//
// That was not a guess. This op was first written as compose.build, and the
// frame type collided: organs/composer already answers to compose.build for
// building a context window. A frame type is a global namespace, and the fix
// was not to rename this one — it was to notice this one was redundant.
//
// The op does not run anything either. A proposal is a claim about types, and
// only running it can say whether the claim was worth making.

type proposeBody struct {
	From         []string `json:"from"`
	To           []string `json:"to"`
	TaskClass    string   `json:"task_class"`
	MaxDepth     int      `json:"max_depth"`
	Floor        float64  `json:"floor"`
	MaxProposals int      `json:"max_proposals"`
}

// typeTokens reads a declared schema as a set of type names.
//
// A real JSON Schema would need a subsumption check. Pretending to do that with
// a list of strings would be worse than admitting what this is: the planner's
// notion of a type is a token, and a declaration that is not a list of tokens is
// one the planner refuses to reason about rather than guesses at.
func typeTokens(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var toks []string
	if err := json.Unmarshal(raw, &toks); err != nil {
		return nil, false
	}
	if len(toks) == 0 {
		return nil, false
	}
	for _, t := range toks {
		if strings.TrimSpace(t) == "" {
			return nil, false
		}
	}
	sort.Strings(toks)
	return toks, true
}

func (s *server) handlePropose(f core.Frame, out *core.Frame) {
	var body proposeBody
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	if len(body.From) == 0 || len(body.To) == 0 {
		out.Err = core.Refuse(core.CodeBadRequest,
			"a search needs both from and to: what types are available at the start, and what is wanted at the end")
		return
	}
	catalog, refusal := s.skills.Catalog()
	if refusal != nil {
		out.Err = refusal
		return
	}
	s.decisions.lock()
	tbl := s.decisions.table()
	s.decisions.unlock()

	var skills []planner.Skill
	var untyped []map[string]string
	for _, e := range catalog {
		in, okIn := typeTokens(e.Manifest.Input)
		ou, okOut := typeTokens(e.Manifest.Output)
		if !okIn || !okOut {
			untyped = append(untyped, map[string]string{
				"skill": e.Name,
				"reason": "does not declare both input_schema and output_schema as lists of type names: " +
					"the planner will not guess what a skill accepts",
			})
			continue
		}
		// Reliability by skill_id and task class — the same key skill.invoke
		// writes its observations under.
		p := tbl.Get(arbiter.Key{Skill: e.Name, TaskClass: body.TaskClass})
		skills = append(skills, planner.Skill{Ref: e.Ref, Name: e.Name, In: in, Out: ou, Rel: p.Mean()})
	}

	res := planner.Search(skills, planner.Goal{
		From: body.From, To: body.To, MaxDepth: body.MaxDepth,
		Floor: body.Floor, MaxProposals: body.MaxProposals,
	})

	props := make([]map[string]any, 0, len(res.Proposals))
	for _, p := range res.Proposals {
		props = append(props, map[string]any{
			"children": p.Children, "names": p.Names,
			// The shape id is the proposal's identity, and it is recomputable
			// from anything published: a composition manifest already declares
			// op and children. Publishing is still a separate act, so a
			// proposal nobody publishes leaves no trace — which is why the
			// matching rate below counts what was published, not what was
			// proposed.
			"shape_id":    planner.ShapeID(planner.SearchOp, p.Children),
			"reliability": round(p.Rel), "depth": p.Depth,
			"reaches": p.Reaches, "why": p.Why,
		})
	}
	drops := make([]map[string]any, 0, len(res.Dropped))
	for _, d := range res.Dropped {
		drops = append(drops, map[string]any{"children": d.Children, "reason": d.Reason})
	}
	un := make([]map[string]any, 0, len(res.Unusable))
	for _, u := range res.Unusable {
		un = append(un, map[string]any{"skill": u.Name, "reason": u.Reason})
	}
	out.Body = mustJSON(map[string]any{
		"proposals": props, "dropped": drops, "unusable": un, "untyped": untyped,
		"states": res.States, "truncated": res.Truncated,
		"catalog": len(catalog),
	})
}
