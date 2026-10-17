package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAssignsPositionAndIsIdempotent(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()

	first, ref := l.Append("k1", []byte(`{"b":1,"a":2}`))
	if ref != nil {
		t.Fatal(ref)
	}
	if first.Seq != 1 {
		t.Fatalf("the first record should be 1, got %d", first.Seq)
	}

	// The same key with a differently ordered body is still the same attempt.
	again, ref := l.Append("k1", []byte(`{"a":2,"b":1}`))
	if ref != nil {
		t.Fatal(ref)
	}
	if again.Head != first.Head || again.Seq != first.Seq {
		t.Fatalf("repeating a key must not append twice: %+v then %+v", first, again)
	}
	if string(first.Body) != `{"a":2,"b":1}` {
		t.Fatalf("the body was not normalized: %s", first.Body)
	}
}

func TestEmptyKeyIsRefused(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	if _, ref := l.Append("", []byte(`{}`)); ref == nil || ref.Code != CodeBadRequest {
		t.Fatalf("an empty key should be refused, got %v", ref)
	}
}

func TestTamperedChainIsRefusedAtOpen(t *testing.T) {
	dir := t.TempDir()
	l, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ref := l.Append(k, []byte(`{"v":1}`)); ref != nil {
			t.Fatal(ref)
		}
	}
	l.Close()

	p := filepath.Join(dir, "events.log")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(raw, []byte(`{"v":1}`))
	if i < 0 {
		t.Fatal("could not locate a body to alter")
	}
	raw[i+5] = '2'
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ref := Open(dir); ref == nil || ref.Code != CodeRecoveryRequired {
		t.Fatalf("a chain that does not match must be refused, got %v", ref)
	}
}

func TestEpochGrowsOnEveryOpen(t *testing.T) {
	dir := t.TempDir()
	l1, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	e1 := l1.Epoch()
	l1.Close()

	l2, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	e2 := l2.Epoch()
	l2.Close()

	if e2 <= e1 {
		t.Fatalf("the epoch should grow on every open: %d then %d", e1, e2)
	}
}

func TestPositionAndKeySurviveRestart(t *testing.T) {
	dir := t.TempDir()
	l1, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	e, ref := l1.Append("k", []byte(`{"x":1}`))
	if ref != nil {
		t.Fatal(ref)
	}
	l1.Close()

	l2, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	defer l2.Close()
	head, seq := l2.Head()
	if seq != 1 || head != e.Head {
		t.Fatalf("the position did not survive the restart: seq=%d head=%s", seq, head)
	}
	again, ref := l2.Append("k", []byte(`{"x":1}`))
	if ref != nil || again.Head != e.Head {
		t.Fatalf("the key did not resolve after the restart: %+v %v", again, ref)
	}
}

func TestFrameRefusesServerOwnedFields(t *testing.T) {
	for _, raw := range []string{
		`{"t":"put","v":1,"seq":9}`,
		`{"t":"put","v":1,"rev":9}`,
		`{"t":"put","v":1,"head":"x"}`,
		`{"t":"put","v":1,"balance":9}`,
		`{"t":"put","v":1,"state_hash":"x"}`,
		`{"t":"put","v":1,"committed":true}`,
	} {
		if _, ref := ParseFrame([]byte(raw)); ref == nil || ref.Code != CodeClientAuthority {
			t.Fatalf("%s should be refused as client_authority, got %v", raw, ref)
		}
	}
	if _, ref := ParseFrame([]byte(`{"t":"put","v":1,"key":"k","body":{"a":1}}`)); ref != nil {
		t.Fatalf("a well-formed frame should be accepted, got %v", ref)
	}
}

func TestFrameTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte("a"), MaxFrameBytes+1)
	if _, ref := ParseFrame(big); ref == nil || ref.Code != CodeFrameTooLarge {
		t.Fatalf("an oversized frame should be refused, got %v", ref)
	}
}

func TestBudgetIsAllOrNothing(t *testing.T) {
	a := NewAccount(Budget{MS: 10, Tokens: 10, Money: 10})
	if ref := a.Spend(Budget{MS: 5, Tokens: 20}); ref == nil {
		t.Fatal("overspending one dimension should be refused")
	}
	if left := a.Left(); left.MS != 10 {
		t.Fatalf("a refusal must not charge anything: %+v", left)
	}
	if ref := a.Spend(Budget{MS: 5, Tokens: 5}); ref != nil {
		t.Fatal(ref)
	}
	if left := a.Left(); left.MS != 5 || left.Tokens != 5 {
		t.Fatalf("unexpected remainder: %+v", left)
	}
}

