package composer

import (
	"strconv"
	"strings"
	"testing"
)

func cand(id, section, text string, score float64) Candidate {
	return Candidate{
		ID:            id,
		Section:       section,
		Text:          text,
		Len:           len(text),
		Similarity:    score,
		TaskRelevance: score,
	}
}

func ids(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestBothSplitsSumToOne(t *testing.T) {
	for _, c := range []TaskClass{ClassChat, ClassTaskExec} {
		if ref := SplitFor(c).Validate(); ref != nil {
			t.Fatalf("%s split does not validate: %s", c, ref.Reason)
		}
	}
}

func TestChatAndTaskExecSplitDifferently(t *testing.T) {
	chat := SplitFor(ClassChat)
	task := SplitFor(ClassTaskExec)
	// A chat needs the recent turns; a task needs the retrieved material.
	if chat.Recent <= task.Recent {
		t.Fatal("a chat must weight recent turns more than a task does")
	}
	if task.Retrieval <= chat.Retrieval {
		t.Fatal("a task must weight retrieval more than a chat does")
	}
}

func TestSplitValidateRefusesBadSums(t *testing.T) {
	bad := Split{System: 0.1, SkillSchema: 0.1, ActiveState: 0.1, Retrieval: 0.1, Recent: 0.1}
	if ref := bad.Validate(); ref == nil {
		t.Fatal("a split summing to 0.5 must be refused")
	}
	over := Split{System: 0.5, SkillSchema: 0.5, ActiveState: 0.5, Retrieval: 0.5, Recent: 0.5}
	if ref := over.Validate(); ref == nil {
		t.Fatal("a split summing to 2.5 must be refused")
	}
	neg := Split{System: -0.1, SkillSchema: 0.3, ActiveState: 0.2, Retrieval: 0.3, Recent: 0.3}
	if ref := neg.Validate(); ref == nil {
		t.Fatal("a negative fraction must be refused")
	}
}

func TestAllocateNeverExceedsTheBudget(t *testing.T) {
	for _, budget := range []int{0, 1, 7, 100, 999, 1000, 12345} {
		for _, c := range []TaskClass{ClassChat, ClassTaskExec} {
			alloc := Allocate(budget, SplitFor(c))
			sum := 0
			for _, n := range Sections() {
				sum += alloc[n]
			}
			if sum != budget {
				t.Fatalf("budget %d (%s): sections sum to %d", budget, c, sum)
			}
		}
	}
}

// Rounding must not silently evaporate tokens, and the remainder has to land
// somewhere predictable.
func TestAllocateGivesTheRemainderToRetrieval(t *testing.T) {
	alloc := Allocate(1000, Split{System: 0.15, SkillSchema: 0.15, ActiveState: 0.20, Retrieval: 0.35, Recent: 0.15})
	if alloc["system"] != 150 || alloc["recent"] != 150 {
		t.Fatalf("exact shares must be exact: %+v", alloc)
	}
	if alloc["retrieval"] != 350 {
		t.Fatalf("want retrieval 350, got %d", alloc["retrieval"])
	}
}

func TestFingerprintIsStable(t *testing.T) {
	a := Fingerprint("the quick brown fox")
	b := Fingerprint("the quick brown fox")
	if a != b {
		t.Fatal("the same text must fingerprint the same")
	}
}

// Realistic near-duplicates: same claim, a word or two changed. Short strings
// make SimHash noisy, which is why the fixtures are sentence-length — that is
// the size of the material this actually sees.
const (
	baseText = "the kernel keeps only four concepts because a concept earns its place by being authoritative or atomic or crossed by every request and nothing else does"
	nearText = "the kernel keeps only four concepts because a concept earns its place by being authoritative or atomic or crossed by every reply and nothing else does"
	farText  = "licensing obligations propagate through the dependency closure so a single strong copyleft member makes the whole package strong copyleft"
)

// The radius is not a taste; it has to actually separate the two populations.
// Asserting the separation rather than a magic number means the test fails if
// the fingerprint or the fixtures drift, instead of passing by coincidence.
func TestFingerprintSeparatesNearFromUnrelated(t *testing.T) {
	base := Fingerprint(baseText)
	near := Hamming(base, Fingerprint(nearText))
	far := Hamming(base, Fingerprint(farText))
	if near >= far {
		t.Fatalf("a near-duplicate (%d) must be closer than unrelated text (%d)", near, far)
	}
	if near > DefaultMaxDistance {
		t.Fatalf("a near-duplicate (%d) must be caught by radius %d", near, DefaultMaxDistance)
	}
	if far <= DefaultMaxDistance {
		t.Fatalf("unrelated text (%d) must not be caught by radius %d", far, DefaultMaxDistance)
	}
	t.Logf("radius %d separates near=%d from far=%d", DefaultMaxDistance, near, far)
}

// CJK has no spaces. Hashing a whole Han run as one token would make every
// Chinese sentence look unlike every other one, so the near-duplicate test
// would never fire on Chinese text.
func TestHanTextIsNotOneToken(t *testing.T) {
	toks := tokens("内核必须微小")
	if len(toks) != 6 {
		t.Fatalf("want 6 Han tokens, got %d: %v", len(toks), toks)
	}
	a := Fingerprint("内核必须控制在微小级别否则不易升级维护")
	b := Fingerprint("内核必须控制在微小级别否则不易升级维修")
	if d := Hamming(a, b); d > DefaultMaxDistance {
		t.Fatalf("near-duplicate Chinese must stay inside the radius, got %d", d)
	}
}

func TestDedupKeepsTheStrongerOfAPair(t *testing.T) {
	weak := cand("weak", "retrieval", baseText, 0.1)
	strong := cand("strong", "retrieval", nearText, 0.9)
	kept, dropped := Dedup([]Candidate{weak, strong}, DefaultWeights(), DefaultMaxDistance)
	if len(kept) != 1 || kept[0].ID != "strong" {
		t.Fatalf("the stronger of a duplicate pair must survive: kept=%v", ids(kept))
	}
	if len(dropped) != 1 || dropped[0].ID != "weak" {
		t.Fatalf("the weaker must be reported as dropped: dropped=%v", ids(dropped))
	}
}

func TestDedupDoesNotDropDistinctMaterial(t *testing.T) {
	a := cand("a", "retrieval", baseText, 0.9)
	b := cand("b", "retrieval", farText, 0.8)
	kept, dropped := Dedup([]Candidate{a, b}, DefaultWeights(), DefaultMaxDistance)
	if len(kept) != 2 || len(dropped) != 0 {
		t.Fatalf("distinct material must survive: kept=%v dropped=%v", ids(kept), ids(dropped))
	}
}

// A single long candidate must not block the short ones behind it.
func TestPackSkipsWhatDoesNotFitAndKeepsGoing(t *testing.T) {
	long := cand("long", "retrieval", strings.Repeat("x", 500), 1.0)
	small1 := cand("s1", "retrieval", strings.Repeat("y", 10), 0.5)
	small2 := cand("s2", "retrieval", strings.Repeat("z", 10), 0.4)

	kept, dropped := Pack([]Candidate{long, small1, small2}, DefaultWeights(), 50)
	if len(kept) != 2 {
		t.Fatalf("want the two small ones kept, got %v", ids(kept))
	}
	if len(dropped) != 1 || dropped[0].ID != "long" {
		t.Fatalf("the long one must be reported dropped, got %v", ids(dropped))
	}
}

// A packer that reports only what it kept makes the budget unauditable: you
// cannot tell whether material was dropped for being weak or for being long.
func TestPackAccountsForEveryCandidate(t *testing.T) {
	in := []Candidate{
		cand("a", "retrieval", strings.Repeat("a", 30), 0.9),
		cand("b", "retrieval", strings.Repeat("b", 30), 0.8),
		cand("c", "retrieval", strings.Repeat("c", 30), 0.7),
	}
	kept, dropped := Pack(in, DefaultWeights(), 70)
	if len(kept)+len(dropped) != len(in) {
		t.Fatalf("every candidate must be accounted for: kept=%v dropped=%v", ids(kept), ids(dropped))
	}
	used := 0
	for _, c := range kept {
		used += c.Len
	}
	if used > 70 {
		t.Fatalf("packed %d into a budget of 70", used)
	}
}

// Packs by score per unit length, not by score alone.
//
// The first case is the one that actually depends on the ordering: the budget
// fits both, so only the resulting order can show which one was preferred. (A
// budget that fits only one would pass whatever the sort did — which is how a
// mutation survived the first version of this test.)
func TestPackRanksByScorePerUnitLength(t *testing.T) {
	sprawling := cand("sprawling", "retrieval", strings.Repeat("x", 100), 1.0) // 0.01 per unit
	compact := cand("compact", "retrieval", strings.Repeat("y", 10), 0.5)      // 0.05 per unit

	kept, _ := Pack([]Candidate{sprawling, compact}, DefaultWeights(), 200)
	if len(kept) != 2 || kept[0].ID != "compact" {
		t.Fatalf("score per unit length must decide the order, got %v", ids(kept))
	}

	// And with a budget that fits only one, the compact item is the one that
	// survives on merit rather than on being first in the list.
	kept, dropped := Pack([]Candidate{sprawling, compact}, DefaultWeights(), 20)
	if len(kept) != 1 || kept[0].ID != "compact" {
		t.Fatalf("want the compact item kept, got %v", ids(kept))
	}
	if len(dropped) != 1 || dropped[0].ID != "sprawling" {
		t.Fatalf("want the sprawling item dropped, got %v", ids(dropped))
	}
}

// The lost-in-the-middle correction: the strongest material goes at the two
// ends, and the weakest lands in the middle.
func TestOrderPutsTheStrongestAtTheEnds(t *testing.T) {
	in := []Candidate{
		cand("s1", "retrieval", "one", 1.0),
		cand("s2", "retrieval", "two", 0.9),
		cand("s3", "retrieval", "three", 0.8),
		cand("s4", "retrieval", "four", 0.7),
		cand("s5", "retrieval", "five", 0.6),
	}
	out := Order(in, DefaultWeights())
	if len(out) != 5 {
		t.Fatalf("ordering must not change the count, got %d", len(out))
	}
	if out[0].ID != "s1" {
		t.Fatalf("the strongest must come first, got %s", out[0].ID)
	}
	if out[len(out)-1].ID != "s2" {
		t.Fatalf("the second strongest must come last, got %s", out[len(out)-1].ID)
	}
	if out[2].ID != "s5" {
		t.Fatalf("the weakest must land in the middle, got %s", out[2].ID)
	}
}

func TestOrderKeepsEveryCandidate(t *testing.T) {
	in := []Candidate{
		cand("a", "retrieval", "one", 0.3),
		cand("b", "retrieval", "two", 0.2),
		cand("c", "retrieval", "three", 0.1),
	}
	out := Order(in, DefaultWeights())
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.ID] = true
	}
	for _, c := range in {
		if !seen[c.ID] {
			t.Fatalf("ordering dropped %s", c.ID)
		}
	}
}

