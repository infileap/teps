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
	// ★★★ 链读不回来的记录不是记录。
	//
	// `Replay` 与 `load` 都用 `bufio.Scanner`，它的上限是
	// `sc.Buffer(make([]byte, 0, 64*1024), MaxFrameBytes+1)` —— 也就是
	// **一行的字节数**。而 `Append` 此前对 `line` 的长度**一个字节都不查**。
	// 于是一条比 `MaxFrameBytes` 大的记录会让 `Replay` 回
	// `recovery_required: bufio.Scanner: token too long`，而 `load` 在
	// `Open` 时跑 —— **服务起不来，链再也读不回来。**
	//
	// 实测：`Append` 一条 2 MiB 的记录之后，`l.Tail(10)` 回的就是上面那句。
	// 那个失败是**永久的、整条的**：不是这一条读不到，是整条链读不到。
	//
	// `ParseFrame` 那条 1 MiB 的入口上限让这件事从适配器走不到 —— 但那是
	// **调用方**守的规矩，而 `Append` 是内核的公开入口。**一个只有调用方遵守的
	// 上限不是一个上限**：下一个人从另一个入口写一条大记录，代价由整条链付。
	//
	// 判据取 `MaxFrameBytes`（不是 +1）：写出去的是 `line + '\n'`，而扫描器的
	// 上限是 `MaxFrameBytes + 1`，两边正好对齐。
	if len(line) > MaxFrameBytes {
		return Entry{}, Refuse(CodeFrameTooLarge,
			"record is "+itoa(len(line))+" bytes and the chain reads one record per line capped at "+
				itoa(MaxFrameBytes)+": a record the chain cannot read back is not a record, and "+
				"writing it would make the whole chain unreadable rather than just this one")
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
//
// ★★★ 它同时受**两个**上限约束，而此前只有一个。
//
// `MaxTail` 是**条数**上限，`MaxFrameBytes` 是**一个帧**的上限 —— 而这两个
// 数是各自独立选出来的，于是条数上限能造出一个违反帧上限的回答。实测：
// 写 5 条 300 KiB 的记录（每条都在帧上限之内），再 `audit.tail` 要
// `limit: 1000`，回答是 **1537486 字节 = 1.47 MiB** —— 而 `MaxFrameBytes`
// 是 1 MiB，它的注释写着「caps **one frame**」。
//
// 受害的是客户端：那条上限告诉它「一帧最多 1 MiB」，于是它按这个开缓冲区，
// 然后在一个 1.47 MiB 的行上断掉。**一个只约束一个方向的上限不是一个上限，
// 是一句话。**
//
// 所以这里从**最新**往回收，收到装不下为止。窗口因此会缩，而缩了是要说出来的：
// 适配器已经报了 `returned` / `first_seq` / `last_seq` / `truncated`，读的人
// 看得出他要的条数没有全拿到。**一个装不下的窗口应当变小并说清，而不是
// 造一个比上限还大的帧。**
func (l *Log) Tail(limit int) ([]Entry, *Refusal) {
	if limit <= 0 {
		return nil, Refuse(CodeBadRequest, "tail needs a positive limit")
	}
	if limit > MaxTail {
		return nil, Refuse(CodeBadRequest, "tail is capped at "+itoa(MaxTail)+" records")
	}
	var all []Entry
	if ref := l.Replay(func(e Entry) error {
		all = append(all, e)
		return nil
	}); ref != nil {
		return nil, ref
	}
	// 从最新往回收。`tailFrameReserve` 留给调用方在 entries 之外还要放的那些
	// 字段（head / seq / epoch / basis / first_seq / last_seq / limit / returned）——
	// 只按 entries 的字节数卡，回答仍然可能越界，而那样卡的就是一个假的界。
	budget := MaxFrameBytes - tailFrameReserve
	out := make([]Entry, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		size := len(all[i].Body) + len(all[i].Key) + len(all[i].Prev) + len(all[i].Head) + 128
		if len(out) > 0 && size > budget {
			break
		}
		out = append(out, all[i])
		budget -= size
	}
	// 反转成链序（最旧在前）。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// tailFrameReserve is the room Tail leaves for everything a tail response carries
// besides the records themselves.
//
// It is a named constant rather than a number inside the loop because it is the
// answer to "why did my window shrink": the response has a head, a seq, an
// epoch, a basis sentence and two window ends, and a budget that ignored them
// would be a budget that is wrong by however much they weigh.
const tailFrameReserve = 8 << 10

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