func TestEveryCodeHasARetryAnswer(t *testing.T) {
	for _, c := range []string{
		CodeClientAuthority, CodeNoSession, CodeStaleEpoch, CodeBudgetExhausted,
		CodeBacklogFull, CodeTaintedInput, CodeQuotaExhausted, CodeLicenseConflict,
		CodeLeaseHeld, CodeShadowNested, CodeFrameTooLarge, CodeRecoveryRequired,
		CodeBadRequest,
	} {
		if !Refused(c) {
			t.Fatalf("code %s is in the vocabulary but has no retry answer", c)
		}
	}
}

func TestTailReturnsTheLastRecordsInOrder(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	for i := 1; i <= 5; i++ {
		if _, ref := l.Append("k"+itoa(i), []byte(`{"n":`+itoa(i)+`}`)); ref != nil {
			t.Fatal(ref)
		}
	}
	got, ref := l.Tail(3)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 records, got %d", len(got))
	}
	for i, e := range got {
		if e.Seq != uint64(3+i) {
			t.Fatalf("record %d has seq %d, want %d", i, e.Seq, 3+i)
		}
	}
	// The last tailed record must be the chain head. A tail that ended
	// somewhere else would be a second account of what the chain contains.
	head, seq := l.Head()
	last := got[len(got)-1]
	if last.Head != head || last.Seq != seq {
		t.Fatalf("tail ends at %d/%s but the chain head is %d/%s", last.Seq, last.Head, seq, head)
	}
}

// ★★★ 一个只约束一个方向的上限不是一个上限，是一句话。
//
// `MaxTail` 是**条数**上限，`MaxFrameBytes` 是**一个帧**的上限 —— 两个数各自
// 独立选出来，于是条数上限能造出一个违反帧上限的回答。实测（写 5 条 300 KiB
// 的记录、每条都在帧上限之内，再 `audit.tail` 要 `limit: 1000`）：
//
//	回答 1537486 字节 = 1.47 MiB，而 MaxFrameBytes = 1 MiB
//
// 而 `MaxFrameBytes` 的注释写着「caps **one frame**」。受害的是客户端：那条
// 上限告诉它「一帧最多 1 MiB」，于是它按这个开缓冲区，然后在一个 1.47 MiB
// 的行上断掉。
//
// 现在 Tail 从最新往回收，收到装不下为止 —— 窗口会缩，而缩了是要说出来的
// （适配器报 returned / first_seq / last_seq / truncated）。
func TestTailFitsInsideOneFrame(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	blob := make([]byte, 300*1024)
	for i := range blob {
		blob[i] = 'x'
	}
	body := append([]byte(`{"blob":"`), blob...)
	body = append(body, []byte(`"}`)...)
	for i := 0; i < 5; i++ {
		if _, ref := l.Append("big"+itoa(i), body); ref != nil {
			t.Fatal(ref)
		}
	}
	got, ref := l.Tail(MaxTail)
	if ref != nil {
		t.Fatal(ref)
	}
	total := 0
	for _, e := range got {
		total += len(e.Body) + len(e.Key) + len(e.Prev) + len(e.Head) + 128
	}
	if total > MaxFrameBytes-tailFrameReserve {
		t.Fatalf("the tail weighs %d bytes against a frame budget of %d",
			total, MaxFrameBytes-tailFrameReserve)
	}
	// ★ 它必须**缩**，而不是拒绝，也不是造一个超大的帧。
	if len(got) >= 5 {
		t.Fatalf("five 300 KiB records cannot fit in one frame, got %d", len(got))
	}
	if len(got) == 0 {
		t.Fatal("at least the newest record must come back: a tail that returns nothing " +
			"is a client that cannot read the chain at all")
	}
	// ★ 窗口缩了，但**最新的那条必须在**：尾巴的定义就是它止于链头。
	head, seq := l.Head()
	last := got[len(got)-1]
	if last.Head != head || last.Seq != seq {
		t.Fatalf("the shrunk tail ends at %d/%s but the chain head is %d/%s", last.Seq, last.Head, seq, head)
	}
	// ★ 而链序没有被打乱：seq 仍然递增。
	for i := 1; i < len(got); i++ {
		if got[i].Seq <= got[i-1].Seq {
			t.Fatalf("chain order is broken at %d: %d then %d", i, got[i-1].Seq, got[i].Seq)
		}
	}
}

// 反向对照：装得下的时候，字节预算不许把窗口缩小。
// 没有这一条，上面那条可以被「反正永远只回一条」满足。
func TestTailStillReturnsTheRequestedCountWhenItFits(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	for i := 1; i <= 5; i++ {
		if _, ref := l.Append("k"+itoa(i), []byte(`{"n":1}`)); ref != nil {
			t.Fatal(ref)
		}
	}
	got, ref := l.Tail(5)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(got) != 5 {
		t.Fatalf("five tiny records fit easily, got %d", len(got))
	}
}

