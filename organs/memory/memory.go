// Package memory is the fact store, and it is **bi-temporal**.
//
// Two clocks, not one:
//
//   - the **validity** clock — when a fact was true in the world
//     (`ValidFrom` / `ValidUntil`), and
//   - the **record** clock — when we learned it, and when we learned better
//     (`RecordedAt` / `SupersededAt`).
//
// One clock cannot express "we believed X on Tuesday, and X had already stopped
// being true on Monday". Without that distinction a system cannot tell a stale
// fact from a wrong one, so it keeps both forever and grows steadily more
// confident and less correct — while being unable to notice. That failure is
// the reason this package has two clocks and the contract names them.
//
// **Writes are immutable.** A correction is a new record plus a `supersede`
// pointing at the old one. Editing in place would leave no trace that the old
// value was ever believed, which is the same as having no history at all.
//
// The package is pure: it holds no file handles and does no I/O. The adapter
// folds it out of the kernel's log, so a restart reconstructs it exactly.
package memory

import (
	"strings"

	"teps/core"
	"teps/evidence"
)

// Op is a memory operation.
type Op string

const (
	OpWrite     Op = "write"
	OpSupersede Op = "supersede"
)

// Record is one fact, as stored.
type Record struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Evidence evidence.Level `json:"evidence"`

	ValidFrom  int64 `json:"valid_from"`
	ValidUntil int64 `json:"valid_until,omitempty"`

	SupersededBy string `json:"superseded_by,omitempty"`
	SupersededAt int64  `json:"superseded_at,omitempty"`

	RecordedAt int64    `json:"recorded_at"`
	Tags       []string `json:"tags,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// Event is one line of the memory log.
type Event struct {
	Op       Op             `json:"op"`
	ID       string         `json:"id"`
	Text     string         `json:"text,omitempty"`
	Evidence evidence.Level `json:"evidence,omitempty"`

	ValidFrom  int64 `json:"valid_from,omitempty"`
	ValidUntil int64 `json:"valid_until,omitempty"`

	SupersededBy string `json:"superseded_by,omitempty"`

	RecordedAt int64    `json:"recorded_at"`
	Tags       []string `json:"tags,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// Store is a fold over write and supersede events.
type Store struct {
	order []string
	byID  map[string]*Record
}

// New returns an empty store.
func New() *Store {
	return &Store{byID: map[string]*Record{}}
}

// Len is the number of records, superseded ones included.
func (s *Store) Len() int { return len(s.order) }

// Clone returns a deep copy.
//
// It exists so a caller can **validate against the real state without mutating
// it**. Validating against an empty store validates nothing about the store: a
// second write with the same id passes every shape check and is refused only by
// the state it would collide with. That failure is silent — the request is
// accepted and the old value is quietly overwritten.
func (s *Store) Clone() *Store {
	out := &Store{
		order: append([]string(nil), s.order...),
		byID:  make(map[string]*Record, len(s.byID)),
	}
	for id, r := range s.byID {
		cp := *r
		cp.Tags = append([]string(nil), r.Tags...)
		out.byID[id] = &cp
	}
	return out
}

// Apply folds one event. It is the only way records come into existence.
func (s *Store) Apply(ev Event) *core.Refusal {
	switch ev.Op {
	case OpWrite:
		return s.applyWrite(ev)
	case OpSupersede:
		return s.applySupersede(ev)
	}
	return &core.Refusal{Code: core.CodeBadRequest, Reason: "unknown memory op: " + string(ev.Op)}
}

func (s *Store) applyWrite(ev Event) *core.Refusal {
	if strings.TrimSpace(ev.ID) == "" {
		return bad("a memory record needs an id")
	}
	if _, exists := s.byID[ev.ID]; exists {
		// Immutability is the whole point. A second write with the same id is
		// either a bug or an edit in disguise, and an edit in disguise leaves no
		// trace that the old value was ever believed.
		return bad("record already exists; a correction is a supersede, not a second write: " + ev.ID)
	}
	if !evidence.Known(ev.Evidence) {
		return bad("unknown evidence level: " + string(ev.Evidence))
	}
	if ev.RecordedAt <= 0 {
		return bad("a record needs recorded_at: without it the two clocks collapse into one")
	}
	from := ev.ValidFrom
	if from == 0 {
		from = ev.RecordedAt
	}
	if ev.ValidUntil != 0 && ev.ValidUntil < from {
		return bad("valid_until precedes valid_from: " + ev.ID)
	}
	r := &Record{
		ID:         ev.ID,
		Text:       ev.Text,
		Evidence:   ev.Evidence,
		ValidFrom:  from,
		ValidUntil: ev.ValidUntil,
		RecordedAt: ev.RecordedAt,
		Tags:       append([]string(nil), ev.Tags...),
		Source:     ev.Source,
	}
	s.byID[r.ID] = r
	s.order = append(s.order, r.ID)
	return nil
}

