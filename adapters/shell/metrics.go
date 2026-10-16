package main

import (
	"sort"
	"strconv"
	"strings"

	"teps/core"
	"teps/organs/arbiter"
	"teps/organs/intake"
	"teps/store"
)

// metrics.get is the one read that reports whether the numbers are defined.
//
// Every organ already reports its own numbers and each of those reports is
// correct on its own. What none of them says is whether the numbers compose
// into anything. The capability formula is a product:
//
//	capability = coverage × accuracy × reliability
//
// and a product with one undefined factor is undefined. Today that has to be
// discovered by asking four ops and noticing that one of them returned a zero
// which means "we have never seen a task" rather than "we answered none of
// them" — the same number standing for two different facts.
//
// So this op adds no facts of its own. It reports each factor from the organ
// that owns it, and it says out loud whether the factor is defined. That is the
// whole contribution: a zero with no denominator is not a small number, it is
// an absent one.
//
// The accuracy factor was the one with no definition at all, because nothing
// linked a proposal to the runs of the skill it became. It does now. A
// proposal's identity is the *shape* of the chain it proposes — a hash over the
// operator and the ordered children — and that hash can be recomputed from any
// published manifest, so the link needs no second copy of anything. Accuracy is
// therefore counted over the compositions whose provenance says the planner
// produced them: how many of those have ever run, and how they ran.
//
// Note what is *not* counted: proposals nobody published. They leave no trace,
// and a rate over them would be a rate over zero.
func (s *server) handleMetrics(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "metrics.") {
		return false
	}
	switch f.T {
	case "metrics.get":
		s.metricsGet(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) metricsGet(f core.Frame, out *core.Frame) {
	// A read takes no body; see audit.tail for why an all-optional op must not
	// demand one.
	if len(f.Body) > 0 {
		var body struct{}
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return
		}
	}

	s.tasks.lock()
	stats := s.tasks.summarise()
	s.tasks.unlock()

	s.decisions.lock()
	tbl := s.decisions.table()
	undecayed := s.decisions.undecayedTable()
	rules := arbiter.RulesOf(s.decisions.obs)
	mixed := mixedSkills(rules)
	observations := len(s.decisions.obs)
	spent := round(s.decisions.spent)
	successes := s.decisions.ok
	s.decisions.unlock()

	coverage := map[string]any{
		"tasks":                    stats.Total,
		"answered_without_context": stats.ByLayer[intake.LayerReflex],
		"intercept_rate":           round(stats.InterceptRate),
		"source":                   "task.stats",
	}
	coverage["defined"] = stats.Total > 0
	if stats.Total == 0 {
		coverage["reason"] = "no task has ever been taken in, so the denominator does not exist: a rate over zero arrivals is not zero, it is undefined"
	}

	accuracy := s.accuracy(tbl)

	// ★ `skills` 数的是**不同的 skill_id**，`rows` 数的是后验表里有多少行。
	//
	// 此前只有 `"skills": len(tbl)`，而 `tbl` 是 `map[Key]Posterior`、`Key{Skill, TaskClass}`。
	// 于是那一个数把三件事混在一起：**同一个技能在两个任务类下算两个**，
	// 而**组合**（`compose:serial:<形状哈希>`）与技能在同一个命名空间里、也各算一个。
	//
	// 一个字段名叫 `skills`、算的是「后验行数」的数，读起来是「有几个技能」。
	// 这与 DSH 任务看板那个 `armedSchedules` 是同一个形状：
	// **一个读起来像运行时事实的名字，底下是一个别的事实。**
	distinctSkills := map[string]struct{}{}
	for k := range tbl {
		distinctSkills[k.Skill] = struct{}{}
	}

	reliability := map[string]any{
		"observations": observations,
		"skills":       len(distinctSkills),
		"rows":         len(tbl),
		"source":       "arbiter.state",
		// spent 是链上那些执行记录说的钱。它必须与 reliability 同时出现：
		// 一条可靠但没人付得起的路，和一条便宜但不可靠的路，是两个不同的答案。
		"spent": spent,
	}
	if len(tbl) == 0 {
		reliability["defined"] = false
		reliability["reason"] = "no execution has ever been concluded, so every skill is still at the prior"
	} else {
		sum, weakest := 0.0, 1.0
		for _, p := range tbl {
			m := p.Mean()
			sum += m
			if m < weakest {
				weakest = m
			}
		}
		reliability["defined"] = true
		reliability["mean"] = round(sum / float64(len(tbl)))
		// ★★★ 三个量、三个名字。
		//
		// 此前 `decay_gap = mean − raw_rate`，而 `mean` 是**按行不加权**的
		// 衰减后平均、`raw_rate` 是**按观测加权**的成功率。那个差因此把
		// 三件事混在一起：① 衰减 ② 加权方式 ③ Beta(1,1) 先验把低观测数的
		// 行拉向 0.5。而名字只说 decay。
		//
		// 实测（一个技能观测 1 次成功、另一个观测 9 次失败）：
		// mean 0.3762 · raw_rate 0.1 · 旧 decay_gap **+0.2762** —— 正的，
		// 而衰减只会把一行拉向先验。现在 decay_gap 是同一批记录折两次之差。
		reliability["mean_undecayed"] = round(meanOf(undecayed))
		reliability["raw_rate"] = round(rawRate(successes, observations))
		reliability["decay_gap"] = round(sum/float64(len(tbl)) - meanOf(undecayed))
		// ★ 这是**表里最差的那一行**，一个诊断，不是一个因子。
		//
		// 它此前写着「对一个产品来说重要的是最弱的一环：一条链和它最差的一步
		// 一样可靠，而平均值恰好掩盖了那件事」——那句话**借用了组合的教义，
		// 而表不是一条链**：`weakest` 是**所有技能 × 所有任务类**里的最小值，
		// 不是某条链的某一步。而且一行只观测一次且失败时 `mean ≈ 0.1`，
		// 把它当因子乘进能力，会让**一次坏观测支配整个乘积**。
		//
		// 那条注释真正想说的东西，现在报在 `capability.weakest_factor` 里：
		// **乘积自己的最弱因子**，那才是一条链与它最差的一步的关系。
		reliability["weakest"] = round(weakest)
	}
	// ★ 一个后验如果在两套判定规则上折出来，它就不是一个后验。
	//
	// 与「同一个 0 可以是两个事实」是同一条论证：均值 0.5 可以表示
	// 「一个程序一半时间能用」，也可以表示「两个程序，一个总行一个从不行」——
	// 那是两个事实，而报出来的只有一个数。
	reliability["mixed_skills"] = mixed

	capability := map[string]any{"defined": false}
	switch {
	case !coverage["defined"].(bool):
		capability["reason"] = "coverage is undefined"
	case !accuracy["defined"].(bool):
		capability["reason"] = "accuracy is undefined"
	case !reliability["defined"].(bool):
		capability["reason"] = "reliability is undefined"
	case mixed > 0:
		// 能力是乘积，而可靠性这一项此刻是**不同问题的平均值**。
		// 把它当因子乘进去，乘积看起来照旧是个数——那正是最坏的一种错：
		// 一个没人会去查的数。
		capability["reason"] = "reliability averages observations judged under more than one rule set: the factor is an average of different questions, and a product that hides that is a number nobody will check"
	default:
		capability["defined"] = true
		coverageFactor := coverage["intercept_rate"].(float64)
		accuracyFactor := accuracy["value"].(float64)
		reliabilityFactor := reliability["mean"].(float64)
		capability["value"] = round(coverageFactor * accuracyFactor * reliabilityFactor)

		// ★ 乘积自己的最弱因子——那条旧注释想说的东西。
		//
		// 「能力是乘积」的意思是：三个因子里**最弱的那个**决定了乘积的上限，
		// 而一个乘积不告诉你它被谁压着。这与组合的教义是同一条
		// （一条链和它最差的一步一样可靠），区别是这里的三步确实是**一条链**：
		// 覆盖率 → 准确率 → 可靠性，每一步都依赖前一步产出的东西。
		//
		// ★ 按**排好序的名字**遍历，不按 map 顺序：并列时 map 迭代顺序是随机的，
		// 于是同一个输入会报出两个不同的名字——一个会飘的数不是一个读数。
		wf, wname := weakestFactor([]capFactor{
			{"coverage.intercept_rate", coverageFactor},
			{"accuracy.value", accuracyFactor},
			{"reliability.mean", reliabilityFactor},
		})
		capability["weakest_factor"] = round(wf)
		capability["weakest_name"] = wname
		capability["basis"] = "the product of three factors, each of which depends on the one before it; the weakest of them caps what the product can be"
	}

	// ★ 一个价格只有放在它买到的东西旁边才有意义。
	//
	// 花费与成功率各自都有定义，但「每次成功花了多少钱」是第三个事实，
	// 而它恰恰是唯一能回答「这件事值不值得做」的那个。分母为零时它没有定义 ——
	// 一个没有成功的每次成功成本，是一个装扮成数字的除零。
	cost := map[string]any{
		"spent":     spent,
		"successes": successes,
		"source":    "the execution records on the chain",
	}
	if successes == 0 {
		cost["defined"] = false
		cost["reason"] = "nothing has succeeded yet, so there is no cost per success: dividing by a success count of zero is not a very large number, it is an absent one"
	} else {
		cost["defined"] = true
		cost["per_success"] = round(spent / float64(successes))
	}

	out.Body = mustJSON(map[string]any{
		"coverage":    coverage,
		"accuracy":    accuracy,
		"reliability": reliability,
		"capability":  capability,
		"cost":        cost,
	})
}

