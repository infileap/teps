// Package composer decides what goes into a context window, and in what order.
//
// It is a **pure function of its inputs**: candidates in, an ordered selection
// out. It holds no state, touches no file, and does not know whether it is
// feeding a 1B local model or an API call — which is why it is an organ and not
// part of the kernel.
//
// Three decisions in here are not obvious, and each is load-bearing:
//
//  1. **Retrieval fills by score per unit length, not by top-k.** A top-k list
//     spends the budget on whatever scored highest and then truncates; a greedy
//     knapsack spends it on whatever buys the most score per token. Same
//     candidates, strictly more signal for the same budget.
//
//  2. **The strongest material goes at the two ends, not the middle.** Recall in
//     the middle of a long context is measurably worse than at either end. A
//     budget that puts its best material in the middle throws away the material
//     it spent the most on — so this is a correctness property, not formatting.
//
//  3. **Near-duplicate removal and redundancy are two different things.**
//     Dedup is a hard, structural test (SimHash within a small Hamming radius);
//     redundancy is a soft, topical penalty that survives into the score. Doing
//     only one of them leaves either exact repeats or a context that says the
//     same thing four ways.
package composer

import (
	"hash/fnv"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"teps/core"
)

// TaskClass selects a budget split.
//
// The two classes exist because the same total budget should not be divided the
// same way for a conversation and for a task. A chat needs the recent turns; a
// task needs the skills and the retrieved material.
type TaskClass string

const (
	ClassChat     TaskClass = "chat"
	ClassTaskExec TaskClass = "task_exec"
)

// Split is how a context budget is divided. The fractions sum to 1.
type Split struct {
	System      float64
	SkillSchema float64
	ActiveState float64
	Retrieval   float64
	Recent      float64
}

// Sections is the canonical order of the parts of a context.
//
// The order is fixed because it carries meaning: the system prompt comes first
// as precedence, the recent turns come last as chronology. Only the sections
// named in orderFree are reordered internally.
func Sections() []string {
	return []string{"system", "skill_schema", "active_state", "retrieval", "recent"}
}

// orderFree lists the sections whose internal order carries no meaning.
//
// Reordering "recent" would rewrite chronology and reordering "system" would
// rewrite precedence — those are changes in meaning, not in layout. "retrieval"
// is a bag of facts with no inherent order, so it is the one that gets the
// lost-in-the-middle treatment.
var orderFree = map[string]bool{"retrieval": true}

// SplitFor returns the split for a task class.
func SplitFor(c TaskClass) Split {
	switch c {
	case ClassChat:
		return Split{System: 0.20, SkillSchema: 0.05, ActiveState: 0.15, Retrieval: 0.30, Recent: 0.30}
	default:
		return Split{System: 0.15, SkillSchema: 0.15, ActiveState: 0.20, Retrieval: 0.35, Recent: 0.15}
	}
}

// Sum is the total of the fractions.
func (s Split) Sum() float64 {
	return s.System + s.SkillSchema + s.ActiveState + s.Retrieval + s.Recent
}

// fraction returns one section's share, by name.
func (s Split) fraction(name string) float64 {
	switch name {
	case "system":
		return s.System
	case "skill_schema":
		return s.SkillSchema
	case "active_state":
		return s.ActiveState
	case "retrieval":
		return s.Retrieval
	case "recent":
		return s.Recent
	}
	return 0
}

// Validate refuses a split that does not add up.
//
// A split summing to 0.9 silently gives away a tenth of the budget; one summing
// to 1.1 silently overruns it. Neither announces itself, which is why this is a
// refusal and not a normalisation.
func (s Split) Validate() *core.Refusal {
	parts := []float64{s.System, s.SkillSchema, s.ActiveState, s.Retrieval, s.Recent}
	for _, p := range parts {
		if p < 0 {
			return &core.Refusal{Code: core.CodeBadRequest, Reason: "a split fraction must not be negative"}
		}
	}
	if math.Abs(s.Sum()-1.0) > 1e-9 {
		return &core.Refusal{
			Code:   core.CodeBadRequest,
			Reason: "split fractions must sum to 1, got " + trimFloat(s.Sum()),
		}
	}
	return nil
}

// Allocate turns fractions into integer budgets, and gives the rounding
// remainder to retrieval.
//
// Retrieval gets the remainder because it is the section that grows with the
// corpus, and because a budget that silently loses a few tokens to rounding is
// a budget that cannot be asserted against.
func Allocate(budget int, s Split) map[string]int {
	out := map[string]int{}
	used := 0
	for _, name := range Sections() {
		share := int(float64(budget) * s.fraction(name))
		out[name] = share
		used += share
	}
	out["retrieval"] += budget - used
	return out
}

// Weights is the scoring function's coefficients.
type Weights struct {
	Similarity    float64
	Evidence      float64
	Recency       float64
	TaskRelevance float64
	Redundancy    float64 // subtracted
}

