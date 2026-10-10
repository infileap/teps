package core

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// genesis is the parent of the first record: thirty-two zero bytes, in hex.
const genesis = "0000000000000000000000000000000000000000000000000000000000000000"

// Entry is one record of the append-only chain.
//
// Seq, Prev and Head are assigned by the server immediately before the record
// becomes durable. Head commits to Prev and to the normalized body, so any
// later change to any earlier record breaks every Head that follows it.
type Entry struct {
	Seq  uint64          `json:"seq"`
	Key  string          `json:"key"`
	Prev string          `json:"prev"`
	Head string          `json:"head"`
	Body json.RawMessage `json:"body"`
}

// Log is the record file plus the position the chain has reached.
//
// The chain is the whole history's integrity: one altered byte makes every
// later Head stop matching, and a mismatch is a refusal rather than a warning.
type Log struct {
	dir   string
	mu    sync.Mutex
	file  *os.File
	seq   uint64
	head  string
	epoch uint64
	seen  map[string]Entry
}

// Open reads whatever chain already exists in dir, then opens it for appending.
//
// Reading first is not an optimization: it is the recovery contract. A chain
// that does not match is refused at Open, before anything new is written to it.
func Open(dir string) (*Log, *Refusal) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, Refuse(CodeRecoveryRequired, err.Error())
	}
	l := &Log{dir: dir, head: genesis, seen: map[string]Entry{}}
	if r := l.load(); r != nil {
		return nil, r
	}
	if r := l.bumpEpoch(); r != nil {
		return nil, r
	}
	f, err := os.OpenFile(l.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, Refuse(CodeRecoveryRequired, err.Error())
	}
	l.file = f
	return l, nil
}

func (l *Log) path() string { return filepath.Join(l.dir, "events.log") }

// load walks the chain from the first record and refuses at the first break.
func (l *Log) load() *Refusal {
	f, err := os.Open(l.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return Refuse(CodeRecoveryRequired, err.Error())
	}
	defer f.Close()

	prev := genesis
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxFrameBytes+1)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return Refuse(CodeRecoveryRequired, "record "+
				strconv.FormatUint(l.seq+1, 10)+" is not JSON")
		}
		if e.Prev != prev {
			return Refuse(CodeRecoveryRequired, "record "+
				strconv.FormatUint(e.Seq, 10)+": prev does not match the chain")
		}
		if got := link(e.Prev, e.Body); got != e.Head {
			return Refuse(CodeRecoveryRequired, "record "+
				strconv.FormatUint(e.Seq, 10)+": head does not commit to its body")
		}
		l.seq = e.Seq
		l.head = e.Head
		l.seen[e.Key] = e
		prev = e.Head
	}
	if err := sc.Err(); err != nil {
		return Refuse(CodeRecoveryRequired, err.Error())
	}
	return nil
}

// Append adds one record, or returns the record the key already produced.
//
// A2: the key names one logical attempt, not one network request. Repeating it
// is not an error and does not append twice; the original record comes back
// unchanged, including after a restart.
func (l *Log) Append(key string, body []byte) (Entry, *Refusal) {
	if key == "" {
		return Entry{}, Refuse(CodeBadRequest, "an idempotency key is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if e, ok := l.seen[key]; ok {
		return e, nil
	}
	canon, r := Canonical(body)
	if r != nil {
		return Entry{}, r
	}

	e := Entry{Seq: l.seq + 1, Key: key, Prev: l.head, Body: canon}
	e.Head = link(e.Prev, canon)

	line, err := json.Marshal(e)
	if err != nil {
		return Entry{}, Refuse(CodeBadRequest, err.Error())
	}
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return Entry{}, Refuse(CodeRecoveryRequired, err.Error())
	}
	// Durability comes before the position moves. If the sync fails the record
	// may or may not be on disk, and the position must not claim otherwise.
	if err := l.file.Sync(); err != nil {
		return Entry{}, Refuse(CodeRecoveryRequired, err.Error())
	}

	l.seq = e.Seq
	l.head = e.Head
	l.seen[e.Key] = e
	return e, nil
}

// Replay walks the chain in order and hands every record to fn.
func (l *Log) Replay(fn func(Entry) error) *Refusal {
	l.mu.Lock()
	path := l.path()
	l.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return Refuse(CodeRecoveryRequired, err.Error())
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxFrameBytes+1)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return Refuse(CodeRecoveryRequired, err.Error())
		}
		if err := fn(e); err != nil {
			return Refuse(CodeRecoveryRequired, err.Error())
		}
	}
	if err := sc.Err(); err != nil {
		return Refuse(CodeRecoveryRequired, err.Error())
	}
	return nil
}

// MaxTail caps how many records one tail request may return.
//
// The chain is append-only and unbounded; an uncapped read is a request that
// gets slower forever. The cap is the honest form of "read the tail": if you
// need more than this, you are not auditing a tail, you are copying the log,
// and that deserves its own answer rather than a silent truncation.
const MaxTail = 1000

// Tail returns the last `limit` records in chain order.
//
// Reading the chain is a read of the authoritative structure, so it belongs
// here and not in an adapter: what the chain contains is the Log concept's own
// fact, and an adapter that rebuilt the tail by parsing events.log would be a
// second implementation of it — the exact drift this project has paid for
// repeatedly.
//
// The walk is O(n) in the chain, which is honest: the chain is the only place
// the records are, and a cached index would be a second copy of them.
func (l *Log) Tail(limit int) ([]Entry, *Refusal) {
	if limit <= 0 {
		return nil, Refuse(CodeBadRequest, "tail needs a positive limit")
	}
	if limit > MaxTail {
		return nil, Refuse(CodeBadRequest, "tail is capped at "+itoa(MaxTail)+" records")
	}
	ring := make([]Entry, 0, limit)
	if ref := l.Replay(func(e Entry) error {
		if len(ring) == limit {
			copy(ring, ring[1:])
			ring = ring[:limit-1]
		}
		ring = append(ring, e)
		return nil
	}); ref != nil {
		return nil, ref
	}
	return ring, nil
}

// itoa is a local integer formatter, so this file keeps needing no imports
// beyond what it already has.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Head returns the current chain head and the number of records in it.
func (l *Log) Head() (string, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.head, l.seq
}

// Epoch identifies one run of the server over this chain. It grows on every
// Open, so a client holding a stale epoch can tell that the process it was
// talking to is gone.
func (l *Log) Epoch() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

func (l *Log) bumpEpoch() *Refusal {
	p := filepath.Join(l.dir, "epoch")
	var n uint64
	if b, err := os.ReadFile(p); err == nil {
		n, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	n++
	if err := os.WriteFile(p, []byte(strconv.FormatUint(n, 10)), 0o644); err != nil {
		return Refuse(CodeRecoveryRequired, err.Error())
	}
	l.epoch = n
	return nil
}

// Close flushes and releases the record file.
func (l *Log) Close() *Refusal {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	if err != nil {
		return Refuse(CodeRecoveryRequired, err.Error())
	}
	return nil
}

// link is the chain function: the head commits to the parent and the body.
func link(prev string, canon []byte) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{0})
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil))
}
