package distill

import (
	"strings"
	"testing"
)

func good() Trace {
	return Trace{TaskID: "t1", Stimulus: "what time is it", Layer: "full", Outcome: "ok", Action: "clock.now"}
}

func TestASuccessfulFullPathAnswerIsDistillable(t *testing.T) {
	p, ref := Propose(good())
	if ref != nil {
		t.Fatalf("a successful full-path answer must be distillable, got %v", ref)
	}
	if p.Kind != KindReflex || p.Action != "clock.now" || p.Stimulus != "what time is it" {
		t.Fatalf("wrong proposal: %+v", p)
	}
	if p.Reason == "" {
		t.Fatal("a proposal with no reason is a promotion, and promotions are what the evidence ladder exists to stop")
	}
}

// ★ 失败教的是回避，不是规则。
func TestAFailureIsNotDistillable(t *testing.T) {
	for _, outcome := range []string{"failed", "timeout", "refused", "degraded", "unknown"} {
		tr := good()
		tr.Outcome = outcome
		if _, ref := Propose(tr); ref == nil {
			t.Fatalf("outcome %q must not be distillable", outcome)
		}
	}
}

// ★ 「没人记录过它成没成」是最需要被说出来的那种情况。
func TestAMissingOutcomeIsNamedInTheRefusal(t *testing.T) {
	tr := good()
	tr.Outcome = ""
	_, ref := Propose(tr)
	if ref == nil {
		t.Fatal("a trace with no outcome is not a success")
	}
	if !strings.Contains(ref.Reason, "missing") || !strings.Contains(ref.Reason, "nobody recorded") {
		t.Fatalf("an empty outcome must be named, not left blank: %q", ref.Reason)
	}
}

// ★ 已经零上下文答过的东西没有可蒸馏的：再蒸一次会把同一次成功数两遍。
func TestAnAlreadyCheapAnswerIsNotDistillable(t *testing.T) {
	tr := good()
	tr.Layer = LayerReflex
	_, ref := Propose(tr)
	if ref == nil {
		t.Fatal("an answer that cost nothing has nothing left to distil")
	}
	if !strings.Contains(ref.Reason, "twice") {
		t.Fatalf("the reason must name the double-counting risk: %q", ref.Reason)
	}
}

func TestAnActionlessTraceIsNotDistillable(t *testing.T) {
	tr := good()
	tr.Action = ""
	_, ref := Propose(tr)
	if ref == nil {
		t.Fatal("a rule that fires and does nothing is worse than no rule")
	}
	if !strings.Contains(ref.Reason, "stops the full path") {
		t.Fatalf("the reason must say why doing nothing is harmful: %q", ref.Reason)
	}
}

func TestAStimuluslessTraceIsNotDistillable(t *testing.T) {
	tr := good()
	tr.Stimulus = "   "
	if _, ref := Propose(tr); ref == nil {
		t.Fatal("a rule with no key would match nothing, or everything")
	}
}

func TestATraceWithoutAnIDIsNotDistillable(t *testing.T) {
	tr := good()
	tr.TaskID = ""
	_, ref := Propose(tr)
	if ref == nil {
		t.Fatal("a rule with no provenance cannot be audited")
	}
	if !strings.Contains(ref.Reason, "which experience") {
		t.Fatalf("the reason must say what is missing: %q", ref.Reason)
	}
}

func TestEveryRefusalSaysWhy(t *testing.T) {
	bad := []Trace{
		{},
		{TaskID: "t", Stimulus: "s", Layer: "full", Outcome: "failed", Action: "a"},
		{TaskID: "t", Stimulus: "s", Layer: LayerReflex, Outcome: "ok", Action: "a"},
		{TaskID: "t", Stimulus: "s", Layer: "full", Outcome: "ok"},
		{TaskID: "t", Layer: "full", Outcome: "ok", Action: "a"},
	}
	for i, tr := range bad {
		_, ref := Propose(tr)
		if ref == nil {
			t.Fatalf("case %d must be refused", i)
		}
		if len(ref.Reason) < 40 {
			t.Fatalf("case %d has a reason too short to argue with: %q", i, ref.Reason)
		}
	}
}

func TestDistillableAgreesWithPropose(t *testing.T) {
	for _, tr := range []Trace{good(), {TaskID: "t", Layer: "full", Outcome: "ok", Stimulus: "s", Action: "a"}, {}} {
		_, ref := Propose(tr)
		if Distillable(tr) != (ref == nil) {
			t.Fatal("Distillable and Propose must agree")
		}
	}
}

func TestProposeIsDeterministic(t *testing.T) {
	a, _ := Propose(good())
	b, _ := Propose(good())
	if a != b {
		t.Fatal("the same trace must propose the same thing")
	}
}
