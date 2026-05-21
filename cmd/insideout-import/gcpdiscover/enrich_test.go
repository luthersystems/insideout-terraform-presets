package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// shrinkEnrichRetryDelays lowers the package-level retry backoff bounds
// to sub-millisecond values for the duration of a test and restores
// them on cleanup, so retry tests don't pay the production 200ms base /
// 8s ceiling. enrichRetryBaseDelay/enrichRetryMaxDelay are vars (not
// consts) precisely to allow this — see their doc comment in enrich.go.
func shrinkEnrichRetryDelays(t *testing.T) {
	t.Helper()
	origBase, origMax := enrichRetryBaseDelay, enrichRetryMaxDelay
	enrichRetryBaseDelay = 100 * time.Microsecond
	enrichRetryMaxDelay = 1 * time.Millisecond
	t.Cleanup(func() {
		enrichRetryBaseDelay, enrichRetryMaxDelay = origBase, origMax
	})
}

// fakeEnricher is a minimal AttributeEnricher that records its calls
// and returns a configurable result. Used to exercise EnrichAttributes
// dispatch / ordering / error-accumulation semantics without standing
// up real SDK clients.
//
// Since EnrichAttributes now fans the per-resource Enrich calls out
// across a bounded worker pool (#655), the calls slice is mutex-guarded
// so the recording is data-race-free under -race. The recorded order
// reflects goroutine completion order, NOT dispatch order — tests that
// need deterministic dispatch order assert on emitted progress events
// (which EnrichAttributes emits from its serial post-Wait pass).
type fakeEnricher struct {
	tfType string
	mu     sync.Mutex
	calls  []string                               // Identity.ImportID per call, completion order
	result func(*imported.ImportedResource) error // per-call result
}

func (f *fakeEnricher) ResourceType() string { return f.tfType }
func (f *fakeEnricher) Enrich(_ context.Context, ir *imported.ImportedResource, _ EnrichClients) error {
	f.mu.Lock()
	f.calls = append(f.calls, ir.Identity.ImportID)
	f.mu.Unlock()
	if f.result == nil {
		ir.Attrs = json.RawMessage(`{}`)
		return nil
	}
	return f.result(ir)
}

func TestEnrichAttributes_SkipsUnregisteredTypes(t *testing.T) {
	t.Parallel()
	g := &GCPDiscoverer{
		byTypeEnricher: map[string]AttributeEnricher{
			"google_storage_bucket": &fakeEnricher{tfType: "google_storage_bucket"},
		},
	}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_pubsub_topic", ImportID: "t1", Address: "google_pubsub_topic.t1"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b1", Address: "google_storage_bucket.b1"}},
	}
	require.NoError(t, g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil))

	// Only the storage_bucket got enriched.
	assert.Empty(t, irs[0].Attrs, "google_pubsub_topic has no enricher; Attrs must remain empty")
	assert.NotEmpty(t, irs[1].Attrs, "google_storage_bucket Attrs must be populated")

	// EnrichmentStatus is the typed signal added in #471. Types
	// without a registered enricher stay at the empty/unknown state;
	// the dispatched type lands on Full and has no EnrichErrors.
	assert.Equal(t, imported.EnrichmentStatusUnknown, irs[0].Identity.EnrichmentStatus,
		"unregistered type must keep EnrichmentStatus at Unknown")
	assert.Empty(t, irs[0].Identity.EnrichErrors)
	assert.Equal(t, imported.EnrichmentStatusFull, irs[1].Identity.EnrichmentStatus,
		"successfully enriched IR must be marked Full")
	assert.Empty(t, irs[1].Identity.EnrichErrors)
}

