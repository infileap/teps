package intake

import "testing"

type fakeLayer struct {
	name    string
	answers map[string]string
}

func (f fakeLayer) Name() string { return f.name }

func (f fakeLayer) Answer(stimulus string) (string, bool) {
	action, ok := f.answers[stimulus]
	return action, ok
}

func TestTheFirstLayerThatWillAnswerAnswers(t *testing.T) {
	first := fakeLayer{name: "first", answers: map[string]string{"a": "one"}}
	second := fakeLayer{name: "second", answers: map[string]string{"a": "two"}}
	got := Route("a", []Answerer{first, second})
	if got.By != "first" {
		t.Fatalf("the first layer to answer must answer, got %q", got.By)
	}
	if got.Action != "one" {
		t.Fatalf("the action must be the answering layer's, got %q", got.Action)
	}
	if got.Layer != LayerReflex || got.FallThrough {
		t.Fatalf("a zero-context answer must not be marked as a fall-through: %+v", got)
	}
}

func TestALayerThatDeclinesIsNotAFailure(t *testing.T) {
	declines := fakeLayer{name: "declines", answers: map[string]string{}}
	answers := fakeLayer{name: "answers", answers: map[string]string{"a": "two"}}
	got := Route("a", []Answerer{declines, answers})
	if got.By != "answers" {
		t.Fatalf("a declining layer must be skipped, not treated as an error, got %q", got.By)
	}
}

func TestNothingAnsweringIsAFallThrough(t *testing.T) {
	got := Route("a", []Answerer{fakeLayer{name: "x", answers: map[string]string{}}})
	if !got.FallThrough || got.Layer != LayerFull {
		t.Fatalf("want a fall-through, got %+v", got)
	}
	if got.ContextTokens != 0 {
		t.Fatalf("the router composes no context itself, got %d", got.ContextTokens)
	}
	if got.Reason == "" {
		t.Fatal("a fall-through must say why, or the caller cannot argue with it")
	}
}

func TestANilLayerIsSkipped(t *testing.T) {
	got := Route("a", []Answerer{nil, fakeLayer{name: "real", answers: map[string]string{"a": "x"}}})
	if got.By != "real" {
		t.Fatalf("a nil layer must be skipped, got %q", got.By)
	}
}

func TestNoLayersAtAllIsAFallThrough(t *testing.T) {
	got := Route("a", nil)
	if !got.FallThrough {
		t.Fatal("with no layers there is nothing that could answer")
	}
}

func TestAZeroContextAnswerCostsZeroContext(t *testing.T) {
	got := Route("a", []Answerer{fakeLayer{name: "r", answers: map[string]string{"a": "x"}}})
	if got.ContextTokens != 0 {
		t.Fatalf("answering from no context must report no context, got %d", got.ContextTokens)
	}
}

func TestTheRateIsTheShareAnsweredWithNoContext(t *testing.T) {
	rs := []Record{
		{TaskID: "1", Layer: LayerReflex},
		{TaskID: "2", Layer: LayerReflex},
		{TaskID: "3", Layer: LayerFull},
		{TaskID: "4", Layer: LayerFull},
	}
	s := Summarise(rs)
	if s.Total != 4 {
		t.Fatalf("want 4 arrivals, got %d", s.Total)
	}
	if s.InterceptRate != 0.5 {
		t.Fatalf("want 0.5, got %v", s.InterceptRate)
	}
}

func TestAnArrivalWithNoLayerCountsAsTheExpensivePath(t *testing.T) {
	// Dropping it would raise the rate by forgetting the cases that went badly.
	s := Summarise([]Record{{TaskID: "1", Layer: LayerReflex}, {TaskID: "2"}})
	if s.Total != 2 {
		t.Fatalf("an arrival with no layer must still be counted, got %d", s.Total)
	}
	if s.ByLayer[LayerFull] != 1 {
		t.Fatalf("want it counted as a fall-through, got %v", s.ByLayer)
	}
	if s.InterceptRate != 0.5 {
		t.Fatalf("want 0.5, got %v", s.InterceptRate)
	}
}

func TestTheCountsAddUpToTheTotal(t *testing.T) {
	s := Summarise([]Record{
		{TaskID: "1", Layer: LayerReflex},
		{TaskID: "2"},
		{TaskID: "3", Layer: LayerReflex},
	})
	sum := 0
	for _, n := range s.ByLayer {
		sum += n
	}
	if sum != s.Total {
		t.Fatalf("counts must add up to the total, %d != %d", sum, s.Total)
	}
}

// ★ 率永远带着它的分母。
//
// 「60–80%」之所以是一句没人能核对的话，就是因为没有分母。
func TestTheRateAlwaysCarriesItsDenominator(t *testing.T) {
	s := Summarise([]Record{{TaskID: "1", Layer: LayerReflex}})
	if s.Scope != ScopeArrived {
		t.Fatalf("the rate must say what it is a rate of, got %q", s.Scope)
	}
	empty := Summarise(nil)
	if empty.Scope != ScopeArrived {
		t.Fatal("even an empty report must name its scope")
	}
	if empty.Total != 0 || empty.InterceptRate != 0 {
		t.Fatalf("an empty report is zero, not undefined, got %+v", empty)
	}
}

func TestLayersAreListedInAStableOrder(t *testing.T) {
	s := Summarise([]Record{
		{TaskID: "1", Layer: LayerReflex},
		{TaskID: "2", Layer: LayerFull},
		{TaskID: "3", Layer: LayerReflex},
	})
	got := s.Layers()
	if len(got) != 2 || got[0] != LayerFull || got[1] != LayerReflex {
		t.Fatalf("layers must be sorted, not in map order, got %v", got)
	}
}

func TestSummariseIsDeterministic(t *testing.T) {
	rs := []Record{
		{TaskID: "1", Layer: LayerReflex}, {TaskID: "2"}, {TaskID: "3", Layer: LayerReflex},
	}
	a, b := Summarise(rs), Summarise(rs)
	if a.Total != b.Total || a.InterceptRate != b.InterceptRate {
		t.Fatal("the same arrivals must summarise the same way")
	}
	if len(a.Layers()) != len(b.Layers()) {
		t.Fatal("layer lists differ between two identical folds")
	}
}

func TestAnArrivalWithoutAnIDIsRefused(t *testing.T) {
	ref := Validate(Record{Stimulus: "x"})
	if ref == nil || ref.Code != "bad_request" {
		t.Fatalf("want bad_request, got %v", ref)
	}
}

func TestAnArrivalWithoutAStimulusIsRefused(t *testing.T) {
	ref := Validate(Record{TaskID: "1"})
	if ref == nil || ref.Code != "bad_request" {
		t.Fatalf("want bad_request, got %v", ref)
	}
}

func TestAWellFormedArrivalPasses(t *testing.T) {
	if ref := Validate(Record{TaskID: "1", Stimulus: "x"}); ref != nil {
		t.Fatalf("a well-formed arrival must pass, got %v", ref)
	}
}
