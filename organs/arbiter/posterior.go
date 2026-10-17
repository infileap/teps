// Package arbiter decides which action to take next, and whether a skill that
// used to work still does.
//
// # Why a plain Beta posterior is not enough
//
// The contract says P(success) ~ Beta(alpha + ok, beta + failures). Taken
// literally, that is a running average whose memory grows without bound: a skill
// that succeeded 1000 times and then rots needs roughly that many bad calls
// before its mean falls anywhere interesting. The scheduler would keep choosing
// a dead skill for a long time, and "the system notices by itself" would be a
// claim rather than a property.
//
// So the posterior here is a **fold with exponential forgetting**: before each
// observation, both counts are scaled by a decay factor slightly below 1. That
// keeps the estimator's memory bounded, so evidence from long ago cannot outvote
// evidence from now. The decay is an explicit parameter rather than a hidden
// constant, because it *is* the sensitivity/specificity tradeoff: lower means
// faster detection of rot and noisier decisions.
//
// # Why exploration is not optional
//
// An arbiter that always picks the current best makes every choice final. One
// bad patch would kill a good skill permanently, because nothing would ever call
// it again and its posterior could never recover. So utility carries an
// upper-confidence term: a rarely-tried action keeps a small standing chance.
// Without it the system can only ever get worse at what it already does.
package arbiter

import (
	"math"
	"sort"
)

// Key identifies what a posterior is about.
//
// Grouping by (skill, task class) rather than by skill alone is what keeps a
// skill from being condemned for failing at something it was never meant to do.
// One posterior per skill would average "read a file" together with "parse a
// PDF" and produce a number that describes neither.
type Key struct {
	Skill     string
	TaskClass string
}

// Posterior is a Beta distribution, kept as its two shape parameters.
type Posterior struct {
	Alpha float64
	Beta  float64
}

// Prior is the posterior of something never observed.
//
// Beta(1,1) is uniform on [0,1]: "no idea". A prior of zero would make the mean
// undefined and would let a single failure produce certainty, which is exactly
// the mistake this package exists to avoid.
func Prior() Posterior { return Posterior{Alpha: 1, Beta: 1} }

// Mean is the expected success probability.
func (p Posterior) Mean() float64 {
	d := p.Alpha + p.Beta
	if d <= 0 {
		return 0.5
	}
	return p.Alpha / d
}

// Count is the effective number of observations, after forgetting.
//
// It is not the number of calls ever made: decay is what makes old evidence
// count for less, and reporting the raw total here would hide that.
func (p Posterior) Count() float64 { return p.Alpha + p.Beta }

// Weight is the decayed weight of this key's observations.
//
// ★★★ 它**不是**观测次数，而这一条差点就以 `Observations` 这个名字出去。
//
// 第一版写的是「`Count()` 含先验那两个伪计数，所以 `Count() - 2` 就是观测次数」。
// 那个推理在**没有遗忘**时成立，而这里有遗忘（`DefaultDecay = 0.98`）：
// `Observe` 每次先把两边乘上 `d`，所以先验的那两个伪计数**自己也在一起衰减**。
// 实测（`Beta(1,1)` 上记一次 ok，`arbiter.state`）：
//
//	alpha=1.98 beta=0.98 -> Count()=2.96 -> Count()-2 = **0.96**
//
// 一次观测，答案是 0.96。三次是 2.82，七次是 6.33 —— 差随次数增长。
// 而 `0.96` 这个数会被读成「这个技能几乎没被看过」，它是 `sum(d^i)` 的形状。
//
// ★ 一个名字读作 A 而值是 B，正是这个项目一直在抓的那一类。**它不能叫
// `Observations`。** 真实的观测次数只有登记表知道（`arbiter.state` 从
// `s.decisions.obs` 里按 key 数出来，报在 `observations`）。
//
// 而这个重量是**真的有用**的：它正是 `UpperBound` 的 `n` —— UCB 的宽度要用
// 的是「有效样本量」，不是「条数」。所以它在这里，只是叫对了名字。
func (p Posterior) Weight() float64 { return p.Count() - 2 }

// Observe folds in one execution result.
//
// ok is the verifier's verdict, not the executor's claim. A skill that reports
// its own success has a posterior that measures its optimism.
func (p Posterior) Observe(ok bool, decay float64) Posterior {
	if decay <= 0 || decay > 1 {
		decay = 1
	}
	out := Posterior{Alpha: p.Alpha * decay, Beta: p.Beta * decay}
	if ok {
		out.Alpha++
	} else {
		out.Beta++
	}
	return out
}

// Table is the set of posteriors an arbiter has.
type Table map[Key]Posterior

// Get returns the posterior for a key, or the prior if it has never been seen.
func (t Table) Get(k Key) Posterior {
	if p, ok := t[k]; ok {
		return p
	}
	return Prior()
}

// Observation is one execution result, as it arrives from the record stream.
type Observation struct {
	Key Key
	OK  bool
	// Rules is the identity of the assertions this run was judged against.
	//
	// The chain already records *which verifier* passed a run. It did not
	// record *which assertions* it was judged against, so a skill republished
	// with different assertions kept folding new runs into the same key — and
	// the number that came out was an average of two different questions.
	//
	// The empty value means "the run predates this field", which is a third
	// thing: not one rule set, not two, but unknown.
	Rules string
}

