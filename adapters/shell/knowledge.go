package main

import (
	"encoding/json"
	"sort"
	"strings"

	"sansheng/core"
	"sansheng/evidence"
	"sansheng/organs/composer"
	"sansheng/organs/memory"
)

// Two more organs get a protocol face here: the memory store and the context
// composer.
//
// They are deliberately **not wired to each other**. The contract says organs do
// not call one another; they exchange state through the log and the protocol.
// The adapter exposes each one's own face, and whoever schedules the work
// decides what feeds what. An adapter that composed memory into context would
// have made that decision invisibly, and it is not the adapter's decision.

type memoryRecord struct {
	Rec          string   `json:"rec"`
	Op           string   `json:"op"`
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Evidence     string   `json:"evidence"`
	ValidFrom    int64    `json:"valid_from"`
	ValidUntil   int64    `json:"valid_until"`
	SupersededBy string   `json:"superseded_by"`
	RecordedAt   int64    `json:"recorded_at"`
	Tags         []string `json:"tags"`
	Source       string   `json:"source"`
}

// memoryStore projects the memory organ out of the log.
//
// The same log feeds the session store and this one. That is the point of
// "the event stream is the truth, a capsule is a cache": each organ is an
// independent projection of one append-only record, so neither can be the
// other's authority.
type memoryStore struct {
	mu      chan struct{} // 1-buffered: a mutex that cannot be copied by accident
	log     *core.Log
	store   *memory.Store
	applied uint64
}

func newMemoryStore(lg *core.Log) (*memoryStore, *core.Refusal) {
	st := &memoryStore{mu: make(chan struct{}, 1), log: lg, store: memory.New()}
	st.mu <- struct{}{}
	if ref := lg.Replay(st.absorb); ref != nil {
		return nil, ref
	}
	return st, nil
}

func (st *memoryStore) lock()   { <-st.mu }
func (st *memoryStore) unlock() { st.mu <- struct{}{} }

func (st *memoryStore) absorb(e core.Entry) error {
	var rec memoryRecord
	if err := json.Unmarshal(e.Body, &rec); err != nil || rec.Rec != "memory" {
		return nil // not ours; the log carries several kinds of record
	}
	st.apply(rec)
	if e.Seq > st.applied {
		st.applied = e.Seq
	}
	return nil
}

func (st *memoryStore) apply(rec memoryRecord) {
	_ = st.store.Apply(memory.Event{
		Op:           memory.Op(rec.Op),
		ID:           rec.ID,
		Text:         rec.Text,
		Evidence:     evidence.Level(rec.Evidence),
		ValidFrom:    rec.ValidFrom,
		ValidUntil:   rec.ValidUntil,
		SupersededBy: rec.SupersededBy,
		RecordedAt:   rec.RecordedAt,
		Tags:         rec.Tags,
		Source:       rec.Source,
	})
}

// commit durably records the event, then folds it in. Replaying a duplicate key
// returns the original entry, and the seq check keeps it from being folded
// twice — the same ordering the session store uses, for the same reason.
func (st *memoryStore) commit(rec memoryRecord, key string) (core.Entry, *core.Refusal) {
	if key == "" {
		return core.Entry{}, core.Refuse(core.CodeBadRequest, "an idempotency key is required")
	}
	rec.Rec = "memory"
	body, err := core.Canonical(mustJSON(rec))
	if err != nil {
		return core.Entry{}, err
	}
	entry, ref := st.log.Append(key, body)
	if ref != nil {
		return core.Entry{}, ref
	}
	if entry.Seq > st.applied {
		st.apply(rec)
		st.applied = entry.Seq
	}
	return entry, nil
}

func (s *server) handleKnowledge(f core.Frame, out *core.Frame) bool {
	switch {
	case strings.HasPrefix(f.T, "memory."):
		if s.memory == nil {
			out.Err = core.Refuse(core.CodeBadRequest, "this server was started without a memory store")
			return true
		}
		s.handleMemory(f, out)
		return true
	case strings.HasPrefix(f.T, "compose."):
		s.handleCompose(f, out)
		return true
	}
	return false
}

