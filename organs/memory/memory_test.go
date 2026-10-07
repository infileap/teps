package memory

import (
	"strings"
	"testing"

	"sansheng/evidence"
)

func write(id, text string, ev evidence.Level, recorded int64) Event {
	return Event{Op: OpWrite, ID: id, Text: text, Evidence: ev, RecordedAt: recorded}
}

func mustApply(t *testing.T, s *Store, ev Event) {
	t.Helper()
	if ref := s.Apply(ev); ref != nil {
		t.Fatalf("apply %v %s: unexpected refusal %s: %s", ev.Op, ev.ID, ref.Code, ref.Reason)
	}
}

func mustRefuse(t *testing.T, s *Store, ev Event) string {
	t.Helper()
	ref := s.Apply(ev)
	if ref == nil {
		t.Fatalf("apply %v %s: expected a refusal, got none", ev.Op, ev.ID)
	}
	return ref.Code
}

func TestWriteThenGet(t *testing.T) {
	s := New()
	mustApply(t, s, write("f1", "the sky is blue", evidence.L2, 100))
	r, ok := s.Get("f1")
	if !ok {
		t.Fatal("f1 must exist")
	}
	if r.Text != "the sky is blue" || r.Evidence != evidence.L2 {
		t.Fatalf("round trip lost data: %+v", r)
	}
	// ValidFrom defaults to RecordedAt when the caller does not supply one.
	if r.ValidFrom != 100 {
		t.Fatalf("valid_from must default to recorded_at, got %d", r.ValidFrom)
	}
}

func TestWriteIsImmutable(t *testing.T) {
	s := New()
	mustApply(t, s, write("f1", "v1", evidence.L2, 100))
	// A second write with the same id is an edit in disguise. Allowing it would
	// leave no trace that the first value was ever believed.
	mustRefuse(t, s, write("f1", "v2", evidence.L2, 200))
	r, _ := s.Get("f1")
	if r.Text != "v1" {
		t.Fatalf("the first value must survive, got %q", r.Text)
	}
}

func TestWriteRefusesMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{"no id", write("", "x", evidence.L1, 1)},
		{"blank id", write("   ", "x", evidence.L1, 1)},
		{"unknown evidence", write("f", "x", "E9", 1)},
		{"no recorded_at", write("f", "x", evidence.L1, 0)},
		{"valid_until before valid_from", Event{Op: OpWrite, ID: "f", Evidence: evidence.L1, RecordedAt: 10, ValidFrom: 10, ValidUntil: 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustRefuse(t, New(), c.ev)
		})
	}
}

func TestUnknownOpIsRefused(t *testing.T) {
	ref := New().Apply(Event{Op: "forget", ID: "f", RecordedAt: 1})
	if ref == nil || ref.Code != "bad_request" {
		t.Fatalf("an unknown op must be refused, got %+v", ref)
	}
}

func TestSupersedeNeedsTheReplacementWrittenFirst(t *testing.T) {
	s := New()
	mustApply(t, s, write("old", "v1", evidence.L1, 100))
	// Write-then-supersede is the required order. A dangling pointer would make
	// a replay depend on an event that had not happened yet.
	mustRefuse(t, s, Event{Op: OpSupersede, ID: "old", SupersededBy: "new", RecordedAt: 200})
	mustApply(t, s, write("new", "v2", evidence.L2, 200))
	mustApply(t, s, Event{Op: OpSupersede, ID: "old", SupersededBy: "new", RecordedAt: 200})

	r, _ := s.Get("old")
	if r.SupersededBy != "new" || r.SupersededAt != 200 {
		t.Fatalf("supersede did not land: %+v", r)
	}
}