func TestEnrichAttributes_DeterministicOrder(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{tfType: "google_storage_bucket"}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}

	// Intentionally out-of-order Addresses; aggregator must sort
	// (type, address) so progress events are stable across runs.
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "z", Address: "google_storage_bucket.z"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "a", Address: "google_storage_bucket.a"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "m", Address: "google_storage_bucket.m"}},
	}
	// Since enrichers now run concurrently (#655), call-completion
	// order is non-deterministic — the deterministic-order contract is
	// proven instead by the post-Wait serial emit pass: ItemFound
	// events must arrive in sorted (type, address) order.
	rec := &recordingEmitter{}
	require.NoError(t, g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, rec))
	items := filterEvents(rec.snapshot(), "item_found")
	got := make([]string, len(items))
	for i, e := range items {
		got[i] = e.ImportID
	}
	assert.Equal(t, []string{"a", "m", "z"}, got,
		"enrich must emit ItemFound in sorted (type, address) order regardless of input order")

	// Guard against an idx-vs-i writeback bug: each IR must be marked
	// Full at its original input position, not at the sorted-dispatch
	// position. A regression here (e.g. `for i := range irs` instead
	// of `for _, i := range idx`) would still pass the emit-order
	// assertion above but corrupt the per-IR EnrichmentStatus.
	for i, ir := range irs {
		assert.Equal(t, imported.EnrichmentStatusFull, ir.Identity.EnrichmentStatus,
			"irs[%d] (ImportID=%q) must be Full at its original input position", i, ir.Identity.ImportID)
	}
}

// TestEnrichAttributes_RunsConcurrently proves the per-resource
// enrichers run in parallel AND that the fan-out is bounded by the
// worker pool (#655). It dispatches more resources than
// defaultEnrichConcurrency: a barrier enricher blocks until exactly
// defaultEnrichConcurrency goroutines are simultaneously in-flight
// (the pool cap), then releases everyone. It asserts BOTH bounds —
// maxInFlight >= 2 (concurrency happens) and maxInFlight <=
// defaultEnrichConcurrency (the pool is bounded). A regression that
// dropped grp.SetLimit (unbounded fan-out) would push maxInFlight to n
// and fail the upper bound.
func TestEnrichAttributes_RunsConcurrently(t *testing.T) {
	t.Parallel()

	// More resources than the pool cap: the extra ones can only be
	// enriched after the first batch is released.
	const n = defaultEnrichConcurrency + 3
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	barrier := make(chan struct{})
	allArrived := make(chan struct{})
	var arrivedOnce sync.Once

	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			cur := inFlight.Add(1)
			for {
				m := maxInFlight.Load()
				if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
					break
				}
			}
			// The barrier releases once exactly defaultEnrichConcurrency
			// goroutines (the pool cap) are simultaneously in-flight. A
			// bounded pool can never exceed that, so this fires exactly
			// when the pool is saturated.
			if cur == int64(defaultEnrichConcurrency) {
				arrivedOnce.Do(func() { close(allArrived) })
			}
			<-barrier // block until the test releases everyone
			inFlight.Add(-1)
			ir.Attrs = json.RawMessage(`{}`)
			return nil
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}

	irs := make([]imported.ImportedResource, n)
	for i := range irs {
		irs[i] = imported.ImportedResource{Identity: imported.ResourceIdentity{
			Type: "google_storage_bucket", ImportID: string(rune('a' + i)),
			Address: "google_storage_bucket." + string(rune('a'+i)),
		}}
	}

	done := make(chan error, 1)
	go func() {
		done <- g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil)
	}()

	// If the pool regressed to serial, only one goroutine would ever be
	// in-flight and allArrived would never close — guard the receive so
	// the test fails fast instead of hanging until the package timeout.
	select {
	case <-allArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("enrichers never reached expected concurrency — worker pool likely serial")
	}
	close(barrier)
	require.NoError(t, <-done)

	got := maxInFlight.Load()
	assert.GreaterOrEqual(t, got, int64(2),
		"enrichers must run concurrently, observed max in-flight %d", got)
	assert.LessOrEqual(t, got, int64(defaultEnrichConcurrency),
		"fan-out must be bounded by the worker pool, observed max in-flight %d", got)

	// Every resource — including the ones beyond the pool cap — must
	// still get enriched.
	for i := range irs {
		assert.NotEmpty(t, irs[i].Attrs,
			"irs[%d] (ImportID=%q) must be enriched even though it dispatched after the pool cap", i, irs[i].Identity.ImportID)
	}
}