func (s *server) handleMemory(f core.Frame, out *core.Frame) {
	switch f.T {
	case "memory.write", "memory.supersede":
		var body memoryRecord
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return
		}
		body.Op = strings.TrimPrefix(f.T, "memory.")
		// Validate against the real state, not a fresh empty store. A second
		// write with the same id passes every shape check and is refused only by
		// the state it would collide with, so validating against an empty store
		// validates nothing — and it fails silently: the request is accepted and
		// the old value is quietly overwritten.
		//
		// The clone keeps the check non-mutating while still using the one place
		// the rules live. The lock spans validate-and-append as a single step, or
		// two writers could both pass validation against the same state.
		s.memory.lock()
		probe := s.memory.store.Clone()
		ref := probe.Apply(memory.Event{
			Op: memory.Op(body.Op), ID: body.ID, Text: body.Text,
			Evidence: evidence.Level(body.Evidence), ValidFrom: body.ValidFrom,
			ValidUntil: body.ValidUntil, SupersededBy: body.SupersededBy,
			RecordedAt: body.RecordedAt, Tags: body.Tags, Source: body.Source,
		})
		if ref != nil {
			s.memory.unlock()
			out.Err = ref
			return
		}
		entry, ref := s.memory.commit(body, f.Key)
		s.memory.unlock()
		if ref != nil {
			out.Err = ref
			return
		}
		out.Seq, out.Rev = entry.Seq, entry.Seq
		out.Body = mustJSON(map[string]any{"id": body.ID, "op": body.Op, "head": entry.Head})

	case "memory.retrieve":
		var body struct {
			MinEvidence string   `json:"min_evidence"`
			AsOf        int64    `json:"as_of"`
			Tags        []string `json:"tags"`
		}
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return
		}
		s.memory.lock()
		recs, ref := s.memory.store.Retrieve(memory.Query{
			MinEvidence: evidence.Level(body.MinEvidence),
			AsOf:        body.AsOf,
			Tags:        body.Tags,
		})
		s.memory.unlock()
		if ref != nil {
			out.Err = ref
			return
		}
		ids := make([]string, 0, len(recs))
		texts := make([]string, 0, len(recs))
		for _, r := range recs {
			ids = append(ids, r.ID)
			texts = append(texts, r.Text)
		}
		sort.Strings(ids)
		out.Body = mustJSON(map[string]any{
			"count": len(recs), "ids": ids, "texts": texts, "total": s.memory.store.Len(),
		})

	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
}

type composeCandidate struct {
	ID            string  `json:"id"`
	Section       string  `json:"section"`
	Text          string  `json:"text"`
	Similarity    float64 `json:"similarity"`
	Evidence      string  `json:"evidence"`
	Recency       float64 `json:"recency"`
	TaskRelevance float64 `json:"task_relevance"`
	Redundancy    float64 `json:"redundancy"`
	// Required is the caller saying this material is not optional. The composer
	// will refuse rather than hand back a context that is missing it — see the
	// note on Required in organs/composer.
	Required bool `json:"required"`
}

func (s *server) handleCompose(f core.Frame, out *core.Frame) {
	if f.T != "compose.build" {
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
		return
	}
	var body struct {
		Class      string             `json:"class"`
		Budget     int                `json:"budget"`
		Candidates []composeCandidate `json:"candidates"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	class := composer.TaskClass(body.Class)
	if class != composer.ClassChat && class != composer.ClassTaskExec {
		out.Err = core.Refuse(core.CodeBadRequest, "class must be chat or task_exec")
		return
	}
	cands := make([]composer.Candidate, 0, len(body.Candidates))
	for _, c := range body.Candidates {
		rank := 0.0
		if r, ok := evidence.Rank(evidence.Level(c.Evidence)); ok {
			rank = float64(r) / 4.0
		}
		cands = append(cands, composer.Candidate{
			ID: c.ID, Section: c.Section, Text: c.Text, Len: len(c.Text),
			Similarity: c.Similarity, EvidenceRank: rank, Recency: c.Recency,
			TaskRelevance: c.TaskRelevance, Redundancy: c.Redundancy,
			Required: c.Required,
		})
	}
	res, ref := composer.Compose(composer.Request{
		Class: class, Budget: body.Budget, Split: composer.SplitFor(class),
		Weights: composer.DefaultWeights(), Candidates: cands,
	})
	if ref != nil {
		out.Err = ref
		return
	}
	kept := make([]string, 0, len(res.Kept))
	for _, c := range res.Kept {
		kept = append(kept, c.ID)
	}
	dropped := make([]string, 0, len(res.Dropped))
	for _, c := range res.Dropped {
		dropped = append(dropped, c.ID)
	}
	out.Body = mustJSON(map[string]any{
		"kept": kept, "dropped": dropped, "used": res.Used,
		"allocated": res.Allocated, "order_free": []string{"retrieval"},
	})
}
