package breaker

import (
	"testing"

	"teps/core"
)

func trips(host string, oks []bool, start int64) []Trip {
	var out []Trip
	for i, ok := range oks {
		out = append(out, Trip{Host: host, OK: ok, At: start + int64(i)*1000})
	}
	return out
}

func TestABreakerThatNeverOpensIsNotABreaker(t *testing.T) {
	// Below the threshold it is closed, and it says so.
	ts := trips("/bin/sh", []bool{false, false}, 0)
	if got := StateOf(ts, "/bin/sh", 0); got != Closed {
		t.Fatalf("two failures is not enough to open: got %s", got)
	}
	if ref := Allow(ts, "/bin/sh", 0); ref != nil {
		t.Fatalf("a closed breaker allows: %s", ref.Reason)
	}
	// At the threshold it opens.
	ts = trips("/bin/sh", []bool{false, false, false}, 0)
	if got := StateOf(ts, "/bin/sh", 2500); got != Open {
		t.Fatalf("three in a row opens: got %s", got)
	}
}

func TestASuccessResetsTheCount(t *testing.T) {
	// ★ Counting back from the end is the whole point. A host that fails three
	// times, works once, then fails three more is not the same as one that
	// failed six times, and only the second should be open.
	ts := trips("/bin/sh", []bool{false, false, true, false, false}, 0)
	if got := StateOf(ts, "/bin/sh", 4500); got != Closed {
		t.Fatalf("a success breaks the run, so five trips with a success in the middle is only two in a row: got %s", got)
	}
	ts = trips("/bin/sh", []bool{false, false, false, true, false, false}, 0)
	if got := StateOf(ts, "/bin/sh", 5500); got != Closed {
		t.Fatalf("two in a row after a success is still closed: got %s", got)
	}
}

func TestTheCooldownEndsInExactlyOneProbe(t *testing.T) {
	ts := trips("/bin/sh", []bool{false, false, false}, 0)
	last := ts[len(ts)-1].At

	// Still inside the cooldown: open, and the refusal names the wait.
	inside := last + CooldownMS - 1
	if got := StateOf(ts, "/bin/sh", inside); got != Open {
		t.Fatalf("still open inside the cooldown: got %s", got)
	}
	ref := Allow(ts, "/bin/sh", inside)
	if ref == nil {
		t.Fatal("an open breaker must refuse")
	}
	if ref.Code != core.CodeCircuitOpen {
		t.Fatalf("want circuit_open, got %s", ref.Code)
	}
	if !ref.Retryable {
		t.Fatal("the condition is temporary and the reason names the wait, so a retry can succeed")
	}

	// At the cooldown boundary: half-open, and it admits.
	if got := StateOf(ts, "/bin/sh", last+CooldownMS); got != HalfOpen {
		t.Fatalf("the cooldown ends in half-open: got %s", got)
	}
	if ref := Allow(ts, "/bin/sh", last+CooldownMS); ref != nil {
		t.Fatalf("half-open admits the probe: %s", ref.Reason)
	}
	if !HalfOpenProbe(ts, "/bin/sh", last+CooldownMS) {
		t.Fatal("the caller has to be able to tell that this attempt is the probe")
	}
}

func TestTheReasonNamesHowLongIsLeft(t *testing.T) {
	ts := trips("/bin/sh", []bool{false, false, false}, 0)
	last := ts[len(ts)-1].At
	ref := Allow(ts, "/bin/sh", last+1000)
	if ref == nil {
		t.Fatal("open")
	}
	// "Wait" without a number is not actionable, and a number that is not the
	// real remaining wait is worse than none.
	if !contains(ref.Reason, itoa64(CooldownMS-1000)) {
		t.Fatalf("the refusal should name the remaining wait (%dms): %s", CooldownMS-1000, ref.Reason)
	}
}

func TestOnlyHostFailuresCount(t *testing.T) {
	// ★ A skill whose assertion fails is the skill reporting a true thing about
	// itself. Folding that into the host's record would trip the breaker on the
	// system working.
	for _, v := range []string{"failed", "ok", "unknown", "unable"} {
		if IsHostFailure(v) {
			t.Fatalf("%q is not the host failing", v)
		}
	}
	for _, v := range []string{"refused", "timeout", "Refused", "TIMEOUT"} {
		if !IsHostFailure(v) {
			t.Fatalf("%q is the host failing: it never started, or it never came back", v)
		}
	}
}

func TestOneBrokenHostDoesNotCloseTheOthers(t *testing.T) {
	// The count is per host. Otherwise a broken interpreter would stop every
	// skill on the machine, including the ones that do not use it.
	ts := trips("/bin/broken", []bool{false, false, false}, 0)
	if got := StateOf(ts, "/bin/broken", 2500); got != Open {
		t.Fatalf("the broken host is open: got %s", got)
	}
	if got := StateOf(ts, "/bin/sh", 2500); got != Closed {
		t.Fatalf("a host with no trips of its own is closed: got %s", got)
	}
	if ref := Allow(ts, "/bin/sh", 2500); ref != nil {
		t.Fatalf("the other host must still run: %s", ref.Reason)
	}
}

func TestRecordIsTheOnlyWayATripIsBuilt(t *testing.T) {
	ts := Record(nil, "/bin/sh", false, 100)
	ts = Record(ts, "/bin/sh", false, 200)
	ts = Record(ts, "/bin/sh", true, 300)
	if len(ts) != 3 {
		t.Fatalf("want three trips, got %d", len(ts))
	}
	if got := StateOf(ts, "/bin/sh", 300); got != Closed {
		t.Fatalf("the last one worked: got %s", got)
	}
}

func TestHostsAreSortedSoAReportDoesNotDependOnArrivalOrder(t *testing.T) {
	ts := []Trip{{Host: "c"}, {Host: "a"}, {Host: "c"}, {Host: "b"}}
	got := Hosts(ts)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
