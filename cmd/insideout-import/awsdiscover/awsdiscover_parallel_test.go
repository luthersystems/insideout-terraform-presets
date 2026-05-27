package awsdiscover

import (
	"context"
	"errors"
	"reflect"
	"slices"
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
	for i := range services {
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
	// This test depends on the bare &AWSDiscoverer{} zero-value
	// startupJitter so no [0, 500ms) sleep widens the parallel-saturation
	// window. If a future refactor moves jitter to a struct-default, the
	// 30ms sleep budget here is no longer enough to guarantee saturation.
	if agg.startupJitter != 0 {
		t.Fatalf("agg.startupJitter=%v, want 0 (test depends on zero-value default)", agg.startupJitter)
	}

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

// TestDiscoverTypes_StartupJitterApplied asserts DiscoverTypes
// stagger-starts its per-service goroutines via the jitterSample +
// jitterSleep seams (#632). Without jitter all N goroutines fire
// their first AWS calls at t=0, which is what triggered the
// CloudControl ThrottlingException observed in staging.
//
// We inject a deterministic sample sequence so the count and value
// assertions are exact (no statistical lower bound, no dependency on
// math/rand's seeding policy). The sequence includes one zero entry
// to pin the production code's `if delay > 0 { jitterSleep(...) }`
// guard, one near-zero, one near-window-max, and a mid-range — so a
// mutation that shrinks the window or skips the sleep is loud.
func TestDiscoverTypes_StartupJitterApplied(t *testing.T) {
	t.Parallel()

	const jitterWindow = 100 * time.Millisecond
	// One sample per service. Order matches the alphabetic byType
	// iteration order DiscoverTypes uses via its `selected` slice
	// (DiscoverTypes sorts internally), so the recorder sees the
	// non-zero entries in this order regardless of goroutine
	// scheduling.
	sequence := []time.Duration{
		0,                              // pins the `if delay > 0` guard — must NOT be recorded
		1 * time.Microsecond,           // near-zero, MUST be recorded
		50 * time.Millisecond,          // mid-range, MUST be recorded
		99 * time.Millisecond,          // near-window-max — catches a shrunk-window mutation
		25 * time.Millisecond,          // mid-range
		75 * time.Millisecond,          // mid-range
		10 * time.Millisecond,          // near-zero+, MUST be recorded
		jitterWindow - time.Nanosecond, // [0, window) boundary
	}
	services := len(sequence)
	wantSlept := make([]time.Duration, 0, services)
	for _, d := range sequence {
		if d > 0 {
			wantSlept = append(wantSlept, d)
		}
	}

	var sampleIdx int32
	sampler := func() time.Duration {
		i := atomic.AddInt32(&sampleIdx, 1) - 1
		return sequence[i]
	}

	var mu sync.Mutex
	var slept []time.Duration
	recorder := func(d time.Duration) {
		mu.Lock()
		slept = append(slept, d)
		mu.Unlock()
	}

	byType := make(map[string]Discoverer, services)
	types := make([]string, 0, services)
	for i := range services {
		name := "type_" + string(rune('a'+i))
		byType[name] = &fakeDiscoverer{t: name, out: []imported.ImportedResource{ir(name + "1")}}
		types = append(types, name)
	}
	agg := &AWSDiscoverer{
		byType:        byType,
		startupJitter: jitterWindow,
		jitterSample:  sampler,
		jitterSleep:   recorder,
	}

	if _, err := agg.DiscoverTypes(context.Background(), types, argsBasic()); err != nil {
		t.Fatal(err)
	}

	// Sampler called exactly once per service (in the parent loop
	// before each g.Go) — pin the count to catch a mutation that
	// drops the sample or moves it inside a conditional.
	if got := atomic.LoadInt32(&sampleIdx); int(got) != services {
		t.Errorf("jitterSample called %d times, want %d (once per service)", got, services)
	}

	mu.Lock()
	got := append([]time.Duration(nil), slept...)
	mu.Unlock()

	// Recorder receives exactly the non-zero samples — proves both
	// (a) the `delay > 0` guard skips the zero entry (catches a
	// mutation that always sleeps, slowing the broad scan by
	// median-of-window per goroutine) and (b) every non-zero sample
	// is faithfully forwarded (catches a mutation that drops the
	// sleep entirely, re-introducing the aligned t=0 burst).
	if len(got) != len(wantSlept) {
		t.Fatalf("jitterSleep called %d times, want %d (one per non-zero sample)", len(got), len(wantSlept))
	}
	// Order isn't goroutine-deterministic, so compare as multisets.
	slices.Sort(got)
	slices.Sort(wantSlept)
	for i, d := range got {
		if d != wantSlept[i] {
			t.Errorf("jitterSleep[%d]=%v, want %v (slept=%v, wantSlept=%v)", i, d, wantSlept[i], got, wantSlept)
		}
	}
}

// TestDiscoverTypes_StartupJitterDisabledWhenZero pins two coupled
// invariants for the startupJitter=0 path — the shape every existing
// test in the suite gets by default (bare &AWSDiscoverer{} with the
// zero-value startupJitter, on which the #629 parallel wall-time
// bound depends):
//
//  1. defaultJitterSample MUST return 0 (not call rand.Int63n with
//     a zero/negative bound — that panics).
//  2. With the default sampler returning 0, jitterSleep is never
//     called.
//
// The recover() block proves the panic-guard is real; the recorder
// count proves the `if delay > 0` guard skips the sleep. Both
// together catch a mutation that drops either guard.
func TestDiscoverTypes_StartupJitterDisabledWhenZero(t *testing.T) {
	t.Parallel()

	// Invariant 1: the default sampler must not panic when
	// startupJitter is zero. A naive `rand.Int63n(0)` would.
	agg := &AWSDiscoverer{startupJitter: 0}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("defaultJitterSample panicked at startupJitter=0: %v — production code must guard rand.Int63n against n<=0", r)
			}
		}()
		if d := agg.defaultJitterSample(); d != 0 {
			t.Errorf("defaultJitterSample()=%v at startupJitter=0, want 0", d)
		}
	}()

	// Invariant 2: with the default sampler returning 0, no
	// jitterSleep call is issued.
	var sleeps int32
	recorder := func(time.Duration) { atomic.AddInt32(&sleeps, 1) }

	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	agg2 := &AWSDiscoverer{
		byType:        map[string]Discoverer{"type_a": a, "type_b": b},
		startupJitter: 0,
		jitterSleep:   recorder,
	}

	if _, err := agg2.DiscoverTypes(context.Background(), []string{"type_a", "type_b"}, argsBasic()); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&sleeps); got != 0 {
		t.Errorf("jitterSleep called %d times with startupJitter=0, want 0", got)
	}
}