// DefaultWeights is the starting point, not a tuned answer.
//
// Task relevance outweighs similarity because a context is assembled for a
// task, not for a topic; evidence outweighs recency because a stale fact that
// was verified is worth more than a fresh one that was guessed.
func DefaultWeights() Weights {
	return Weights{Similarity: 1.0, Evidence: 0.8, Recency: 0.4, TaskRelevance: 1.2, Redundancy: 0.6}
}

// Candidate is one thing that could go into the context.
type Candidate struct {
	ID      string
	Section string
	Text    string
	Len     int

	Similarity    float64
	EvidenceRank  float64
	Recency       float64
	TaskRelevance float64
	Redundancy    float64

	// Required marks material the caller says is not optional — the system
	// prompt, the skill schema, the live state.
	//
	// It exists because of how this kind of failure behaves: a context that is
	// missing its essentials does not fail loudly. It produces a plausible
	// wrong answer, and nothing downstream can tell that it was assembled from
	// an incomplete budget.
	Required bool
}

// Score is the ranking function.
//
// Redundancy is the only subtracted term. That is deliberate: everything else
// argues for including a candidate, and redundancy is the one thing that argues
// against — so it must not be folded into any of the others.
func Score(c Candidate, w Weights) float64 {
	return w.Similarity*c.Similarity +
		w.Evidence*c.EvidenceRank +
		w.Recency*c.Recency +
		w.TaskRelevance*c.TaskRelevance -
		w.Redundancy*c.Redundancy
}

// tokens splits text into the units SimHash hashes.
//
// CJK has no spaces, so a run of Han characters would otherwise collapse into a
// single token and make every Chinese sentence look unlike every other one.
// Han runes are emitted one at a time; everything else is lowercased and split
// on non-alphanumerics.
func tokens(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			out = append(out, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

// Fingerprint is a 64-bit SimHash of the text.
func Fingerprint(text string) uint64 {
	var acc [64]int
	for _, tok := range tokens(text) {
		h := fnv.New64a()
		h.Write([]byte(tok))
		v := h.Sum64()
		for i := 0; i < 64; i++ {
			if v&(1<<uint(i)) != 0 {
				acc[i]++
			} else {
				acc[i]--
			}
		}
	}
	var out uint64
	for i := 0; i < 64; i++ {
		if acc[i] > 0 {
			out |= 1 << uint(i)
		}
	}
	return out
}

// Hamming is the number of differing bits.
func Hamming(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// DefaultMaxDistance is the near-duplicate radius, in bits of 64.
//
// It is **measured, not chosen**: for sentence-length English, a one-or-two-word
// change lands at about 6 bits and genuinely unrelated material at about 32.
// 12 sits between them with margin on both sides. The test asserts that
// separation rather than the number, so this constant cannot drift into a value
// that stops distinguishing the two.
//
// The trade is asymmetric on purpose: the cost of dropping one of two sentences
// that say the same thing is small, and the cost of keeping both is a budget
// spent twice on one fact.
const DefaultMaxDistance = 12

// byScore returns a copy sorted by score, strongest first.
//
// The sort is stable so that equal scores keep their input order, which makes
// the whole composer deterministic without needing a tie-break rule.
func byScore(cands []Candidate, w Weights) []Candidate {
	out := make([]Candidate, len(cands))
	copy(out, cands)
	sort.SliceStable(out, func(i, j int) bool {
		return Score(out[i], w) > Score(out[j], w)
	})
	return out
}

// Dedup drops near-duplicates, keeping the stronger of each pair.
//
// It is O(n²) in the number of candidates. That is acceptable because n is the
// size of a context, not the size of a corpus — the corpus was already narrowed
// before it got here.
func Dedup(cands []Candidate, w Weights, maxDistance int) (kept, dropped []Candidate) {
	sorted := byScore(cands, w)
	seen := make([]uint64, 0, len(sorted))
	for _, c := range sorted {
		fp := Fingerprint(c.Text)
		hit := -1
		for i, s := range seen {
			if Hamming(fp, s) <= maxDistance {
				hit = i
				break
			}
		}
		if hit < 0 {
			kept = append(kept, c)
			seen = append(seen, fp)
			continue
		}
		// A required candidate never loses a near-duplicate contest. Letting it
		// lose would drop material the caller said was not optional, and the
		// surviving text would read fine — which is exactly why it would go
		// unnoticed.
		if c.Required && !kept[hit].Required {
			dropped = append(dropped, kept[hit])
			kept[hit] = c
			seen[hit] = fp
			continue
		}
		dropped = append(dropped, c)
	}
	return kept, dropped
}

// Pack fills a budget greedily by score per unit length.
//
// An item that does not fit is **skipped, not fatal**: the loop continues, so a
// single long candidate cannot block the short ones behind it.
//
// Both halves are returned. A packer that reports only what it kept makes the
// budget unauditable — you cannot tell whether material was dropped for being
// weak or for being long.
func Pack(cands []Candidate, w Weights, budget int) (kept, dropped []Candidate) {
	type row struct {
		c     Candidate
		ratio float64
	}
	rows := make([]row, 0, len(cands))
	for _, c := range cands {
		l := c.Len
		if l <= 0 {
			l = 1
		}
		rows = append(rows, row{c: c, ratio: Score(c, w) / float64(l)})
	}
	// Required material is placed first, then the rest by score per unit length.
	//
	// Ordering matters here for the same reason it does in Dedup: without it a
	// low-scoring essential can be crowded out by high-scoring optional
	// material, and the result is a context that looks full and is missing the
	// one thing it could not do without.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].c.Required != rows[j].c.Required {
			return rows[i].c.Required
		}
		return rows[i].ratio > rows[j].ratio
	})

	used := 0
	for _, r := range rows {
		if used+r.c.Len <= budget {
			kept = append(kept, r.c)
			used += r.c.Len
			continue
		}
		dropped = append(dropped, r.c)
	}
	return kept, dropped
}