func TestSupersedeIsOnceAndNotSelfReferential(t *testing.T) {
	s := New()
	mustApply(t, s, write("a", "x", evidence.L1, 1))
	mustApply(t, s, write("b", "y", evidence.L1, 2))
	mustRefuse(t, s, Event{Op: OpSupersede, ID: "a", SupersededBy: "a", RecordedAt: 3})
	mustApply(t, s, Event{Op: OpSupersede, ID: "a", SupersededBy: "b", RecordedAt: 3})
	mustRefuse(t, s, Event{Op: OpSupersede, ID: "a", SupersededBy: "b", RecordedAt: 4})
}

func TestSupersedeOfAMissingRecordIsRefused(t *testing.T) {
	mustRefuse(t, New(), Event{Op: OpSupersede, ID: "ghost", RecordedAt: 1})
}

func TestCurrentExcludesSupersededAndClosed(t *testing.T) {
	s := New()
	mustApply(t, s, write("a", "x", evidence.L1, 1))
	mustApply(t, s, write("b", "y", evidence.L1, 2))
	mustApply(t, s, write("c", "z", evidence.L1, 3))
	mustApply(t, s, Event{Op: OpSupersede, ID: "a", SupersededBy: "b", RecordedAt: 4})
	// c's validity window has closed, so it is no longer current either.
	mustApply(t, s, Event{Op: OpSupersede, ID: "c", RecordedAt: 5, ValidUntil: 4})

	cur := s.Current()
	if len(cur) != 1 || cur[0].ID != "b" {
		t.Fatalf("want only b current, got %+v", ids(cur))
	}
	if s.Len() != 3 {
		t.Fatalf("superseding must not delete: want 3 records, got %d", s.Len())
	}
}

// The whole reason there are two clocks.
func TestAsOfUsesBothClocks(t *testing.T) {
	s := New()
	// Learned at t=100 that X was true from t=50.
	mustApply(t, s, Event{Op: OpWrite, ID: "x", Text: "x", Evidence: evidence.L1, RecordedAt: 100, ValidFrom: 50})
	// At t=200 we learn that X stopped being true at t=150.
	mustApply(t, s, Event{Op: OpSupersede, ID: "x", RecordedAt: 200, ValidUntil: 150})

	if got := s.AsOf(40); len(got) != 0 {
		t.Fatalf("at t=40 the fact was not yet true: %+v", ids(got))
	}
	if got := s.AsOf(80); len(got) != 0 {
		// t=80 is inside the validity window but before we learned it. A
		// one-clock store cannot express this and would wrongly return it.
		t.Fatalf("at t=80 we did not know it yet: %+v", ids(got))
	}
	if got := s.AsOf(120); len(got) != 1 {
		t.Fatalf("at t=120 it was true and known: %+v", ids(got))
	}
	if got := s.AsOf(160); len(got) != 0 {
		// t=160 is after the fact stopped being true, even though we only
		// learned that at t=200.
		t.Fatalf("at t=160 it was no longer true: %+v", ids(got))
	}
	if got := s.AsOf(250); len(got) != 0 {
		t.Fatalf("at t=250 it is known to be over: %+v", ids(got))
	}
	// The record itself is never destroyed.
	if s.Len() != 1 {
		t.Fatalf("want the record kept, got %d", s.Len())
	}
}

func TestRetrieveDefaultsToE1SoE0IsInvisible(t *testing.T) {
	s := New()
	mustApply(t, s, write("guess", "a guess", evidence.L0, 1))
	mustApply(t, s, write("ran", "it ran once", evidence.L1, 2))

	got, ref := s.Retrieve(Query{})
	if ref != nil {
		t.Fatalf("unexpected refusal: %s", ref.Reason)
	}
	if len(got) != 1 || got[0].ID != "ran" {
		t.Fatalf("E0 must not surface by default, got %+v", ids(got))
	}

	// It can still be asked for by name — that is what "written down but not
	// retrieved by accident" means.
	got, _ = s.Retrieve(Query{MinEvidence: evidence.L0})
	if len(got) != 2 {
		t.Fatalf("naming E0 must surface it, got %+v", ids(got))
	}
}