// TestEnrichAttributes_OutOfOrderCompletionStillDeterministic proves
// that even when enrichers finish in an order unrelated to the sorted
// dispatch order, EnrichAttributes still emits ItemFound, stamps
// EnrichmentStatus, and aggregates errors in sorted (type, address)
// order — because emit/stamp/aggregate runs in the serial post-Wait
// pass (#655).
func TestEnrichAttributes_OutOfOrderCompletionStillDeterministic(t *testing.T) {
	t.Parallel()

	release := map[string]chan struct{}{
		"a": make(chan struct{}),
		"m": make(chan struct{}),
		"z": make(chan struct{}),
	}
	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			<-release[ir.Identity.ImportID]
			ir.Attrs = json.RawMessage(`{}`)
			return nil
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}

	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "z", Address: "google_storage_bucket.z"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "a", Address: "google_storage_bucket.a"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "m", Address: "google_storage_bucket.m"}},
	}

	rec := &recordingEmitter{}
	done := make(chan error, 1)
	go func() {
		done <- g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, rec)
	}()
	// Release in reverse-sorted order.
	close(release["z"])
	close(release["m"])
	close(release["a"])
	require.NoError(t, <-done)

	items := filterEvents(rec.snapshot(), "item_found")
	got := make([]string, len(items))
	for i, e := range items {
		got[i] = e.ImportID
	}
	assert.Equal(t, []string{"a", "m", "z"}, got,
		"ItemFound order must follow sorted idx, not completion order")
	for i, ir := range irs {
		assert.Equal(t, imported.EnrichmentStatusFull, ir.Identity.EnrichmentStatus,
			"irs[%d] must be Full regardless of completion order", i)
	}
}

// TestEnrichAttributes_ErrorAggregationOrder proves the joined error's
// per-resource order follows sorted idx, not goroutine completion order
// (#655). Each enricher fails after a release gate; gates fire in
// reverse-sorted order, yet the joined error must list a before b.
func TestEnrichAttributes_ErrorAggregationOrder(t *testing.T) {
	t.Parallel()

	release := map[string]chan struct{}{
		"a": make(chan struct{}),
		"b": make(chan struct{}),
	}
	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			<-release[ir.Identity.ImportID]
			return errors.New("403: " + ir.Identity.ImportID)
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}

	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "a", Address: "google_storage_bucket.a"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b", Address: "google_storage_bucket.b"}},
	}

	done := make(chan error, 1)
	go func() {
		done <- g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil)
	}()
	// b completes before a — joined error must still list a first.
	close(release["b"])
	close(release["a"])
	err := <-done
	require.Error(t, err)
	idxA := strings.Index(err.Error(), "google_storage_bucket.a")
	idxB := strings.Index(err.Error(), "google_storage_bucket.b")
	require.GreaterOrEqual(t, idxA, 0)
	require.GreaterOrEqual(t, idxB, 0)
	assert.Less(t, idxA, idxB,
		"joined error must list resources in sorted idx order regardless of completion order")
}

func TestEnrichAttributes_AccumulatesErrors(t *testing.T) {
	t.Parallel()
	failBoth := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			return errors.New("403: " + ir.Identity.ImportID)
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": failBoth}}

	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "a", Address: "google_storage_bucket.a"}},
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b", Address: "google_storage_bucket.b"}},
	}
	err := g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil)
	require.Error(t, err)
	// Both per-resource errors must be present in the joined error so
	// the operator sees every failure in one shot rather than playing
	// whack-a-mole.
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")

	// Each failed IR carries the typed Failed status plus a per-pass
	// error string so downstream consumers (#471) can render the row
	// as needs-attention without grep-ing the joined error.
	for i, ir := range irs {
		assert.Equal(t, imported.EnrichmentStatusFailed, ir.Identity.EnrichmentStatus,
			"irs[%d] must be marked Failed after enricher error", i)
		require.Len(t, ir.Identity.EnrichErrors, 1, "irs[%d] EnrichErrors must hold one entry", i)
		// The orchestrator stores the *wrapped* error (the
		// `fmt.Errorf("enrich %s/%s: %w", ...)` form that goes into
		// the joined return), not the bare per-enricher error. Pin
		// the prefix + Address so a regression to err.Error() would
		// fail; pin ImportID because the fake error embeds it.
		assert.True(t, strings.HasPrefix(ir.Identity.EnrichErrors[0], "enrich "),
			"irs[%d] EnrichErrors[0] must carry the orchestrator's wrap prefix, got %q", i, ir.Identity.EnrichErrors[0])
		assert.Contains(t, ir.Identity.EnrichErrors[0], ir.Identity.Address,
			"irs[%d] EnrichErrors must include the Address (proves the wrap path, not bare err.Error())", i)
		assert.Contains(t, ir.Identity.EnrichErrors[0], ir.Identity.ImportID,
			"irs[%d] EnrichErrors must mention the resource it failed on", i)
	}
}

