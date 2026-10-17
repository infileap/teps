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
	// ★★★ 搜索的分母必须出现。
	//
	// 此前这里只有一个 `"catalog": len(catalog)` —— 它是**登记表条数**，而真正
	// 交给规划器的技能是 `len(skills)`，两者之间差着 `untyped` 那一批。实测
	// （登记表 4 个技能，其中 2 个不声明类型）返回的是：
	//
	//   ['catalog', 'dropped', 'proposals', 'states', 'truncated', 'untyped', 'unusable']
	//   catalog = 4 · untyped 条数 = 2 · **参与搜索的技能数：没有这个字段**
	//
	// 读的人只能自己做减法 4 − 2 = 2。而这不是一个可有可无的数：`proposals: []`
	// 在一个「4 个技能里 2 个不会声明类型」的登记表上，与在一个「4 个技能全都
	// 能参与搜索、但确实没有路」的登记表上，是两个完全不同的事实 ——
	// 一个是**问不出来**，一个是**问过了，没有**。
	//
	// `catalog` 这个名字本身也读错方向：它读起来是「这就是登记表」，而它是一个数。
	// 与 `metrics.accuracy` 报 `traced`/`chains_run`/`unrun`、与 `waste` 报它的
	// 两个计数是同一条：**一个数是某个窗口里的数时，窗口是它的一部分。**
	out.Body = mustJSON(map[string]any{
		"proposals": props, "dropped": drops, "unusable": un, "untyped": untyped,
		"states": res.States, "truncated": res.Truncated,
		// 登记表条数。
		"catalog": len(catalog),
		// ★ 真正交给规划器的技能数 —— 上面那个数减去这一批。
		"searched": len(skills),
		// 这一批的条数。与 `len(untyped)` 是同一个数，但它在这里是**被报出来的**，
		// 而不是要调用方自己去数一个数组。
		"untyped_count": len(untyped),
		"basis": "searched is the number of skills that declared both input_schema and output_schema " +
			"and were therefore handed to the planner; catalog is the whole registry and equals " +
			"searched + untyped_count. `proposals: []` over a registry where most skills declare no " +
			"types means the question could not be asked, which is not the same as having asked it " +
			"and found nothing",
	})
}