func TestMinEvidenceIsAHardFilterNotAScore(t *testing.T) {
	s := New()
	mustApply(t, s, write("a", "x", evidence.L1, 1))
	mustApply(t, s, write("b", "y", evidence.L4, 2))

	got, _ := s.Retrieve(Query{MinEvidence: evidence.L3})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("want only the E4 record, got %+v", ids(got))
	}
	// A candidate that is not trusted enough is not a weak candidate; it is not
	// a candidate. Nothing about "a" may leak into a result at this floor.
	for _, r := range got {
		if r.ID == "a" {
			t.Fatal("an under-evidenced record must not appear at all")
		}
	}
}

func TestRetrieveRefusesUnknownFloor(t *testing.T) {
	_, ref := New().Retrieve(Query{MinEvidence: "E9"})
	if ref == nil || ref.Code != "bad_request" {
		t.Fatalf("an unknown floor must be refused, got %+v", ref)
	}
}

func TestRetrieveTagsAreAllRequired(t *testing.T) {
	s := New()
	mustApply(t, s, Event{Op: OpWrite, ID: "a", Text: "x", Evidence: evidence.L1, RecordedAt: 1, Tags: []string{"os", "win"}})
	mustApply(t, s, Event{Op: OpWrite, ID: "b", Text: "y", Evidence: evidence.L1, RecordedAt: 2, Tags: []string{"os"}})

	got, _ := s.Retrieve(Query{Tags: []string{"os", "win"}})
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("all tags must be required, got %+v", ids(got))
	}
	got, _ = s.Retrieve(Query{Tags: []string{"os"}})
	if len(got) != 2 {
		t.Fatalf("one matching tag must match both, got %+v", ids(got))
	}
}

func TestChainWalksTheReplacements(t *testing.T) {
	s := New()
	mustApply(t, s, write("v1", "one", evidence.L1, 1))
	mustApply(t, s, write("v2", "two", evidence.L2, 2))
	mustApply(t, s, write("v3", "three", evidence.L3, 3))
	mustApply(t, s, Event{Op: OpSupersede, ID: "v1", SupersededBy: "v2", RecordedAt: 4})
	mustApply(t, s, Event{Op: OpSupersede, ID: "v2", SupersededBy: "v3", RecordedAt: 5})

	chain := s.Chain("v1")
	if len(chain) != 3 {
		t.Fatalf("want a chain of 3, got %+v", ids(chain))
	}
	if chain[0].ID != "v1" || chain[2].ID != "v3" {
		t.Fatalf("chain must be oldest first: %+v", ids(chain))
	}
}

// Replay is the recovery story: the same events must produce the same store.
func TestReplayIsDeterministic(t *testing.T) {
	events := []Event{
		write("a", "x", evidence.L1, 10),
		write("b", "y", evidence.L2, 20),
		{Op: OpSupersede, ID: "a", SupersededBy: "b", RecordedAt: 30},
	}
	first := New()
	second := New()
	for _, ev := range events {
		mustApply(t, first, ev)
		mustApply(t, second, ev)
	}
	if first.Len() != second.Len() {
		t.Fatalf("replay diverged: %d vs %d", first.Len(), second.Len())
	}
	for _, id := range []string{"a", "b"} {
		x, _ := first.Get(id)
		y, _ := second.Get(id)
		if x.ID != y.ID || x.Text != y.Text || x.Evidence != y.Evidence ||
			x.SupersededBy != y.SupersededBy || x.SupersededAt != y.SupersededAt ||
			x.ValidFrom != y.ValidFrom || x.ValidUntil != y.ValidUntil ||
			x.RecordedAt != y.RecordedAt || strings.Join(x.Tags, ",") != strings.Join(y.Tags, ",") {
			t.Fatalf("replay diverged on %s: %+v vs %+v", id, x, y)
		}
	}
}

func ids(rs []Record) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}