// Only sections whose internal order carries no meaning get reordered.
// Reordering "recent" would rewrite chronology.
func TestOnlyOrderFreeSectionsAreReordered(t *testing.T) {
	if !orderFree["retrieval"] {
		t.Fatal("retrieval is a bag of facts and must be reorderable")
	}
	for _, s := range []string{"system", "recent", "skill_schema", "active_state"} {
		if orderFree[s] {
			t.Fatalf("%s carries meaning in its order and must not be reordered", s)
		}
	}
}

func TestComposeRespectsTheBudget(t *testing.T) {
	var cands []Candidate
	for i := 0; i < 40; i++ {
		// Distinct material, not 40 copies of one string: copies would be
		// deduplicated away and the packing path would never be exercised.
		text := strings.Repeat("w"+strconv.Itoa(i)+" ", 5)
		cands = append(cands, cand("c"+strconv.Itoa(i), "retrieval", text, float64(40-i)/40))
	}
	res, ref := Compose(Request{Class: ClassTaskExec, Budget: 300, Candidates: cands})
	if ref != nil {
		t.Fatalf("unexpected refusal: %s", ref.Reason)
	}
	if res.Used > 300 {
		t.Fatalf("composed %d into a budget of 300", res.Used)
	}
	if len(res.Kept) == 0 {
		t.Fatal("something must have been kept")
	}
	if len(res.Dropped) == 0 {
		t.Fatal("40 items into 300 bytes must drop something, and must say so")
	}
}