func TestEnrichAttributes_DowngradesClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			return ErrEnrichClientUnavailable
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}

	rec := &recordingEmitter{}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b1", Address: "google_storage_bucket.b1"}},
	}
	err := g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: nil}, rec)
	require.NoError(t, err, "ErrEnrichClientUnavailable must downgrade to a warn, not a returned error")
	warns := filterEvents(rec.snapshot(), "service_warn")
	require.Len(t, warns, 1, "exactly one ServiceWarn must be emitted for the unavailable client")
	assert.Contains(t, warns[0].Message, "google_storage_bucket")
	assert.Contains(t, warns[0].Message, "client unavailable")

	// The downgraded warn still sets EnrichmentStatus=Failed on the IR
	// so downstream consumers can distinguish it from a happy
	// Identity-only IR. The sentinel error text is preserved in
	// EnrichErrors for triage (#471).
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[0].Identity.EnrichmentStatus,
		"unavailable-client IR must be marked Failed even though no err is returned")
	require.Len(t, irs[0].Identity.EnrichErrors, 1)
	assert.Contains(t, irs[0].Identity.EnrichErrors[0], "client unavailable")
}

func TestEnrichAttributes_EmitsItemFoundOnSuccess(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{tfType: "google_storage_bucket"}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}

	rec := &recordingEmitter{}
	// Pre-populate stale Failed-status + EnrichErrors on the IR to
	// simulate a re-run after a prior failed enrich pass. The
	// orchestrator must reset both on success — otherwise a recovered
	// resource would still carry needs-attention markers indefinitely.
	// Pins the explicit `EnrichErrors = nil` clear at enrich.go's
	// success branch (#471).
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{
			Type:             "google_storage_bucket",
			ImportID:         "b1",
			Address:          "google_storage_bucket.b1",
			Location:         "us",
			EnrichmentStatus: imported.EnrichmentStatusFailed,
			EnrichErrors:     []string{"stale prior-pass error"},
		}},
	}
	require.NoError(t, g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, rec))
	events := rec.snapshot()
	items := filterEvents(events, "item_found")
	require.Len(t, items, 1)
	assert.Equal(t, "b1", items[0].ImportID)
	assert.Equal(t, "google_storage_bucket", items[0].TFType)
	assert.Equal(t, 1, len(filterEvents(events, "service_finish")),
		"ServiceFinish must fire once per call, regardless of per-item outcomes")
	assert.Equal(t, 1, len(filterEvents(events, "service_start")),
		"ServiceStart must fire exactly once per call")

	// Happy path: typed signal flips to Full and the stale
	// EnrichErrors must be cleared so the row no longer renders as
	// needs-attention (#471).
	assert.Equal(t, imported.EnrichmentStatusFull, irs[0].Identity.EnrichmentStatus,
		"successful enrich must overwrite a prior Failed status")
	assert.Nil(t, irs[0].Identity.EnrichErrors,
		"successful enrich must clear stale EnrichErrors")
}

// rateLimitErr returns a real GCP rate-limit-shaped error: a
// *googleapi.Error with HTTP 429 that isGoogleAPIRateLimited classifies
// as a throttle.
func rateLimitErr() error {
	return &googleapi.Error{Code: 429, Message: "rate limit exceeded"}
}

