package awsdiscover

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Tests for DiscoverArgs.OnTypeDiscovered — the per-type RESULTS callback
// (reliable#2060). The callback is the streaming counterpart to the
// count-only TypeProgressEmitter.TypeDone: it delivers each type's
// discovered resources AS THAT TYPE COMPLETES, so a consumer can stream
// per-type batches without fanning out one single-type DiscoverTypes call
// per type (the reliable#2065 workaround this hook retires).
//
// Contract under test (see DiscoverArgs.OnTypeDiscovered doc):
//   - invoked exactly once per requested type, including empty/downgraded
//     types (empty non-nil slice);
//   - invocations serialized (no callback-side locking required, -race clean);
//   - completion-ordered (a fast type fires while a slow type is in flight);
//   - not invoked for the failing type or types that never started on a
//     fail-fast abort, and never invoked more than once.

// recordingCB is a test sink for OnTypeDiscovered that deliberately mutates
// shared state WITHOUT its own lock. Under -race this proves the aggregator
// serializes invocations: if it didn't, the concurrent map write + counter
// increment would trip the race detector (and the counts would tear).
type recordingCB struct {
	byType map[string][]string // tfType -> resource addresses, unguarded on purpose
	order  []string            // completion order of tfTypes, unguarded on purpose
	calls  int                 // total invocations, unguarded on purpose
}

func newRecordingCB() *recordingCB {
	return &recordingCB{byType: map[string][]string{}}
}

func (r *recordingCB) fn(tfType string, resources []imported.ImportedResource) {
	addrs := make([]string, 0, len(resources))
	for _, res := range resources {
		addrs = append(addrs, res.Identity.Address)
	}
	r.byType[tfType] = addrs
	r.order = append(r.order, tfType)
	r.calls++
}

// TestOnTypeDiscovered_DeliversEachTypeOnce asserts the headline contract:
// every requested type fires the callback exactly once with that type's
// resources, and the union of delivered resources equals the flattened
// return value. Running with -race additionally proves the invocations are
// serialized (the callback mutates an unguarded map/slice/counter).
func TestOnTypeDiscovered_DeliversEachTypeOnce(t *testing.T) {
	t.Parallel()

	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1"), ir("a2")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	c := &fakeDiscoverer{t: "type_c", out: nil} // zero-result type still fires once
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b, "type_c": c}}

	cb := newRecordingCB()
	args := argsBasic()
	args.OnTypeDiscovered = cb.fn

	got, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b", "type_c"}, args)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly one callback per requested type.
	if cb.calls != 3 {
		t.Errorf("callback fired %d times, want 3 (once per requested type)", cb.calls)
	}
	for _, want := range []string{"type_a", "type_b", "type_c"} {
		if _, ok := cb.byType[want]; !ok {
			t.Errorf("type %q never delivered to callback", want)
		}
	}
	// Per-type resources match.
	if !reflect.DeepEqual(cb.byType["type_a"], []string{"a1", "a2"}) {
		t.Errorf("type_a resources = %v, want [a1 a2]", cb.byType["type_a"])
	}
	if !reflect.DeepEqual(cb.byType["type_b"], []string{"b1"}) {
		t.Errorf("type_b resources = %v, want [b1]", cb.byType["type_b"])
	}
	// Zero-result type fires with an empty (non-nil) slice.
	if got, want := cb.byType["type_c"], []string{}; !reflect.DeepEqual(got, want) {
		t.Errorf("type_c resources = %v, want [] (zero-result type still delivered)", got)
	}

	// The flattened return value still groups by selection order, unchanged.
	gotAddrs := make([]string, len(got))
	for i, r := range got {
		gotAddrs[i] = r.Identity.Address
	}
	if want := []string{"a1", "a2", "b1"}; !reflect.DeepEqual(gotAddrs, want) {
		t.Errorf("returned slice = %v, want %v (selection order preserved)", gotAddrs, want)
	}
}