func TestComposeKeepsChronologyOfRecentTurns(t *testing.T) {
	cands := []Candidate{
		cand("r1", "recent", "first turn", 0.1),
		cand("r2", "recent", "second turn", 0.9),
		cand("r3", "recent", "third turn", 0.5),
	}
	res, ref := Compose(Request{Class: ClassChat, Budget: 10000, Candidates: cands})
	if ref != nil {
		t.Fatalf("unexpected refusal: %s", ref.Reason)
	}
	var recent []string
	for _, c := range res.Kept {
		if c.Section == "recent" {
			recent = append(recent, c.ID)
		}
	}
	if len(recent) != 3 || recent[0] != "r1" || recent[2] != "r3" {
		t.Fatalf("recent turns must keep their order, got %v", recent)
	}
}

func TestComposeIsDeterministic(t *testing.T) {
	cands := []Candidate{
		cand("a", "retrieval", "alpha material", 0.5),
		cand("b", "retrieval", "beta material", 0.5),
		cand("c", "retrieval", "gamma material", 0.5),
	}
	first, _ := Compose(Request{Class: ClassTaskExec, Budget: 1000, Candidates: cands})
	second, _ := Compose(Request{Class: ClassTaskExec, Budget: 1000, Candidates: cands})
	if strings.Join(ids(first.Kept), ",") != strings.Join(ids(second.Kept), ",") {
		t.Fatalf("composition must be deterministic: %v vs %v", ids(first.Kept), ids(second.Kept))
	}
}

func TestComposeRefusesANegativeBudget(t *testing.T) {
	if _, ref := Compose(Request{Budget: -1}); ref == nil {
		t.Fatal("a negative budget must be refused")
	}
}

// The redundancy term is the only subtracted one, so it must be able to change
// the order on its own.
func TestRedundancyIsTheOnlySubtractedTerm(t *testing.T) {
	w := DefaultWeights()
	if w.Redundancy <= 0 {
		t.Fatal("redundancy must subtract")
	}
	clean := Candidate{ID: "clean", Section: "retrieval", Len: 10, Similarity: 0.5}
	redundant := Candidate{ID: "redundant", Section: "retrieval", Len: 10, Similarity: 0.5, Redundancy: 0.9}
	if Score(redundant, w) >= Score(clean, w) {
		t.Fatal("a redundant candidate must score below an equally similar clean one")
	}
}