// TestNewAWSDiscoverer_DefaultsJitterFields pins that the production
// constructor wires all three jitter fields to their throttle-
// mitigation defaults. Without this, a future refactor of
// NewAWSDiscovererWithConcurrency that drops the startupJitter /
// jitterSleep / jitterSample assignments would silently disable the
// mitigation — every production call would either skip the jitter
// sample (startupJitter == 0) or skip the sleep (jitterSleep == nil
// is recovered to time.Sleep, but a non-nil no-op wiring would not be).
//
// The function-identity assertion via reflect.ValueOf().Pointer()
// catches a mutation that wires a no-op (e.g. jitterSleep = func(time.Duration){})
// — a plain nil-check would happily accept that and silently disable
// the production jitter.
func TestNewAWSDiscoverer_DefaultsJitterFields(t *testing.T) {
	t.Parallel()

	d := NewAWSDiscovererWithConcurrency(aws.Config{}, 1)
	if d.startupJitter != defaultDiscoverStartupJitterMax {
		t.Errorf("startupJitter=%v, want %v", d.startupJitter, defaultDiscoverStartupJitterMax)
	}
	if d.jitterSleep == nil {
		t.Fatalf("jitterSleep is nil; constructor must wire time.Sleep")
	}
	// Function-identity check: confirm the constructor wired the
	// real time.Sleep, not a no-op stub that would silently disable
	// the per-goroutine startup delay. reflect-comparing the code
	// pointer is the standard idiom for this (Go funcs aren't ==
	// comparable in source).
	wantSleep := reflect.ValueOf(time.Sleep).Pointer()
	gotSleep := reflect.ValueOf(d.jitterSleep).Pointer()
	if gotSleep != wantSleep {
		t.Errorf("jitterSleep is not time.Sleep (got pointer=%x, want %x) — a no-op stub would silently disable the #632 mitigation", gotSleep, wantSleep)
	}
	if d.jitterSample == nil {
		t.Fatalf("jitterSample is nil; constructor must wire defaultJitterSample")
	}
	wantSample := reflect.ValueOf(d.defaultJitterSample).Pointer()
	gotSample := reflect.ValueOf(d.jitterSample).Pointer()
	if gotSample != wantSample {
		t.Errorf("jitterSample is not d.defaultJitterSample (got pointer=%x, want %x)", gotSample, wantSample)
	}
}

