// Tests for runSubsBounded — the per-cloud batch fan-out helper. Lock
// the four load-bearing properties of the helper:
//
//  1. Concurrency cap (peak in-flight goroutines ≤ limit)
//  2. Per-sub timeout (a blocked sub doesn't pin the whole batch)
//  3. Result order preservation (index-addressed write, no mutex needed)
//  4. Panic recovery (one sub's panic doesn't bring down siblings)
//
// Lifted from reliable/internal/agentapi/inspect_batch_common_test.go;
// converted from testify to plain testing.

package inspect

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSubsBounded_ConcurrencyCap(t *testing.T) {
	t.Parallel()
	const (
		concurrency = 4
		nSubs       = 16
		workDur     = 40 * time.Millisecond
	)
	subs := make([]SubRequest, nSubs)
	for i := range subs {
		subs[i] = SubRequest{Service: "ec2", Action: "describe-instances"}
	}

	var inflight atomic.Int32
	var peak atomic.Int32

	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		cur := inflight.Add(1)
		// Update peak atomically: keep swapping while cur > peak.
		for {
			p := peak.Load()
			if cur <= p {
				break
			}
			if peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(workDur)
		inflight.Add(-1)
		return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: true}
	}

	results := runSubsBounded(context.Background(), subs, concurrency, time.Second, fn)

	got := peak.Load()
	if got > concurrency {
		t.Fatalf("peak in-flight goroutines = %d, want ≤ %d", got, concurrency)
	}
	// Lower bound: with 16 subs × 40ms work and concurrency=4, the
	// pool must saturate to exactly 4 concurrent subs for multiple
	// batches. Without this, a mutation like `g.SetLimit(1)` (serial)
	// passes the upper bound silently.
	if got < concurrency {
		t.Fatalf("peak in-flight goroutines = %d, want ≥ %d — pool never saturated, "+
			"concurrency may have been downgraded (SetLimit=1? serial execution?)", got, concurrency)
	}
	if len(results) != nSubs {
		t.Fatalf("got %d results, want %d", len(results), nSubs)
	}
	for i, r := range results {
		if !r.OK || r.Index != i {
			t.Fatalf("result[%d] not OK or out of order: %+v", i, r)
		}
	}
}

func TestRunSubsBounded_PerSubTimeout(t *testing.T) {
	t.Parallel()
	const perSub = 30 * time.Millisecond

	subs := []SubRequest{
		{Service: "ec2", Action: "describe-instances"},    // blocks past timeout
		{Service: "rds", Action: "describe-db-instances"}, // quick
	}

	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		if idx == 0 {
			// Block until context is canceled.
			select {
			case <-ctx.Done():
				return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: false, Error: ctx.Err().Error()}
			case <-time.After(10 * time.Second):
				return SubResult{Index: idx, OK: true}
			}
		}
		return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: true}
	}

	start := time.Now()
	results := runSubsBounded(context.Background(), subs, 2, perSub, fn)
	elapsed := time.Since(start)

	if elapsed > 5*perSub {
		t.Fatalf("batch took %v, expected ≤ %v", elapsed, 5*perSub)
	}
	if results[0].OK {
		t.Fatalf("sub 0 should have timed out, got OK=true")
	}
	// Explicit "timeout" prefix — set by runOneSub when ctx.Err() is
	// DeadlineExceeded.
	if !strings.HasPrefix(results[0].Error, "timeout") {
		t.Fatalf("sub 0 error should start with 'timeout' prefix (set on DeadlineExceeded), got %q", results[0].Error)
	}
	if results[0].DurationMS < int64(perSub/time.Millisecond)-10 {
		t.Fatalf("sub 0 returned too early: DurationMS=%d, expected ≈ %d ms", results[0].DurationMS, perSub/time.Millisecond)
	}
	if results[0].DurationMS > 10*int64(perSub/time.Millisecond) {
		t.Fatalf("sub 0 ran too long: DurationMS=%d, expected ≤ %d ms (per-sub timeout regressed?)",
			results[0].DurationMS, 10*int64(perSub/time.Millisecond))
	}
	if !results[1].OK {
		t.Fatalf("sub 1 should have succeeded, got %+v", results[1])
	}
}