// TestEnrichWithRetry_RetriesThrottleThenSucceeds proves enrichWithRetry
// retries a rate-limit error and the resource ends up enriched with no
// error surfaced. The enricher returns a 429 for the first K calls then
// nil.
func TestEnrichWithRetry_RetriesThrottleThenSucceeds(t *testing.T) {
	shrinkEnrichRetryDelays(t)

	const failFirst = 3
	// Distinctive payload written only on the successful (final) attempt
	// so the assertion pins that the retried call's result actually
	// landed — a constant `{}` would pass even if a stale write leaked.
	const successAttrs = `{"retried":true,"bucket":"b1"}`
	var calls atomic.Int64
	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			if calls.Add(1) <= failFirst {
				return rateLimitErr()
			}
			ir.Attrs = json.RawMessage(successAttrs)
			return nil
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b1", Address: "google_storage_bucket.b1"}},
	}
	require.NoError(t, g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil),
		"a rate-limit that eventually clears must not surface as an error")
	assert.JSONEq(t, successAttrs, string(irs[0].Attrs),
		"resource must end up enriched with the retried call's exact payload")
	assert.Equal(t, imported.EnrichmentStatusFull, irs[0].Identity.EnrichmentStatus,
		"retried-to-success IR must be marked Full")
	assert.Equal(t, int64(failFirst+1), calls.Load(),
		"enricher must be retried exactly failFirst times then succeed")
}

// TestEnrichWithRetry_NonThrottleNotRetried proves a non-rate-limit
// error is surfaced immediately without retry — the enricher is called
// once.
func TestEnrichWithRetry_NonThrottleNotRetried(t *testing.T) {
	shrinkEnrichRetryDelays(t)

	var calls atomic.Int64
	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			calls.Add(1)
			return errors.New("403: permission denied")
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b1", Address: "google_storage_bucket.b1"}},
	}
	err := g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil)
	require.Error(t, err, "a non-rate-limit error must surface as a per-resource error")
	assert.Equal(t, int64(1), calls.Load(), "a non-rate-limit error must not be retried")
}

// TestEnrichWithRetry_GivesUpAfterMaxAttempts proves the retry loop
// gives up after enrichRetryMaxAttempts and surfaces the rate-limit
// error.
func TestEnrichWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	shrinkEnrichRetryDelays(t)

	var calls atomic.Int64
	enr := &fakeEnricher{
		tfType: "google_storage_bucket",
		result: func(ir *imported.ImportedResource) error {
			calls.Add(1)
			return rateLimitErr()
		},
	}
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"google_storage_bucket": enr}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", ImportID: "b1", Address: "google_storage_bucket.b1"}},
	}
	err := g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil)
	require.Error(t, err, "an unrelenting rate-limit must eventually surface as an error")
	assert.Equal(t, int64(enrichRetryMaxAttempts), calls.Load(),
		"retry loop must attempt exactly enrichRetryMaxAttempts times before giving up")
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[0].Identity.EnrichmentStatus,
		"a give-up after max attempts must mark the IR Failed")
}

// TestIsGoogleAPIRateLimited covers the GCP rate-limit classifier
// across the HTTP-status path and the per-error Reason scan.
func TestIsGoogleAPIRateLimited(t *testing.T) {
	t.Parallel()
	assert.False(t, isGoogleAPIRateLimited(nil), "nil err must not be rate-limited")
	assert.False(t, isGoogleAPIRateLimited(errors.New("plain")), "non-Google error must not be rate-limited")
	assert.True(t, isGoogleAPIRateLimited(&googleapi.Error{Code: 429}))
	assert.True(t, isGoogleAPIRateLimited(&googleapi.Error{Code: 503}))
	assert.False(t, isGoogleAPIRateLimited(&googleapi.Error{Code: 404}),
		"a 404 must not classify as a rate-limit")
	reasoned := &googleapi.Error{Code: 403}
	reasoned.Errors = []googleapi.ErrorItem{{Reason: "userRateLimitExceeded"}}
	assert.True(t, isGoogleAPIRateLimited(reasoned),
		"a non-429 error carrying a rate-limit Reason must classify as a rate-limit")
}

// filterEvents returns the subset of recorded events whose Kind matches
// kind. Tiny helper kept local to enrich_test.go since it is only used
// by the EnrichAttributes assertions and the existing testhelpers
// recordingEmitter does not export a per-kind accessor.
func filterEvents(events []recordedEvent, kind string) []recordedEvent {
	out := make([]recordedEvent, 0, len(events))
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}