// ★ 单条记录本身就超过**窗口预算**（但仍在帧上限之内）时，它仍然必须回来。
//
// 判据里的 `len(out) > 0 &&` 是承重的：没有它，一条装不下窗口预算的最新记录会
// 让窗口变成空的 —— 而「尾巴是空的」与「链是空的」在回答里长得一样，客户端
// 因此**根本读不了这条链**。
//
// 这条记录的大小是刻意选的：它比 `MaxFrameBytes - tailFrameReserve` 大，
// 但比 `MaxFrameBytes` 小 —— 也就是「链收得下、窗口装不下」。
func TestTailReturnsTheNewestRecordEvenWhenItAloneIsTooBigForTheWindow(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	// 填到比窗口预算大、比帧上限小。
	n := MaxFrameBytes - tailFrameReserve + 4096
	body := append([]byte(`{"b":"`), make([]byte, n)...)
	for i := len(body) - n; i < len(body); i++ {
		body[i] = 'y'
	}
	body = append(body, []byte(`"}`)...)
	if _, ref := l.Append("big", body); ref != nil {
		t.Fatalf("a record inside the frame cap must be accepted: %s", ref.Reason)
	}
	got, ref := l.Tail(10)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(got) != 1 {
		t.Fatalf("the newest record must come back even when it alone exceeds the window budget, got %d", len(got))
	}
	head, seq := l.Head()
	if got[0].Head != head || got[0].Seq != seq {
		t.Fatal("the record that came back is not the chain head")
	}
}

// ★★★ 链读不回来的记录不是记录。
//
// `Append` 此前对写出去的那一行**一个字节都不查**。而 `Replay` 与 `load` 都用
// `bufio.Scanner`，上限是 `MaxFrameBytes + 1`。于是一条更大的记录会让 `Replay`
// 回 `recovery_required: bufio.Scanner: token too long`，而 `load` 在 `Open` 时
// 跑 —— **服务起不来，链再也读不回来。** 那个失败是永久的、整条的。
//
// `ParseFrame` 那条入口上限让这件事从适配器走不到，但那是**调用方**守的规矩，
// 而 `Append` 是内核的公开入口。**一个只有调用方遵守的上限不是一个上限。**
func TestAppendRefusesARecordTheChainCouldNotReadBack(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	// 先写一条正常的，证明链是可读的。
	if _, ref := l.Append("ok", []byte(`{"n":1}`)); ref != nil {
		t.Fatal(ref)
	}
	huge := make([]byte, MaxFrameBytes+1024)
	for i := range huge {
		huge[i] = 'z'
	}
	if _, ref := l.Append("huge", append(append([]byte(`{"b":"`), huge...), []byte(`"}`)...)); ref == nil {
		t.Fatal("a record larger than the chain can read back must be refused")
	} else if ref.Code != CodeFrameTooLarge {
		t.Fatalf("want frame_too_large, got %s: %s", ref.Code, ref.Reason)
	}
	// ★ 最要紧的一条：链**仍然读得回来**。拒绝的价值就在这里 ——
	// 写进去的话，坏的不是这一条，是整条链。
	got, ref := l.Tail(10)
	if ref != nil {
		t.Fatalf("the chain must still be readable after a refused append: %s", ref.Reason)
	}
	if len(got) != 1 {
		t.Fatalf("the refused record must not be on the chain, got %d records", len(got))
	}
	// 而且重开一次也读得回来 —— `load` 与 `Replay` 是两条不同的路径。
	if ref := l.Close(); ref != nil {
		t.Fatal(ref)
	}
	again, ref := Open(l.dir)
	if ref != nil {
		t.Fatalf("reopening must read the chain: %s", ref.Reason)
	}
	defer again.Close()
	if _, ref := again.Tail(10); ref != nil {
		t.Fatalf("the chain must be readable after reopen: %s", ref.Reason)
	}
}

