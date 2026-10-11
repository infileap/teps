package main

import (
	"testing"

	"teps/organs/pool"
)

// The aggregate and the per-resource view are the same fact, so they have to
// agree — including at the exact boundary, which is the only place a mutation
// of the comparison can hide.
//
// This test exists because a mutation survived without it: flipping
// `Expires > now` to `>= now` in the aggregate changed nothing the drive script
// could observe, because that difference only shows at the single millisecond
// where a lease expires. A boundary is not a detail; it is where the two
// implementations of one fact drift apart.
func TestLiveCountAgreesWithLiveOnTheBoundary(t *testing.T) {
	ls := []pool.Lease{
		{Resource: "a", Holder: "h1", Expires: 200},
		{Resource: "a", Holder: "h2", Expires: 150}, // expires exactly at now
		{Resource: "b", Holder: "h3", Expires: 151},
		{Resource: "b", Holder: "h4", Expires: 100}, // already gone
	}
	for _, now := range []int64{99, 100, 149, 150, 151, 200, 201} {
		want := 0
		for _, id := range []string{"a", "b"} {
			want += len(pool.Live(ls, id, now))
		}
		if got := liveCount(ls, now); got != want {
			t.Fatalf("at now=%d the aggregate says %d live but the resources say %d", now, got, want)
		}
	}
}