// TestOnTypeDiscovered_DeliversEmptyOnDowngrade asserts a type DOWNGRADED by
// the PerTypeTimeout fence still fires the callback exactly once, with an
// EMPTY non-nil slice — so a consumer driving a progress denominator off the
// callbacks advances to 100% even when a slow type yields nothing.
func TestOnTypeDiscovered_DeliversEmptyOnDowngrade(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	slow := &sleepDiscoverer{t: "type_slow", out: []imported.ImportedResource{ir("slow1")}, sleep: 500 * time.Millisecond}
	fast := &fakeDiscoverer{t: "type_fast", out: []imported.ImportedResource{ir("fast1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_slow": slow, "type_fast": fast}}

	cb := newRecordingCB()
	args := argsBasic()
	args.PerTypeTimeout = 50 * time.Millisecond
	args.OnTypeDiscovered = cb.fn

	got, err := agg.DiscoverTypes(context.Background(), []string{"type_slow", "type_fast"}, args)
	if err != nil {
		t.Fatalf("DiscoverTypes returned error on per-type timeout (partial result expected): %v", err)
	}

	// Both types fired exactly once.
	if cb.calls != 2 {
		t.Errorf("callback fired %d times, want 2", cb.calls)
	}
	// The fast type delivered its row.
	if !reflect.DeepEqual(cb.byType["type_fast"], []string{"fast1"}) {
		t.Errorf("type_fast resources = %v, want [fast1]", cb.byType["type_fast"])
	}
	// The downgraded type delivered an empty non-nil slice.
	slowRes, ok := cb.byType["type_slow"]
	if !ok {
		t.Fatal("downgraded type_slow never delivered to callback")
	}
	if slowRes == nil || len(slowRes) != 0 {
		t.Errorf("downgraded type_slow resources = %v, want [] (empty non-nil)", slowRes)
	}
	// And the returned slice only carries the fast type's row.
	if len(got) != 1 || got[0].Identity.Address != "fast1" {
		t.Errorf("returned slice = %v, want [fast1]", got)
	}
}

// barrierDiscoverer blocks Discover until release is closed (the slow type)
// or returns immediately after signalling started (the fast type). Used to
// pin the streaming-order guarantee.
type barrierDiscoverer struct {
	t       string
	out     []imported.ImportedResource
	block   bool
	started chan struct{} // closed when Discover is entered (slow type)
	release chan struct{} // Discover returns once this is closed (slow type)
}

func (b *barrierDiscoverer) ResourceType() string { return b.t }

func (b *barrierDiscoverer) Discover(ctx context.Context, _ DiscoverArgs) ([]imported.ImportedResource, error) {
	if b.block {
		if b.started != nil {
			close(b.started)
		}
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return b.out, nil
}

func (b *barrierDiscoverer) DiscoverByID(_ context.Context, _, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, ErrNotSupported
}

// TestOnTypeDiscovered_StreamsBeforeScanCompletes is the streaming-order
// guard: a fast type's callback MUST fire while a slow type is still in
// flight. A regression that gathered all per-type results and only fired the
// callbacks after g.Wait() (defeating streaming) would deadlock here — the
// fast callback would never run, so the slow type would never be released,
// so the bounded escape hatch fires a clean failure instead of hanging CI.
//
// Mirrors reliable's TestRunAWSDiscoverWithPerTypeTimeout_StreamsBatchesBeforeScanCompletes.
func TestOnTypeDiscovered_StreamsBeforeScanCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	slow := &barrierDiscoverer{
		t:       "type_slow",
		out:     []imported.ImportedResource{ir("slow1")},
		block:   true,
		started: slowStarted,
		release: slowRelease,
	}
	fast := &barrierDiscoverer{t: "type_fast", out: []imported.ImportedResource{ir("fast1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_slow": slow, "type_fast": fast}}

	fastCallbackFired := make(chan struct{})
	var once sync.Once
	args := argsBasic()
	args.OnTypeDiscovered = func(tfType string, _ []imported.ImportedResource) {
		if tfType == "type_fast" {
			once.Do(func() { close(fastCallbackFired) })
		}
	}

	done := make(chan struct{})
	var got []imported.ImportedResource
	var gotErr error
	go func() {
		got, gotErr = agg.DiscoverTypes(context.Background(), []string{"type_slow", "type_fast"}, args)
		close(done)
	}()

	// Wait for the slow type to be IN FLIGHT, then assert the fast type's
	// callback fires before we release the slow type. If the implementation
	// gathered-then-emitted, the fast callback would not fire until after
	// g.Wait(), which can't happen until we release slow — deadlock, caught
	// by the escape-hatch timeout.
	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow type never entered Discover")
	}
	select {
	case <-fastCallbackFired:
		// Good: fast type streamed its callback while slow was still blocked.
	case <-time.After(2 * time.Second):
		close(slowRelease) // unblock so the goroutine can exit cleanly
		t.Fatal("fast type's OnTypeDiscovered did not fire while the slow type was still in flight — results are not streaming (gather-then-emit regression)")
	}

	// Release the slow type and let the call finish.
	close(slowRelease)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DiscoverTypes did not return after releasing the slow type")
	}
	if gotErr != nil {
		t.Fatalf("DiscoverTypes returned error: %v", gotErr)
	}
	if len(got) != 2 {
		t.Errorf("len(got)=%d, want 2", len(got))
	}
}