// ★★★ 一个会倒退的计数器不是一个计数器。
//
// `bumpEpoch` 此前是 `n, _ = strconv.ParseUint(...)` —— 解析错误被丢掉。
// 于是 epoch 文件里只要有一个字节不是数字，`ParseUint` 回 `(0, err)`、
// `n = 0`、`n++` ⇒ **1**。实测（第一次启动 epoch=1、第二次 2，然后把文件
// 写成 "abc" 再启动）：**epoch 变成 1**。
//
// 而 `Epoch` 的注释逐字写着它的用途：「It grows on every Open, so a client
// holding a stale epoch can tell that the process it was talking to is gone.」
// 一个能**缩小**的 epoch 让那句话反过来：持着 2 的客户端看到 1，于是它以为
// 自己是对的，而它其实在跟另一个进程说话。
func TestACorruptedEpochFileIsRefusedRatherThanReset(t *testing.T) {
	dir := t.TempDir()
	l, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	first := l.Epoch()
	if first == 0 {
		t.Fatal("the epoch must start somewhere")
	}
	if ref := l.Close(); ref != nil {
		t.Fatal(ref)
	}
	l2, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	second := l2.Epoch()
	if second <= first {
		t.Fatalf("the epoch must grow on every Open: %d then %d", first, second)
	}
	if ref := l2.Close(); ref != nil {
		t.Fatal(ref)
	}

	// 把 epoch 文件写成不是数字。
	if err := os.WriteFile(filepath.Join(dir, "epoch"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	l3, ref := Open(dir)
	if ref == nil {
		defer l3.Close()
		t.Fatalf("a counter that can go backwards cannot do its job: Open accepted a corrupt "+
			"epoch file and produced epoch %d (it was %d)", l3.Epoch(), second)
	}
	if ref.Code != CodeRecoveryRequired {
		t.Fatalf("want recovery_required, got %s: %s", ref.Code, ref.Reason)
	}
	if !strings.Contains(ref.Reason, "abc") {
		t.Fatalf("the reason must say what it read, got %q", ref.Reason)
	}
	// ★ 而修好之后必须能起来 —— 拒绝的价值是让人去修，不是把链锁死。
	if err := os.Remove(filepath.Join(dir, "epoch")); err != nil {
		t.Fatal(err)
	}
	l4, ref := Open(dir)
	if ref != nil {
		t.Fatalf("removing the corrupt file must make Open work again: %s", ref.Reason)
	}
	defer l4.Close()
	if l4.Epoch() == 0 {
		t.Fatal("a fresh epoch file starts the counter again, at a nonzero value")
	}
}

// 空文件是「还没有过」而不是「读不懂」：它必须被当成第一次启动。
func TestAnEmptyEpochFileIsNotCorruption(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "epoch"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, ref := Open(dir)
	if ref != nil {
		t.Fatalf("an empty epoch file means the counter has not been written yet: %s", ref.Reason)
	}
	defer l.Close()
	if l.Epoch() != 1 {
		t.Fatalf("epoch = %d, want 1", l.Epoch())
	}
}

func TestTailLongerThanTheChainReturnsTheChain(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	empty, ref := l.Tail(5)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(empty) != 0 {
		t.Fatalf("an empty chain has an empty tail, got %d", len(empty))
	}
	l.Append("k1", []byte(`{"n":1}`))
	got, ref := l.Tail(50)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(got) != 1 {
		t.Fatalf("asking for more than exists returns what exists, got %d", len(got))
	}
}

func TestTailRefusesANonPositiveLimit(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	for _, n := range []int{0, -1} {
		_, ref := l.Tail(n)
		if ref == nil || ref.Code != CodeBadRequest {
			t.Fatalf("limit %d must be refused as a bad request, got %v", n, ref)
		}
	}
}

func TestTailIsCappedRatherThanTruncatedSilently(t *testing.T) {
	l, ref := Open(t.TempDir())
	if ref != nil {
		t.Fatal(ref)
	}
	defer l.Close()
	_, ref = l.Tail(MaxTail + 1)
	if ref == nil || ref.Code != CodeBadRequest {
		t.Fatalf("a request past the cap must be refused, not truncated: got %v", ref)
	}
	if _, ref := l.Tail(MaxTail); ref != nil {
		t.Fatalf("exactly the cap must be allowed, got %v", ref)
	}
}

func TestTailReadsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	l, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	l.Append("k1", []byte(`{"n":1}`))
	before, seqBefore := l.Head()
	if _, ref := l.Tail(10); ref != nil {
		t.Fatal(ref)
	}
	after, seqAfter := l.Head()
	if before != after || seqBefore != seqAfter {
		t.Fatalf("reading the chain changed it: %s/%d -> %s/%d", before, seqBefore, after, seqAfter)
	}
	l.Close()

	// And the tail survives a restart, because it is read from the file and
	// not remembered.
	again, ref := Open(dir)
	if ref != nil {
		t.Fatal(ref)
	}
	defer again.Close()
	got, ref := again.Tail(1)
	if ref != nil {
		t.Fatal(ref)
	}
	if len(got) != 1 || got[0].Head != before {
		t.Fatalf("after restart the tail should still end at %s, got %v", before, got)
	}
}
