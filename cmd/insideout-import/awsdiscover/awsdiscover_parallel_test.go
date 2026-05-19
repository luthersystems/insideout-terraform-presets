package awsdiscover

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

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
// sequentially. Concurrency was lowered from 8 → 4 in #632 to avoid
// CloudControl throttling; with two 50ms sleepers and limit≥2 the
// parallel wall time stays well under the sequential lower bound of
// 100ms. Jitter is left at the zero-value default for this bare
// &AWSDiscoverer{} (no NewAWSDiscoverer call), so it can't widen the
// wall-time window.
func TestDiscoverTypes_RunsServicesConcurrently(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	if defaultDiscoverTypesConcurrency < 2 {
		t.Fatalf("defaultDiscoverTypesConcurrency=%d; this test requires >=2 to assert parallel execution", defaultDiscoverTypesConcurrency)
	}

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

// TestDiscoverTypes_ConcurrencyCappedAtDefault pins
// defaultDiscoverTypesConcurrency (= 4 per #632, lowered from 8 in
// #629) by registering more services than the cap and asserting the
// observed-concurrent max never exceeds the limit. Catches an
// accidental g.SetLimit() drop or a future bump that re-introduces
// the CloudControl throttle observed in #632.
func TestDiscoverTypes_ConcurrencyCappedAtDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	// Pin the expected cap. If the constant changes intentionally,
	// update the literal here so the failure mode is obvious.
	const wantCap = 4
	if defaultDiscoverTypesConcurrency != wantCap {
		t.Fatalf("defaultDiscoverTypesConcurrency=%d, want %d — if you intentionally changed the cap, update this test and TestProductionLoadConfig retry pins to match (#632)", defaultDiscoverTypesConcurrency, wantCap)
	}

	const services = wantCap * 2 // 8: enough to expose any cap > wantCap
	var concurrent, maxObserved int32
	byType := make(map[string]Discoverer, services)
	types := make([]string, 0, services)
	for i := 0; i < services; i++ {
		name := "type_" + string(rune('a'+i))
		byType[name] = &sleepDiscoverer{
			t:           name,
			out:         []imported.ImportedResource{ir(name + "1")},
			sleep:       30 * time.Millisecond,
			concurrent:  &concurrent,
			maxObserved: &maxObserved,
		}
		types = append(types, name)
	}
	agg := &AWSDiscoverer{byType: byType}

	if _, err := agg.DiscoverTypes(context.Background(), types, argsBasic()); err != nil {
		t.Fatal(err)
	}
	got := atomic.LoadInt32(&maxObserved)
	if int(got) > wantCap {
		t.Errorf("maxObserved=%d, want <=%d (cap exceeded — g.SetLimit(defaultDiscoverTypesConcurrency) regressed)", got, wantCap)
	}
	// And it must have actually saturated the cap — otherwise the
	// test isn't asserting anything (e.g. accidental serialization
	// would also satisfy <=cap).
	if int(got) < wantCap {
		t.Errorf("maxObserved=%d, want ==%d (services should saturate the cap; if not, fan-out is broken)", got, wantCap)
	}
}

// TestDiscoverTypes_StartupJitterApplied asserts that DiscoverTypes
// stagger-starts its per-service goroutines via the jitterSleep seam
// (#632). Without jitter all N goroutines fire their first AWS calls
// at t=0, which is what triggered the CloudControl ThrottlingException
// observed in staging. We assert:
//
//   - jitterSleep is called once per goroutine
//   - the sampled durations span a non-trivial fraction of the jitter
//     window (i.e. they aren't all zero — Int63n(0) panics so the
//     production code guards on startupJitter > 0)
//   - every sampled duration is within [0, startupJitter)
func TestDiscoverTypes_StartupJitterApplied(t *testing.T) {
	t.Parallel()

	const services = 8
	const jitterWindow = 100 * time.Millisecond

	var mu sync.Mutex
	var slept []time.Duration
	recorder := func(d time.Duration) {
		mu.Lock()
		slept = append(slept, d)
		mu.Unlock()
	}

	byType := make(map[string]Discoverer, services)
	types := make([]string, 0, services)
	for i := 0; i < services; i++ {
		name := "type_" + string(rune('a'+i))
		byType[name] = &fakeDiscoverer{t: name, out: []imported.ImportedResource{ir(name + "1")}}
		types = append(types, name)
	}
	agg := &AWSDiscoverer{
		byType:        byType,
		startupJitter: jitterWindow,
		jitterSleep:   recorder,
	}

	if _, err := agg.DiscoverTypes(context.Background(), types, argsBasic()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	got := append([]time.Duration(nil), slept...)
	mu.Unlock()

	// Production samples a non-zero delay per goroutine; the recorder is
	// only invoked when delay > 0. Across 8 goroutines drawing from a
	// 100ms window, the probability of all 8 sampling exactly 0 is
	// effectively zero (sampling resolution is nanoseconds), so we
	// expect ~services entries. Allow for the rare zero by requiring at
	// least services/2 + 1 recorded sleeps — comfortably above noise.
	if len(got) < services/2+1 {
		t.Errorf("jitterSleep called %d times across %d goroutines, want >=%d — jitter likely not applied (all delays 0?)", len(got), services, services/2+1)
	}
	// Every recorded duration must lie in [0, jitterWindow). Anything
	// outside means rand.Int63n was called with a different bound or
	// the duration arithmetic regressed.
	for _, d := range got {
		if d < 0 || d >= jitterWindow {
			t.Errorf("recorded jitter delay=%v out of [0, %v)", d, jitterWindow)
		}
	}
}

// TestDiscoverTypes_StartupJitterDisabledWhenZero is the inverse
// guard: setting startupJitter=0 must skip the rand.Int63n call (it
// panics on 0) AND must not call jitterSleep. This is the shape every
// existing test in the suite gets by default — they construct a bare
// &AWSDiscoverer{} with the zero-value startupJitter, and the previous
// (#629) parallel wall-time bound depends on no jitter being inserted.
func TestDiscoverTypes_StartupJitterDisabledWhenZero(t *testing.T) {
	t.Parallel()

	var sleeps int32
	recorder := func(time.Duration) { atomic.AddInt32(&sleeps, 1) }

	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	agg := &AWSDiscoverer{
		byType:        map[string]Discoverer{"type_a": a, "type_b": b},
		startupJitter: 0,
		jitterSleep:   recorder,
	}

	if _, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b"}, argsBasic()); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&sleeps); got != 0 {
		t.Errorf("jitterSleep called %d times with startupJitter=0, want 0", got)
	}
}

// TestNewAWSDiscoverer_DefaultsJitterFields pins that the production
// constructor wires both jitter fields. Without this, a future refactor
// of NewAWSDiscovererWithConcurrency that drops the startupJitter /
// jitterSleep assignments would silently disable the throttle
// mitigation — every production call would skip the jitter sample
// because startupJitter == 0.
func TestNewAWSDiscoverer_DefaultsJitterFields(t *testing.T) {
	t.Parallel()

	d := NewAWSDiscovererWithConcurrency(aws.Config{}, 1)
	if d.startupJitter != defaultDiscoverStartupJitterMax {
		t.Errorf("startupJitter=%v, want %v", d.startupJitter, defaultDiscoverStartupJitterMax)
	}
	if d.jitterSleep == nil {
		t.Errorf("jitterSleep is nil; constructor must wire time.Sleep")
	}
}