func TestRunSubsBounded_PreservesOrder(t *testing.T) {
	t.Parallel()
	// 8 subs with decreasing sleeps — later indices finish first. If
	// results were appended in completion order, indices would scramble.
	subs := make([]SubRequest, 8)
	for i := range subs {
		subs[i] = SubRequest{Service: "svc", Action: "act"}
	}
	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		sleep := time.Duration(len(subs)-idx) * 5 * time.Millisecond
		time.Sleep(sleep)
		return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: true}
	}
	results := runSubsBounded(context.Background(), subs, 8, time.Second, fn)
	for i, r := range results {
		if r.Index != i {
			t.Fatalf("result[%d].Index = %d, want %d (order scrambled)", i, r.Index, i)
		}
	}
}

func TestRunSubsBounded_PanicRecovery(t *testing.T) {
	t.Parallel()
	subs := []SubRequest{
		{Service: "ec2", Action: "a"},
		{Service: "rds", Action: "b"}, // panics
		{Service: "vpc", Action: "c"},
	}
	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		if idx == 1 {
			panic("boom")
		}
		return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: true}
	}
	results := runSubsBounded(context.Background(), subs, 3, time.Second, fn)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[1].OK {
		t.Fatalf("panicking sub should not be OK")
	}
	if !strings.Contains(results[1].Error, "panic") {
		t.Fatalf("panicking sub error should mention panic, got %q", results[1].Error)
	}
	if results[1].Service != "rds" || results[1].Action != "b" {
		t.Fatalf("panicking sub lost service/action: %+v", results[1])
	}
	if !results[0].OK || !results[2].OK {
		t.Fatalf("sibling subs should have succeeded: %+v %+v", results[0], results[2])
	}
}

func TestRunSubsBounded_EmptyInput(t *testing.T) {
	t.Parallel()
	results := runSubsBounded(context.Background(), nil, 8, time.Second, func(ctx context.Context, idx int, sub SubRequest) SubResult {
		t.Fatal("fn should not be called for empty input")
		return SubResult{}
	})
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestRunSubsBounded_ZeroConfigFallback(t *testing.T) {
	t.Parallel()
	// Zero concurrency/timeout should fall back to defaults rather
	// than deadlocking or running a zero-duration timeout that fails
	// every sub.
	subs := []SubRequest{{Service: "ec2", Action: "a"}}
	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: true}
	}
	results := runSubsBounded(context.Background(), subs, 0, 0, fn)
	if !results[0].OK {
		t.Fatalf("expected OK with zero config (fallback to defaults), got %+v", results[0])
	}
}

// TestRunSubsBounded_DurationMSAlwaysSet verifies that DurationMS is
// always populated, even on the panic path. A regression that swapped
// the defer order would silently zero out DurationMS for panicking
// subs, hiding latency outliers in production telemetry.
func TestRunSubsBounded_DurationMSAlwaysSet(t *testing.T) {
	t.Parallel()
	subs := []SubRequest{
		{Service: "a", Action: "ok"},
		{Service: "b", Action: "panic"},
	}
	fn := func(ctx context.Context, idx int, sub SubRequest) SubResult {
		// Sleep so DurationMS > 0 — a defer-order bug would still
		// emit Duration=0 here.
		time.Sleep(5 * time.Millisecond)
		if idx == 1 {
			panic("boom")
		}
		return SubResult{Index: idx, Service: sub.Service, Action: sub.Action, OK: true}
	}
	results := runSubsBounded(context.Background(), subs, 2, time.Second, fn)
	for i, r := range results {
		if r.DurationMS <= 0 {
			t.Errorf("result[%d] DurationMS = %d, want > 0", i, r.DurationMS)
		}
	}
}
