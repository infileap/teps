package reflex

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func mustNew(t *testing.T, id, stimulus, action string) Rule {
	t.Helper()
	r, ref := New(id, stimulus, action)
	if ref != nil {
		t.Fatalf("New(%q, %q) refused: %v", stimulus, action, ref)
	}
	return r
}

func TestNormaliseCollapsesWhatDoesNotChangeTheStimulus(t *testing.T) {
	cases := map[string]string{
		"  What   TIME is it?  ": "what time is it",
		"What time is it":        "what time is it",
		"what-time-is-it":        "what time is it",
		"WHAT TIME IS IT":        "what time is it",
		"今天几号？":                  "今天几号",
		"今天几号":                   "今天几号",
		"Привет, мир!":           "привет мир",
		"...":                    "",
		"a,b;c":                  "a b c",
	}
	for in, want := range cases {
		if got := Normalise(in); got != want {
			t.Fatalf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAStimulusThatNormalisesToNothingIsRefused(t *testing.T) {
	_, ref := New("r1", "???", "do.thing")
	if ref == nil {
		t.Fatal("an empty pattern is not a general rule, it is a typo")
	}
	if !strings.Contains(ref.Reason, "typo") {
		t.Fatalf("the reason must say why, got %q", ref.Reason)
	}
}

func TestARuleNeedsAnAction(t *testing.T) {
	if _, ref := New("r1", "what time is it", ""); ref == nil {
		t.Fatal("a rule with no action would match and then have nothing to do")
	}
}

// ★ 一次观测永远不够回答。这是这一层「建立」的含义。
func TestAFreshRuleDoesNotFire(t *testing.T) {
	r := mustNew(t, "r1", "what time is it", "clock.now")
	if Fires(r) {
		t.Fatalf("a rule seen once must not answer, strength %v threshold %v", r.Strength, FireThreshold)
	}
}

// ★ One success is deliberately NOT enough.
//
// An observation both creates a rule and applies its outcome, so a fresh rule
// already carries one step: 0.3 + 0.2 = 0.5, below the 0.6 threshold. The layer
// answers only after seeing the same thing work twice.
//
// This test used to assert the opposite, and it passed — because it had the
// wrong constant written into its expectation. A test that encodes a constant
// cannot catch that constant being wrong; drive.sh caught it instead, where the
// expectation is derived from what the layer promises rather than from the code.
// fired is the shortest sequence that reaches the threshold: a rule starts at
// 0.3 and needs two successes to pass 0.6.
func fired(t *testing.T, id, stimulus, action string) Rule {
	t.Helper()
	return Reinforce(Reinforce(mustNew(t, id, stimulus, action)))
}

func TestOneSuccessIsNotEnoughToFire(t *testing.T) {
	r := Reinforce(mustNew(t, "r1", "what time is it", "clock.now"))
	if Fires(r) {
		t.Fatalf("one success must not be enough to start answering, strength %v", r.Strength)
	}
	// ★ 这条此前断言 `r.Fires == 1`，理由是「那次成功还是得算」。
	// 那个断言把 `Fires` 的含义定成了「观测次数」——而同一个包里
	// `Fires(r)` 问的是「它现在答不答」。一个词两个意思。
	//
	// 现在 `Fires` 只数**它答过几次**：这条规则一次都没答过（0.5 < 0.6），
	// 所以是 0。它不是「失败了一次」，它是**没有被问到**。
	if r.Fires != 0 {
		t.Fatalf("a rule that has never answered has no firings, got %d", r.Fires)
	}
}

func TestTwoSuccessesMakeItFire(t *testing.T) {
	r := Reinforce(Reinforce(mustNew(t, "r1", "what time is it", "clock.now")))
	if !Fires(r) {
		t.Fatalf("two successes should start answering, strength %v", r.Strength)
	}
	// 它**现在**能答了，但两次观测都落在它还不能答的时候，所以还不是开火。
	// 下一次观测落在一条正在答的规则上，才第一次算开火。
	if r.Fires != 0 {
		t.Fatalf("reaching the threshold is not itself a firing, got %d", r.Fires)
	}
	r = Reinforce(r)
	if r.Fires != 1 {
		t.Fatalf("the observation after it started answering is the first firing, got %d", r.Fires)
	}
}

// ★★★ 一条从没强到能开始的规则，被观测一次失败之后，**不是**「已消退」。
//
// 这是 `extinguished` 那个数此前报错的那条路径：新规则起点 0.3、阈值 0.6，
// 它够不到开火线；`Weaken` 无条件 `r.Fires++` 把它记成「答过一次且答错了」，
// 而 `reflex.state` 的判据是 `r.Fires > 0 && !Fires(r)` ⇒ 报成「触发过、现在不触发了」。
// 而它旁边那句注释逐字写着这个数要区分开的正是**「从没强到能开始」**。
func TestARuleThatNeverStartedIsNotExtinguished(t *testing.T) {
	r := mustNew(t, "r1", "what time is it", "clock.now")
	if Fires(r) {
		t.Fatal("precondition: a fresh rule cannot answer")
	}
	r = Weaken(r)
	if Fires(r) {
		t.Fatal("precondition: a failure on a fresh rule leaves it unable to answer")
	}
	if r.Fires != 0 {
		t.Fatalf("it was never asked, so it never answered, got %d firings", r.Fires)
	}
	if r.Fires > 0 {
		t.Fatal("`r.Fires > 0` is the whole test `reflex.state` uses for extinction")
	}
}

// ★ 判据取的是**削弱之前**的强度：问的是「它被问到的那一刻答不答」。
//
// 一条正好停在阈值上的规则，削弱之后就不在阈值上了（0.6 → 0.4）。
// 如果判据放在削弱之后，这次失败会被记成「它没在答」——而它**答了**。
func TestTheFiringTestIsTakenBeforeTheStrengthChanges(t *testing.T) {
	r := Rule{ID: "r1", Pattern: "s", Action: "a", Strength: FireThreshold}
	if !Fires(r) {
		t.Fatal("precondition: a rule exactly at the threshold answers")
	}
	r = Weaken(r)
	if r.Fires != 1 {
		t.Fatalf("it answered before the failure, so the failure is a firing, got %d", r.Fires)
	}
	if Fires(r) {
		t.Fatal("precondition: it no longer answers after the failure")
	}
}

// ★ 一条真的开过火的规则被削弱，那次失败**要**算一次开火：
// 「它答了，而且答错了」——这正是 `Weaken` 文档里那句理由成立的那种情形。
func TestAFailureOnAFiringRuleCountsAsAFiring(t *testing.T) {
	r := fired(t, "r1", "what time is it", "clock.now")
	if !Fires(r) {
		t.Fatal("precondition: the rule fires")
	}
	before := r.Fires
	r = Weaken(r)
	if r.Fires != before+1 {
		t.Fatalf("a failure while answering is a firing, got %d from %d", r.Fires, before)
	}
}

// ★★ 不对称是这一层的核心判断：误触发比漏触发贵。
//
// 漏触发只多走一次完整路径，那条路径仍然是对的；
// 误触发是**在完全没有上下文的情况下自信地答错**。
func TestAFailureCostsMoreThanASuccessGains(t *testing.T) {
	// 直接断言常量。行为断言（下面那条）会因为浮点噪声而在对称实现下**通过**：
	// 0.5-0.3 与 0.7-0.5 都不是精确的 0.2，一个比另一个大 2e-17。
	// 第一次跑变异时它就是这么活下来的。
	if WeakenStep <= ReinforceStep {
		t.Fatalf("the layer must be biased against firing: a success gains %v, a failure costs %v", ReinforceStep, WeakenStep)
	}
	fresh := mustNew(t, "r1", "what time is it", "clock.now")
	gained := Reinforce(fresh).Strength - fresh.Strength
	lost := fresh.Strength - Weaken(fresh).Strength
	// 容差是刻意的：这条测的是「显著更贵」，不是「大 2e-17」。
	if lost <= gained+1e-9 {
		t.Fatalf("the layer must be biased against firing: a success gains %v, a failure costs %v", gained, lost)
	}
	if Weaken(fresh).Strength >= fresh.Strength {
		t.Fatal("a failure must lower the strength")
	}
}

func TestStrengthSaturatesAtBothEnds(t *testing.T) {
	r := mustNew(t, "r1", "s", "a")
	for i := 0; i < 20; i++ {
		r = Reinforce(r)
	}
	if r.Strength != 1 {
		t.Fatalf("strength must saturate at 1, got %v", r.Strength)
	}
	for i := 0; i < 20; i++ {
		r = Weaken(r)
	}
	if r.Strength != 0 {
		t.Fatalf("strength must saturate at 0, got %v", r.Strength)
	}
}

// ★ 消退不是删除：掉到阈值以下就不答了，但它还在，一次成功就能回来。
func TestDecayExtinguishesWithoutForgetting(t *testing.T) {
	r := fired(t, "r1", "what time is it", "clock.now")
	if !Fires(r) {
		t.Fatal("precondition: the rule fires before decaying")
	}
	r = Decay(r, 2)
	if Fires(r) {
		t.Fatalf("two idle periods must extinguish it, strength %v", r.Strength)
	}
	if r.Strength <= 0 {
		t.Fatal("extinction must not be deletion: the rule is still there")
	}
	if !Fires(Reinforce(r)) {
		t.Fatal("one success must bring an extinguished rule back")
	}
}

func TestDecayingZeroPeriodsChangesNothing(t *testing.T) {
	r := mustNew(t, "r1", "s", "a")
	if Decay(r, 0) != r || Decay(r, -3) != r {
		t.Fatal("decaying by zero or negative periods must be a no-op")
	}
}

func TestMatchIsExactAfterNormalisation(t *testing.T) {
	r := fired(t, "r1", "what time is it", "clock.now")
	if _, ok := Match("What  TIME is it?", []Rule{r}); !ok {
		t.Fatal("the same stimulus in different punctuation and case must match")
	}
	// ★ 相近但不是同一个刺激，不匹配。模糊的键正是「看起来正常的错答案」的来源。
	if _, ok := Match("what time is it in tokyo", []Rule{r}); ok {
		t.Fatal("a different stimulus must not fire: fuzziness is where a plausible wrong answer comes from")
	}
}

func TestMatchIgnoresRulesThatDoNotFire(t *testing.T) {
	r := mustNew(t, "r1", "what time is it", "clock.now")
	if _, ok := Match("what time is it", []Rule{r}); ok {
		t.Fatal("a rule below the threshold must not answer")
	}
}

func TestMatchPicksTheStrongest(t *testing.T) {
	weak := fired(t, "r1", "what time is it", "clock.weak")
	strong := Reinforce(fired(t, "r2", "what time is it", "clock.strong"))
	got, ok := Match("what time is it", []Rule{weak, strong})
	if !ok || got.ID != "r2" {
		t.Fatalf("the stronger association must win, got %q", got.ID)
	}
}

func TestMatchBreaksTiesByID(t *testing.T) {
	a := fired(t, "aaa", "what time is it", "clock.a")
	b := fired(t, "bbb", "what time is it", "clock.b")
	for _, order := range [][]Rule{{a, b}, {b, a}} {
		got, ok := Match("what time is it", order)
		if !ok || got.ID != "aaa" {
			t.Fatalf("a tie must break the same way regardless of slice order, got %q", got.ID)
		}
	}
}

func TestAnEmptyStimulusNeverMatches(t *testing.T) {
	r := fired(t, "r1", "what time is it", "clock.now")
	if _, ok := Match("???", []Rule{r}); ok {
		t.Fatal("a stimulus that normalises to nothing must not match anything")
	}
}

func TestRate(t *testing.T) {
	if Rate(0, 0) != 0 {
		t.Fatal("a rate with no traffic is zero, not NaN")
	}
	if got := Rate(3, 4); got != 0.75 {
		t.Fatalf("want 0.75, got %v", got)
	}
}

// ★★ 「零上下文」是结构性的，不是一句承诺。
//
// 这一层若 import 了 composer 或 memory，它就不再是反射，而「零上下文」
// 也就从结构性事实变成了一句口号。所以这条测试直接读源码。
func TestReflexImportsNothingFromTheContextPipeline(t *testing.T) {
	src, err := os.ReadFile("reflex.go")
	if err != nil {
		t.Fatalf("the source must be readable for this claim to be checkable: %v", err)
	}
	block := regexp.MustCompile(`(?s)import \((.*?)\)`).FindStringSubmatch(string(src))
	if block == nil {
		t.Fatal("no import block found: this test can no longer see what it is checking")
	}
	for _, line := range strings.Split(block[1], "\n") {
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), `"`))
		if line == "" {
			continue
		}
		if line == "teps/core" {
			continue
		}
		if !strings.Contains(line, "/") && !strings.Contains(line, ".") {
			continue // stdlib
		}
		t.Fatalf("reflex must not depend on %q: a reflex that consults the context pipeline is not a reflex", line)
	}
}
