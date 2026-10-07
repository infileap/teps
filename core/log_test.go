package core

import (
	"bytes"
	"os"
	"path/filepath"
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