func (s *Store) applySupersede(ev Event) *core.Refusal {
	r, ok := s.byID[ev.ID]
	if !ok {
		return bad("no such record to supersede: " + ev.ID)
	}
	if r.SupersededBy != "" || r.SupersededAt != 0 {
		return bad("already superseded: " + ev.ID)
	}
	if ev.SupersededBy == ev.ID {
		return bad("a record cannot supersede itself: " + ev.ID)
	}
	if ev.SupersededBy != "" {
		if _, ok := s.byID[ev.SupersededBy]; !ok {
			// Write-then-supersede is the required order. Allowing the pointer to
			// dangle would make a replay depend on events that had not happened.
			return bad("the replacement must be written before the fact it replaces: " + ev.SupersededBy)
		}
	}
	if ev.RecordedAt <= 0 {
		return bad("a supersede needs recorded_at")
	}
	r.SupersededBy = ev.SupersededBy
	r.SupersededAt = ev.RecordedAt
	if ev.ValidUntil != 0 {
		r.ValidUntil = ev.ValidUntil
	}
	return nil
}

// Get returns a record by id.
func (s *Store) Get(id string) (Record, bool) {
	r, ok := s.byID[id]
	if !ok {
		return Record{}, false
	}
	return *r, true
}

// Current returns the records that are neither superseded nor closed.
func (s *Store) Current() []Record {
	out := make([]Record, 0, len(s.order))
	for _, id := range s.order {
		r := s.byID[id]
		if r.SupersededBy != "" || r.SupersededAt != 0 || r.ValidUntil != 0 {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// AsOf returns what we believed at time t about the world at time t.
//
// Both clocks must agree: the record must already have been learned
// (`RecordedAt <= t`), must not already have been corrected (`SupersededAt`),
// and its validity window must cover t. Dropping either clock turns this into
// an ordinary "latest value" query and loses the ability to ask what was known
// at a past moment.
func (s *Store) AsOf(t int64) []Record {
	out := make([]Record, 0, len(s.order))
	for _, id := range s.order {
		r := s.byID[id]
		if r.RecordedAt > t {
			continue
		}
		if r.SupersededAt != 0 && r.SupersededAt <= t {
			continue
		}
		if r.ValidFrom > t {
			continue
		}
		if r.ValidUntil != 0 && r.ValidUntil <= t {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// DEAD-API: memory 的链只由 Retrieve 折出来
// Chain walks a record's replacement chain, oldest first.
func (s *Store) Chain(id string) []Record {
	var out []Record
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		r, ok := s.byID[id]
		if !ok {
			break
		}
		out = append(out, *r)
		id = r.SupersededBy
	}
	return out
}

// Query filters the store.
type Query struct {
	// MinEvidence is a **hard filter**, not a scoring term. A candidate that is
	// not trusted enough is not a weak candidate; it is not a candidate.
	//
	// Empty means E1. E0 — generated and unverified — is therefore invisible
	// unless a caller names it, which is what "never enters core memory" means
	// in practice: it can be written down, and it cannot be retrieved by
	// accident.
	MinEvidence evidence.Level `json:"min_evidence,omitempty"`

	// AsOf is the bi-temporal probe. Zero means "current".
	AsOf int64 `json:"as_of,omitempty"`

	// Tags are all required, not any.
	Tags []string `json:"tags,omitempty"`
}

// Retrieve returns the records matching a query, in insertion order.
//
// It does **not** pack a budget. Packing is the composer's job, and the memory
// ABI's "retrieve fills a budget" is satisfied at the protocol layer, where the
// adapter joins this filter to the composer's knapsack. Keeping the two apart
// is what lets either be replaced without the other.
func (s *Store) Retrieve(q Query) ([]Record, *core.Refusal) {
	floor := q.MinEvidence
	if floor == "" {
		floor = evidence.L1
	}
	if !evidence.Known(floor) {
		return nil, bad("unknown min_evidence: " + string(floor))
	}
	pool := s.Current()
	if q.AsOf != 0 {
		pool = s.AsOf(q.AsOf)
	}
	out := make([]Record, 0, len(pool))
	for _, r := range pool {
		if !evidence.AtLeast(r.Evidence, floor) {
			continue
		}
		if !hasAllTags(r, q.Tags) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func hasAllTags(r Record, want []string) bool {
	if len(want) == 0 {
		return true
	}
	have := make(map[string]bool, len(r.Tags))
	for _, t := range r.Tags {
		have[t] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func bad(reason string) *core.Refusal {
	return &core.Refusal{Code: core.CodeBadRequest, Reason: reason}
}