// TestDiscoverTypes_PerTypeTimeoutPartialResult pins the #1787 fix —
// one stalled per-type Discover MUST NOT hold up its siblings or fail
// the whole call. With args.PerTypeTimeout set, the aggregator wraps
// each Discover in a child context-with-timeout; on expiry it emits an
// empty slice for that type and lets siblings finish. The overall call
// returns a partial result (the fast type's resources) without error.
//
// Why this matters: before #1787 a single slow S3 ListBuckets / EC2
// DescribeInstances against a hot AWS quota could pin the broad scan
// at the caller's outer Vercel deadline (300s), then fail the whole
// gather. A best-effort survey should return what it has.
func TestDiscoverTypes_PerTypeTimeoutPartialResult(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	// Slow type sleeps well past the per-type budget; fast type
	// returns immediately. With PerTypeTimeout=50ms the slow type must
	// be timed out (and emit nothing) while the fast type's "fast1"
	// row survives in the returned slice.
	slow := &sleepDiscoverer{
		t:     "type_slow",
		out:   []imported.ImportedResource{ir("slow1")},
		sleep: 500 * time.Millisecond,
	}
	fast := &fakeDiscoverer{
		t:   "type_fast",
		out: []imported.ImportedResource{ir("fast1")},
	}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{
		"type_slow": slow,
		"type_fast": fast,
	}}

	args := argsBasic()
	args.PerTypeTimeout = 50 * time.Millisecond

	start := time.Now()
	got, err := agg.DiscoverTypes(context.Background(), []string{"type_slow", "type_fast"}, args)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("DiscoverTypes returned error on per-type timeout (partial result expected): %v", err)
	}
	// Wall time must be bounded by the timeout + a little overhead,
	// NOT the slow sleeper's 500ms. The whole point of the per-type
	// fence is to avoid waiting on the slow one.
	if elapsed >= 400*time.Millisecond {
		t.Errorf("DiscoverTypes elapsed=%v with PerTypeTimeout=50ms, want <400ms — the slow type was not fenced off", elapsed)
	}
	addrs := make([]string, 0, len(got))
	for _, r := range got {
		addrs = append(addrs, r.Identity.Address)
	}
	want := []string{"fast1"}
	if !reflect.DeepEqual(addrs, want) {
		t.Errorf("partial result = %v, want %v (slow type should emit nothing, fast type's row should survive)", addrs, want)
	}
}

// TestDiscoverTypes_PerTypeTimeoutZeroDisablesFence pins the
// back-compat default: PerTypeTimeout==0 means "no per-type bound".
// A bare DiscoverArgs (the shape every existing test in the suite
// uses) must behave exactly as it did pre-#1787: a slow per-type call
// runs to completion and its results are included.
func TestDiscoverTypes_PerTypeTimeoutZeroDisablesFence(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	slow := &sleepDiscoverer{
		t:     "type_slow",
		out:   []imported.ImportedResource{ir("slow1")},
		sleep: 60 * time.Millisecond,
	}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_slow": slow}}

	// argsBasic leaves PerTypeTimeout at the zero value.
	got, err := agg.DiscoverTypes(context.Background(), []string{"type_slow"}, argsBasic())
	if err != nil {
		t.Fatalf("DiscoverTypes returned error with no per-type fence: %v", err)
	}
	if len(got) != 1 || got[0].Identity.Address != "slow1" {
		t.Errorf("got=%v, want [slow1] — PerTypeTimeout==0 must NOT fence off the slow type", got)
	}
}

// TestDiscoverTypes_PerTypeTimeoutParentCancelPropagates pins that the
// timeout downgrade is scoped to per-type budget expiry. A parent
// context cancellation (caller's outer deadline, fail-fast sibling)
// must still propagate as an error — operators rely on the fail-fast
// shape to surface real failures, and silently swallowing parent
// cancellation would let the broad scan return success for a request
// the caller already abandoned.
func TestDiscoverTypes_PerTypeTimeoutParentCancelPropagates(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	slow := &sleepDiscoverer{
		t:     "type_slow",
		out:   []imported.ImportedResource{ir("slow1")},
		sleep: 500 * time.Millisecond,
	}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_slow": slow}}

	args := argsBasic()
	// Per-type budget is generous; the parent ctx cancels first.
	args.PerTypeTimeout = 1 * time.Second

	parentCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := agg.DiscoverTypes(parentCtx, []string{"type_slow"}, args)
	if err == nil {
		t.Fatal("expected error from parent-ctx cancellation; got nil — the per-type timeout downgrade must NOT swallow parent cancellation")
	}
}
