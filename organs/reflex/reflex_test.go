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
	if r.Fires != 1 {
		t.Fatalf("the success must still be counted, got %d", r.Fires)
	}
}

func TestTwoSuccessesMakeItFire(t *testing.T) {
	r := Reinforce(Reinforce(mustNew(t, "r1", "what time is it", "clock.now")))
	if !Fires(r) {
		t.Fatalf("two successes should start answering, strength %v", r.Strength)
	}
	if r.Fires != 2 {
		t.Fatalf("two firings must be counted, got %d", r.Fires)
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