// Order puts the strongest candidates at the beginning and the end, and the
// weakest in the middle.
//
// This is the lost-in-the-middle correction. It is applied only to sections
// whose internal order carries no meaning (see orderFree).
func Order(cands []Candidate, w Weights) []Candidate {
	sorted := byScore(cands, w)
	out := make([]Candidate, len(sorted))
	lo, hi := 0, len(sorted)-1
	for i, c := range sorted {
		if i%2 == 0 {
			out[lo] = c
			lo++
			continue
		}
		out[hi] = c
		hi--
	}
	return out
}

// Request is one composition.
type Request struct {
	Class      TaskClass
	Budget     int
	Split      Split // optional; SplitFor(Class) when zero
	Weights    Weights
	Candidates []Candidate
}

// Result is what was kept, what was dropped, and where the budget went.
type Result struct {
	Kept      []Candidate
	Dropped   []Candidate
	Split     Split
	Allocated map[string]int
	Used      int
}

// Compose runs the whole pipeline: allocate, dedup, pack, order.
func Compose(req Request) (Result, *core.Refusal) {
	if req.Budget < 0 {
		return Result{}, &core.Refusal{Code: core.CodeBadRequest, Reason: "budget must not be negative"}
	}
	sp := req.Split
	if sp == (Split{}) {
		sp = SplitFor(req.Class)
	}
	if ref := sp.Validate(); ref != nil {
		return Result{}, ref
	}
	w := req.Weights
	if w == (Weights{}) {
		w = DefaultWeights()
	}
	alloc := Allocate(req.Budget, sp)

	bySection := map[string][]Candidate{}
	for _, c := range req.Candidates {
		bySection[c.Section] = append(bySection[c.Section], c)
	}

	res := Result{Split: sp, Allocated: alloc}
	for _, name := range Sections() {
		group := bySection[name]
		if len(group) == 0 {
			continue
		}
		kept, dropped := Dedup(group, w, DefaultMaxDistance)
		kept, tooBig := Pack(kept, w, alloc[name])
		res.Dropped = append(res.Dropped, dropped...)
		res.Dropped = append(res.Dropped, tooBig...)
		if orderFree[name] {
			kept = Order(kept, w)
		} else {
			kept = restoreOrder(group, kept)
		}
		res.Kept = append(res.Kept, kept...)
		for _, c := range kept {
			res.Used += c.Len
		}
	}
	// Required material that still does not fit is a refusal, not a smaller
	// context.
	//
	// This is the one rule taken from the architecture note: 超了就拆，绝不扩窗.
	// Growing the window would hide the problem; answering from an incomplete
	// context would not hide it at all — it would dress it up.
	for _, c := range res.Dropped {
		if !c.Required {
			continue
		}
		return Result{}, &core.Refusal{
			Code: core.CodeBudgetExhausted,
			Reason: "required material does not fit: " + c.ID + " in section " + c.Section +
				" needs " + strconv.Itoa(c.Len) + " and that section's share is " +
				strconv.Itoa(alloc[c.Section]) + ". Decompose the task rather than growing the window: " +
				"a context missing its essentials does not fail loudly, it answers plausibly",
		}
	}
	return res, nil
}

// restoreOrder returns the kept candidates in their original input order.
//
// Packing sorts by score per unit length, which is right for *choosing* what
// fits and wrong for *placing* it. A section whose order carries meaning —
// "recent" is chronology, "system" is precedence — must get its order back
// after the selection is made. Without this, packing silently rewrites the
// conversation.
//
// IDs must be unique within a section for this to be exact; that is a
// precondition of Candidate, not a hope.
func restoreOrder(input, kept []Candidate) []Candidate {
	keep := make(map[string]bool, len(kept))
	for _, c := range kept {
		keep[c.ID] = true
	}
	out := make([]Candidate, 0, len(kept))
	for _, c := range input {
		if keep[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// trimFloat renders a float for refusal text without a trailing run of zeros.
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}