// Fold replays observations in order into a table.
//
// Order matters and that is the point: forgetting makes the estimator care about
// *when* something happened. A replay that dropped the order would produce a
// different answer than the live system, which is the failure mode the record
// stream exists to prevent.
func Fold(obs []Observation, decay float64) Table {
	t := Table{}
	for _, o := range obs {
		t[o.Key] = t.Get(o.Key).Observe(o.OK, decay)
	}
	return t
}

// DefaultDecay is the forgetting factor used when a caller does not choose one.
//
// 0.98 gives the estimator an effective memory of roughly 1/(1-0.98) = 50
// observations. That is the number that decides how fast rot is noticed, and it
// is deliberately a parameter rather than a law.
const DefaultDecay = 0.98

// Exploration is the width of the upper-confidence term, in the same units as
// the utility it is added to.
//
// It is not a small epsilon for tie-breaking. It is the only thing that lets a
// skill ever come back, so setting it to zero makes every decision permanent.
const Exploration = 0.25

// UpperBound returns a high-probability estimate of the success rate.
//
// This is the Wilson-style width sqrt(ln(N)/n) in its simplest form: it shrinks
// as evidence accumulates, and it stays large for something tried once. The name
// matters — it is an upper bound, so an unobserved action is treated as
// promising rather than as unknown-and-therefore-avoided.
//
// ★★★ 而「it shrinks as evidence accumulates」这句话此前只在一个**窄带**里成立。
//
// 判据在 `n <= 1` 上：`n = 0` 时 `width = Exploration * sqrt(log(1)/0)` 是
// `0/0`，**公式没有定义**，所以那一档必须由人做选择（见 UnobservedUpperBound）。
// 而 `n = 1` 时公式**是有定义的**：`log(2)/1 = 0.693`。此前那一档与 `n = 0`
// 合并成一句 `return 1.0`，于是**一个跑过一次的技能与一个从没跑过的技能报价
// 一模一样**。实测（`Posterior{Alpha: 2, Beta: 1}`，n = 1）：
//
//	此前：1.0000（守卫直接返回）      公式本应给：0.6667 + 0.25 × 0.8326 = 0.8748
//
// ★ 而同一个 `TestUpperBoundShrinksWithEvidence` 拿 `{2,1}` 当「一次」那一点 ——
// **那一点根本没进公式**，所以那条测试一直在用守卫的 1.0 与 `{100,50}` 的 0.7126
// 相比，而不是在比公式。**一条读作「界随证据收紧」的测试，它的一半在函数之外。**
//
// ★ 另一半：`m > 1` 的夹取在 `Exploration = 0.25` 下从 n≈20 起就开始生效
// （`{20,2}` 的 mean 是 0.9091、width 是 0.0975 ⇒ 夹到 1；`{1000,1}` 的 mean 是
// 0.9990 ⇒ 也夹到 1）。所以一个 1000 次全成的技能与一个从没跑过的技能**仍然
// 同为 1**。夹取本身是对的（一个概率不能大于 1），要改的不是它 ——
// 是**上面那一档不该借走它的答案**。
func (p Posterior) UpperBound() float64 {
	// ★ 这里要的是**有效样本量**（带遗忘的权重），不是观测条数 ——
	// UCB 的宽度按权重收窄是对的。见 Weight 的注释。
	n := p.Weight()
	if n <= 0 {
		// ★ `n = 0`：公式里 `log(1)/0` 没有定义，所以这是一个**选择**，
		// 不是一次计算。它有自己的名字与自己的理由。
		return UnobservedUpperBound
	}
	width := Exploration * math.Sqrt(math.Log(1+n)/n)
	m := p.Mean() + width
	if m > 1 {
		return 1
	}
	return m
}

// UnobservedUpperBound 是**零观测**时 `UpperBound` 的答案。
//
// 它是一个具名的选择，不是公式的一个结果：`width = Exploration * sqrt(log(1)/0)`
// 是 `0/0`，没有定义。这个值说的是「一个还没试过的动作要显得有希望，而不是
// 未知因而回避」—— 与 `Exploration` 那条注释是同一件事的两半，所以它取最大。
//
// ★ 它此前是一个裸的 `1.0`，与「跑过一次」共用同一行 `if n <= 1 { return 1.0 }`。
// 两档合并的代价不是难看：**它让「没试过」与「试过一次」在报价里同价**，
// 而那两件事的差别正是这个函数存在的理由。
const UnobservedUpperBound = 1.0

// RulesOf reports, per key, the distinct judging-rule identities behind its
// observations.
//
// ★ A posterior folded across two judging rules is not one posterior.
//
// The same argument as everywhere else in this project: one number with two
// sources is not one number. A mean of 0.5 can mean "one program that works
// half the time" or "two programs, one always works and one never does" — and
// those are two facts.
//
// It takes the same []Observation as Fold, so the two projections cannot be
// derived from different walks over different data.
func RulesOf(obs []Observation) map[Key][]string {
	seen := map[Key]map[string]bool{}
	out := map[Key][]string{}
	for _, o := range obs {
		id := o.Rules
		if id == "" {
			id = "(unrecorded)"
		}
		if seen[o.Key] == nil {
			seen[o.Key] = map[string]bool{}
		}
		if !seen[o.Key][id] {
			seen[o.Key][id] = true
			out[o.Key] = append(out[o.Key], id)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}