// TestOnTypeDiscovered_FailFastNoDuplicateOrPhantom asserts the failure
// semantics: on a fail-fast abort (a non-timeout per-type error cancels the
// errgroup), the callback is never invoked for the failing type, never
// invoked more than once for any type, and never invoked for a type that
// never started. A type that COMPLETED before the failure MAY have fired —
// that is acceptable streaming behavior — so we assert the bounds, not an
// exact set. The aggregator must still return the error.
func TestOnTypeDiscovered_FailFastNoDuplicateOrPhantom(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()

	// type_boom fails immediately; type_slow would sleep but should observe
	// ctx.Done() (fail-fast) and bail without delivering. With concurrency 4
	// both kick off together.
	boom := &sleepDiscoverer{t: "type_boom", sleep: 0, err: errors.New("boom")}
	slow := &sleepDiscoverer{t: "type_slow", out: []imported.ImportedResource{ir("slow1")}, sleep: 500 * time.Millisecond}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_boom": boom, "type_slow": slow}}

	var mu sync.Mutex
	delivered := map[string]int{}
	args := argsBasic()
	args.OnTypeDiscovered = func(tfType string, _ []imported.ImportedResource) {
		mu.Lock()
		delivered[tfType]++
		mu.Unlock()
	}

	_, err := agg.DiscoverTypes(context.Background(), []string{"type_boom", "type_slow"}, args)
	if err == nil {
		t.Fatal("expected error from failing discoverer")
	}

	mu.Lock()
	defer mu.Unlock()
	// The failing type must never deliver.
	if n := delivered["type_boom"]; n != 0 {
		t.Errorf("failing type_boom delivered %d times, want 0", n)
	}
	// No type fires more than once.
	for tfType, n := range delivered {
		if n > 1 {
			t.Errorf("type %q delivered %d times, want <=1 (no duplicate delivery)", tfType, n)
		}
	}
	// No phantom types (anything outside the requested set).
	for tfType := range delivered {
		if tfType != "type_slow" {
			t.Errorf("unexpected type %q delivered (never-started / phantom)", tfType)
		}
	}
}

// TestOnTypeDiscovered_NilCallbackIsBackCompat pins the back-compat path: a
// nil OnTypeDiscovered runs byte-for-byte the no-callback behavior (the CLI
// and mars shape today). The returned slice is identical and no panic occurs.
func TestOnTypeDiscovered_NilCallbackIsBackCompat(t *testing.T) {
	t.Parallel()

	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	args := argsBasic() // OnTypeDiscovered is nil
	got, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b"}, args)
	if err != nil {
		t.Fatal(err)
	}
	gotAddrs := make([]string, len(got))
	for i, r := range got {
		gotAddrs[i] = r.Identity.Address
	}
	if want := []string{"a1", "b1"}; !reflect.DeepEqual(gotAddrs, want) {
		t.Errorf("returned slice = %v, want %v", gotAddrs, want)
	}
}

// TestOnTypeDiscovered_ConcurrentSerialized stresses the serialization
// guarantee under -race: many types complete concurrently and the callback
// mutates an unguarded counter. The aggregator's internal cbMu must serialize
// the invocations — without it, -race trips and/or the counter tears. We pin
// the exact call count to catch a torn increment, and run enough types to
// saturate the concurrency cap so invocations genuinely overlap in time.
func TestOnTypeDiscovered_ConcurrentSerialized(t *testing.T) {
	t.Parallel()

	const types = 12 // > DiscoverTypesConcurrency so the fan-out genuinely overlaps
	byType := make(map[string]Discoverer, types)
	names := make([]string, 0, types)
	for i := range types {
		name := "type_" + string(rune('a'+i))
		byType[name] = &fakeDiscoverer{t: name, out: []imported.ImportedResource{ir(name + "1")}}
		names = append(names, name)
	}
	agg := &AWSDiscoverer{byType: byType}

	// Unguarded counter on purpose: the aggregator serializes invocations,
	// so this increment is safe; -race proves it.
	var calls int
	seen := map[string]struct{}{}
	args := argsBasic()
	args.OnTypeDiscovered = func(tfType string, _ []imported.ImportedResource) {
		calls++
		seen[tfType] = struct{}{}
	}

	if _, err := agg.DiscoverTypes(context.Background(), names, args); err != nil {
		t.Fatal(err)
	}
	if calls != types {
		t.Errorf("callback fired %d times, want %d (torn counter ⇒ serialization broken)", calls, types)
	}
	gotNames := make([]string, 0, len(seen))
	for n := range seen {
		gotNames = append(gotNames, n)
	}
	slices.Sort(gotNames)
	slices.Sort(names)
	if !reflect.DeepEqual(gotNames, names) {
		t.Errorf("delivered types = %v, want %v", gotNames, names)
	}
}

// TestDiscoverTypesConcurrency_PinnedValue is the exported-constant pin: the
// type-level fan-out cap is 4 (#632), and it is EXPORTED so downstream
// consumers (reliable#2065) can bound their own fan-out by the same value
// instead of guessing. A regression that unexported it fails to compile this
// reference; a regression that changes the value fails the assertion.
func TestDiscoverTypesConcurrency_PinnedValue(t *testing.T) {
	t.Parallel()
	if DiscoverTypesConcurrency != 4 {
		t.Errorf("DiscoverTypesConcurrency = %d, want 4 (type-level fan-out cap, #632)", DiscoverTypesConcurrency)
	}
	// Sanity: it is distinct from the per-resource GetResource cap. The two
	// govern different layers (reliable#2065 codex round 2 conflated them).
	if DiscoverTypesConcurrency == DefaultMaxConcurrency {
		t.Errorf("DiscoverTypesConcurrency (%d) must not equal DefaultMaxConcurrency (%d) — they are different layers",
			DiscoverTypesConcurrency, DefaultMaxConcurrency)
	}
}