// capFactor is one factor of the capability product, with its reported name.
type capFactor struct {
	name string
	v    float64
}

// weakestFactor picks the factor that caps the product, and names it.
//
// It is a function rather than three lines inline because the name has to be
// **deterministic**: iterating a map would give a different name on a tie, and
// a reading that changes between two identical inputs is not a reading. Sorting
// the names first is what makes the tie break by name rather than by chance.
func weakestFactor(factors []capFactor) (float64, string) {
	sorted := append([]capFactor(nil), factors...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	weakest, name := sorted[0].v, sorted[0].name
	for _, f := range sorted[1:] {
		if f.v < weakest {
			weakest, name = f.v, f.name
		}
	}
	return weakest, name
}

// plannerSource is the provenance value that marks a chain as the planner's
// output rather than a person's.
const plannerSource = "planner"

// evenOdds is the line a chain's decayed posterior has to reach to be counted.
//
// ★ 它是**一个阈值**，不是**一个率**。字段名与说明都必须说清这件事：
// 同一批记录里 `reliability.mean` 报的是率，这里报的是「有多少条链越过了这条线」。
const evenOdds = 0.5

// accuracy reports how the chains the planner proposed actually did.
//
// ★ The link between a proposal and a run is provenance.source == "planner".
// It has to be something on the chain, because a proposal is not: the search is
// pure and writes nothing. A published chain's *shape* is recoverable from its
// own manifest, but whether a person or the planner produced it is not — that
// is what the source field records, and it is the whole ABI this factor needed.
//
// The denominator is therefore "compositions that say the planner made them",
// not "chains the planner proposed". A proposal nobody published leaves no
// trace, and reporting a rate over proposals would be inventing a denominator.
func (s *server) accuracy(tbl arbiter.Table) map[string]any {
	out := map[string]any{"source": "store + arbiter", "provenance": plannerSource}
	catalog, ref := s.skills.Catalog()
	if ref != nil {
		out["defined"] = false
		out["reason"] = "the catalogue could not be read: " + ref.Reason
		return out
	}
	traced, observed, favoured := 0, 0, 0
	var chains []string
	byChain := map[string][]string{}
	for _, e := range catalog {
		m := e.Manifest
		if m == nil || m.Kind != store.KindCompose || m.Compose == nil {
			continue
		}
		if m.Provenance.Source != plannerSource {
			continue
		}
		traced++
		chains = append(chains, e.Name)
		// ★ The posterior is keyed by (skill, task class), and an observation
		// is written under the task class the invocation declared. Looking this
		// chain up with an empty task class silently returns the *prior* — the
		// same zero-means-two-things failure this op exists to report, this
		// time inside the op itself. So the evidence is summed over every task
		// class the chain has been run under, and the classes are reported.
		//
		// Count() is Alpha+Beta and the prior is Beta(1,1), so a posterior
		// still at 2.0 has never been observed. "Never run" is not a failure
		// and must not be averaged in as one.
		var p arbiter.Posterior
		var classes []string
		for k, v := range tbl {
			if k.Skill != e.Name {
				continue
			}
			p.Alpha += v.Alpha
			p.Beta += v.Beta
			classes = append(classes, k.TaskClass)
		}
		if p.Count() <= 2.0+1e-9 {
			continue
		}
		sort.Strings(classes)
		byChain[e.Name] = classes
		observed++
		// ★ 这个判据是「衰减后的后验均值 ≥ 0.5」，而**衰减让它严格难于「一半以上的运行成功」**。
		//
		// 实测：一条链跑一次成功、一次失败，未衰减的均值正好是 0.5，
		// 而衰减（DefaultDecay = 0.98）把它压到 **0.4974** ⇒ 它**不算**被计中。
		// 所以 `>= 0.5` 这句话读起来是「至少一半」，行为是「严格多于一半，再加一点衰减的余量」。
		//
		// 字段名必须说清底下是什么。原来叫 `ok`——那个名字读起来是
		// 「这条链成功了」，而底下是「它的后验偏向成功」。
		// 同一批数据里 `reliability.mean` 已经报了真实的率，所以这里报的是
		// **一个过了阈值的计数**，不是一个率。
		if p.Mean() >= evenOdds {
			favoured++
		}
	}
	sort.Strings(chains)
	out["traced"] = traced
	out["observed"] = observed
	out["unrun"] = traced - observed
	out["threshold"] = evenOdds
	// `favoured` 而不是 `ok`：一个名字读起来像运行时事实，底下必须是那个事实。
	out["favoured"] = favoured
	// 这句话要说出来，否则读的人会把 `value` 当成一个成功率——
	// 而 `reliability.mean` 才是那个率。
	out["basis"] = "a chain is counted when its decayed posterior mean is at or above the threshold; " +
		"the decay makes this strictly harder than \"more than half of its runs succeeded\", so a chain " +
		"that succeeded exactly as often as it failed lands just below and is not counted"
	out["chains"] = chains
	out["task_classes"] = byChain
	if observed == 0 {
		out["defined"] = false
		out["reason"] = "no chain the planner proposed has ever been run: " + strconv.Itoa(traced) +
			" are in the shop and every one of them is still at the prior. A matching rate over proposals nobody ran would be a rate over zero"
		return out
	}
	out["defined"] = true
	out["value"] = round(float64(favoured) / float64(observed))
	return out
}
