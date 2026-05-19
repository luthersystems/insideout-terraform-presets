package awsdiscover

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// sleepDiscoverer is a Discoverer whose Discover blocks for sleep before
// returning out. Used by the concurrency tests to assert that
// DiscoverTypes runs services in parallel: two sleepers of D each should
// finish in ~D total, not 2*D.
type sleepDiscoverer struct {
	t           string
	out         []imported.ImportedResource
	err         error
	sleep       time.Duration
	concurrent  *int32 // running count; max ever observed via atomic CAS
	maxObserved *int32 // monotonic max of concurrent observations
}

func (s *sleepDiscoverer) ResourceType() string { return s.t }

func (s *sleepDiscoverer) Discover(ctx context.Context, _ DiscoverArgs) ([]imported.ImportedResource, error) {
	if s.concurrent != nil {
		n := atomic.AddInt32(s.concurrent, 1)
		defer atomic.AddInt32(s.concurrent, -1)
		// Track high-water mark via CAS loop.
		for {
			cur := atomic.LoadInt32(s.maxObserved)
			if n <= cur || atomic.CompareAndSwapInt32(s.maxObserved, cur, n) {
				break
			}
		}
	}
	select {
	case <-time.After(s.sleep):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

func (s *sleepDiscoverer) DiscoverByID(_ context.Context, _, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, ErrNotSupported
}

// TestDiscoverTypes_RunsServicesConcurrently asserts that DiscoverTypes
// fans out across registered services rather than running them
// sequentially. Two 50ms sleepers should complete in well under the
// sequential lower bound of 100ms.
func TestDiscoverTypes_RunsServicesConcurrently(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	var concurrent, maxObserved int32
	const per = 50 * time.Millisecond
	a := &sleepDiscoverer{
		t:           "type_a",
		out:         []imported.ImportedResource{ir("a1")},
		sleep:       per,
		concurrent:  &concurrent,
		maxObserved: &maxObserved,
	}
	b := &sleepDiscoverer{
		t:           "type_b",
		out:         []imported.ImportedResource{ir("b1")},
		sleep:       per,
		concurrent:  &concurrent,
		maxObserved: &maxObserved,
	}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	start := time.Now()
	got, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b"}, argsBasic())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	// Generous bound: sequential would be >=100ms; parallel should be ~50ms.
	// 80ms gives plenty of headroom for slow CI without losing the signal.
	if elapsed >= 80*time.Millisecond {
		t.Errorf("DiscoverTypes elapsed=%v, want <80ms (parallel) — looks sequential", elapsed)
	}
	// At some point both goroutines must have been running simultaneously.
	if atomic.LoadInt32(&maxObserved) < 2 {
		t.Errorf("maxObserved=%d, want >=2 (services should run concurrently)", maxObserved)
	}
	if len(got) != 2 {
		t.Errorf("len(got)=%d, want 2", len(got))
	}
	// Selection order is preserved: results for type_a precede type_b.
	if got[0].Identity.Address != "a1" || got[1].Identity.Address != "b1" {
		t.Errorf("result order not preserved: got %s, %s; want a1, b1",
			got[0].Identity.Address, got[1].Identity.Address)
	}
}

// TestDiscoverTypes_PreservesSelectionOrderWithFan_Out — even with
// concurrent execution, the returned slice is in the order callers
// passed types. This matters because dep-chase and downstream stages
// rely on the deterministic per-type grouping in the manifest.
func TestDiscoverTypes_PreservesSelectionOrderWithFan_Out(t *testing.T) {
	t.Parallel()
	// Three services with varying out lengths. Selection order is
	// c, a, b — the returned slice should group [c-items..., a-items..., b-items...].
	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1"), ir("a2")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	c := &fakeDiscoverer{t: "type_c", out: []imported.ImportedResource{ir("c1"), ir("c2"), ir("c3")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b, "type_c": c}}

	got, err := agg.DiscoverTypes(context.Background(), []string{"type_c", "type_a", "type_b"}, argsBasic())
	if err != nil {
		t.Fatal(err)
	}
	gotAddrs := make([]string, len(got))
	for i, r := range got {
		gotAddrs[i] = r.Identity.Address
	}
	want := []string{"c1", "c2", "c3", "a1", "a2", "b1"}
	if !reflect.DeepEqual(gotAddrs, want) {
		t.Errorf("result order = %v, want %v (selection order: c, a, b)", gotAddrs, want)
	}
}

// TestDiscoverTypes_FailFastCancelsSiblings — when one per-service
// discoverer returns an error, the errgroup context cancels its
// siblings so they return promptly instead of running to completion.
func TestDiscoverTypes_FailFastCancelsSiblings(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()
	// type_a fails immediately; type_b would sleep 500ms but should
	// observe ctx.Done() and bail out fast.
	a := &sleepDiscoverer{t: "type_a", sleep: 0, err: errors.New("boom")}
	b := &sleepDiscoverer{t: "type_b", sleep: 500 * time.Millisecond}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	start := time.Now()
	_, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b"}, argsBasic())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from failing discoverer")
	}
	// Sequential or non-cancelling parallel would wait the full 500ms;
	// a properly-wired errgroup cancels the sibling well under that.
	if elapsed >= 250*time.Millisecond {
		t.Errorf("elapsed=%v, want <250ms — sibling should have been cancelled fast", elapsed)
	}
}

// TestDiscoverTypes_ErrorWrapShapeMatchesPreParallel pins the error
// shape so callers grepping for "<resource_type>: <cause>" keep working
// post-parallelization.
func TestDiscoverTypes_ErrorWrapShapeMatchesPreParallel(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", err: errors.New("Throttling")}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}
	_, err := agg.DiscoverTypes(context.Background(), nil, argsBasic())
	if err == nil {
		t.Fatal("expected error")
	}
	want := "type_a: Throttling"
	if err.Error() != want {
		t.Errorf("err=%q, want %q", err.Error(), want)
	}
}
