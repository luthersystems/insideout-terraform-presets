package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// fakeEnricher is a minimal AttributeEnricher that records its calls and
// returns a configurable result. Used to exercise EnrichAttributes
// dispatch / ordering / error-accumulation semantics without standing
// up real SDK clients. Mirrors gcpdiscover.fakeEnricher.
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
	a := &AWSDiscoverer{
		byTypeEnricher: map[string]AttributeEnricher{
			"aws_dynamodb_table": &fakeEnricher{tfType: "aws_dynamodb_table"},
		},
	}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", ImportID: "q1", Address: "aws_sqs_queue.q1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil))

	// Only the dynamodb_table got enriched.
	assert.Empty(t, irs[0].Attrs, "aws_sqs_queue has no enricher; Attrs must remain empty")
	assert.NotEmpty(t, irs[1].Attrs, "aws_dynamodb_table Attrs must be populated")
}

func TestEnrichAttributes_DeterministicOrder(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{tfType: "aws_dynamodb_table"}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	// Intentionally out-of-order Addresses; aggregator must sort
	// (type, address) so progress events are stable across runs.
	// Since enrichers now run concurrently (#655), call-completion
	// order is non-deterministic — the deterministic-order contract is
	// proven instead by the post-Wait serial emit pass: ItemFound
	// events must arrive in sorted (type, address) order.
	rec := &recordingEmitter{}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "z", Address: "aws_dynamodb_table.z"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "a", Address: "aws_dynamodb_table.a"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "m", Address: "aws_dynamodb_table.m"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, rec))
	items := filterEvents(rec.snapshot(), "item_found")
	got := make([]string, len(items))
	for i, e := range items {
		got[i] = e.ImportID
	}
	assert.Equal(t, []string{"a", "m", "z"}, got,
		"enrich must emit ItemFound in sorted (type, address) order regardless of input order")
}

// TestEnrichAttributes_RunsConcurrently proves the per-resource
// enrichers run in parallel AND that the fan-out is bounded by the
// worker pool (#655). It dispatches more resources than
// defaultEnrichConcurrency: a barrier enricher blocks until exactly
// defaultEnrichConcurrency goroutines are simultaneously in-flight
// (the pool cap), then releases everyone. It asserts BOTH bounds —
// maxInFlight >= 2 (concurrency happens) and maxInFlight <=
// defaultEnrichConcurrency (the pool is bounded). A regression that
// dropped g.SetLimit (unbounded fan-out) would push maxInFlight to n
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
		tfType: "aws_dynamodb_table",
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
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	irs := make([]imported.ImportedResource, n)
	for i := range irs {
		irs[i] = imported.ImportedResource{Identity: imported.ResourceIdentity{
			Type: "aws_dynamodb_table", ImportID: string(rune('a' + i)),
			Address: "aws_dynamodb_table." + string(rune('a'+i)),
		}}
	}

	done := make(chan error, 1)
	go func() {
		done <- a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil)
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
// dispatch order, EnrichAttributes still emits ItemFound and aggregates
// errors in sorted (type, address) order — because emit/aggregate runs
// in the serial post-Wait pass (#655).
func TestEnrichAttributes_OutOfOrderCompletionStillDeterministic(t *testing.T) {
	t.Parallel()

	// "a" finishes last, "z" finishes first — gated by per-ImportID
	// release channels so completion order is the reverse of sorted
	// order.
	release := map[string]chan struct{}{
		"a": make(chan struct{}),
		"m": make(chan struct{}),
		"z": make(chan struct{}),
	}
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			<-release[ir.Identity.ImportID]
			ir.Attrs = json.RawMessage(`{}`)
			return nil
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "z", Address: "aws_dynamodb_table.z"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "a", Address: "aws_dynamodb_table.a"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "m", Address: "aws_dynamodb_table.m"}},
	}

	rec := &recordingEmitter{}
	done := make(chan error, 1)
	go func() {
		done <- a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, rec)
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
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			<-release[ir.Identity.ImportID]
			return errors.New("403: " + ir.Identity.ImportID)
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "a", Address: "aws_dynamodb_table.a"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "b", Address: "aws_dynamodb_table.b"}},
	}

	done := make(chan error, 1)
	go func() {
		done <- a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil)
	}()
	// b completes before a — joined error must still list a first.
	close(release["b"])
	close(release["a"])
	err := <-done
	require.Error(t, err)
	idxA := strings.Index(err.Error(), "aws_dynamodb_table.a")
	idxB := strings.Index(err.Error(), "aws_dynamodb_table.b")
	require.GreaterOrEqual(t, idxA, 0)
	require.GreaterOrEqual(t, idxB, 0)
	assert.Less(t, idxA, idxB,
		"joined error must list resources in sorted idx order regardless of completion order")
}

func TestEnrichAttributes_AccumulatesErrors(t *testing.T) {
	t.Parallel()
	failBoth := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			return errors.New("403: " + ir.Identity.ImportID)
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": failBoth}}

	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "a", Address: "aws_dynamodb_table.a"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "b", Address: "aws_dynamodb_table.b"}},
	}
	err := a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil)
	require.Error(t, err)
	// Both per-resource errors must be present in the joined error so
	// the operator sees every failure in one shot rather than playing
	// whack-a-mole.
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
	// Each failed resource also carries the typed failure marker with
	// the wrapped error text (#654).
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[0].Identity.EnrichmentStatus)
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[1].Identity.EnrichmentStatus)
	require.NotEmpty(t, irs[0].Identity.EnrichErrors)
	require.NotEmpty(t, irs[1].Identity.EnrichErrors)
	assert.Contains(t, irs[0].Identity.EnrichErrors[0], "a")
	assert.Contains(t, irs[1].Identity.EnrichErrors[0], "b")
}

func TestEnrichAttributes_DowngradesClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			return ErrEnrichClientUnavailable
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	rec := &recordingEmitter{}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	err := a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: nil}, rec)
	require.NoError(t, err, "ErrEnrichClientUnavailable must downgrade to a warn, not a returned error")
	warns := filterEvents(rec.snapshot(), "service_warn")
	require.Len(t, warns, 1, "exactly one ServiceWarn must be emitted for the unavailable client")
	assert.Contains(t, warns[0].Message, "aws_dynamodb_table")
	assert.Contains(t, warns[0].Message, "client unavailable")
	// The downgrade still stamps the typed failure marker so a caller
	// can distinguish this from a happy Identity-only IR (#654).
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[0].Identity.EnrichmentStatus)
	assert.NotEmpty(t, irs[0].Identity.EnrichErrors)
}

// TestEnrichAttributes_DowngradesNotFound pins the #654 contract: an
// enricher returning ErrNotFound — the resource, or a sub-resource it
// reads, genuinely does not exist — is downgraded to a ServiceWarn and
// NOT accumulated into the returned batch error, while still stamping
// the typed EnrichmentStatusFailed marker so callers (and the composer's
// drop-uncomposable filter) can flag the resource.
func TestEnrichAttributes_DowngradesNotFound(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			return fmt.Errorf("table %q: %w", ir.Identity.ImportID, ErrNotFound)
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	rec := &recordingEmitter{}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	err := a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, rec)
	require.NoError(t, err, "ErrNotFound must downgrade to a warn, not a returned batch error")

	warns := filterEvents(rec.snapshot(), "service_warn")
	require.Len(t, warns, 1, "exactly one ServiceWarn must be emitted for the not-found resource")
	assert.Contains(t, warns[0].Message, "aws_dynamodb_table")
	assert.Contains(t, warns[0].Message, "t1",
		"the warn message must carry the per-resource failure detail")

	// A not-found resource is not a discovered item — ItemFound must
	// not fire for it.
	assert.Empty(t, filterEvents(rec.snapshot(), "item_found"),
		"a not-found resource must not be reported as ItemFound")

	assert.Equal(t, imported.EnrichmentStatusFailed, irs[0].Identity.EnrichmentStatus,
		"a not-found resource must be stamped EnrichmentStatusFailed")
	require.NotEmpty(t, irs[0].Identity.EnrichErrors, "EnrichErrors must carry the failure text")
	assert.Contains(t, irs[0].Identity.EnrichErrors[0], "t1")
}

// TestEnrichAttributes_NotFoundDowngradedAlongsideRealError pins the
// headline #654 interaction: in a heterogeneous batch an ErrNotFound
// resource is downgraded (warn, no entry in the returned error) while a
// genuine failure in the same pass is still accumulated — and both
// resources are stamped EnrichmentStatusFailed.
func TestEnrichAttributes_NotFoundDowngradedAlongsideRealError(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			if ir.Identity.ImportID == "missing" {
				return fmt.Errorf("table %q: %w", ir.Identity.ImportID, ErrNotFound)
			}
			return errors.New("403: " + ir.Identity.ImportID)
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	rec := &recordingEmitter{}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "missing", Address: "aws_dynamodb_table.missing"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "denied", Address: "aws_dynamodb_table.denied"}},
	}
	err := a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, rec)

	// The genuine 403 is still surfaced; the ErrNotFound resource is not.
	require.Error(t, err, "a genuine failure must still be returned")
	assert.Contains(t, err.Error(), "denied", "the real failure must be in the joined error")
	assert.NotContains(t, err.Error(), "missing",
		"the downgraded ErrNotFound resource must not appear in the joined error")

	// Exactly one warn — for the downgraded ErrNotFound resource.
	warns := filterEvents(rec.snapshot(), "service_warn")
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0].Message, "missing")

	// Both resources carry the typed failure marker regardless of which
	// arm handled them.
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[0].Identity.EnrichmentStatus)
	assert.Equal(t, imported.EnrichmentStatusFailed, irs[1].Identity.EnrichmentStatus)
	assert.NotEmpty(t, irs[0].Identity.EnrichErrors)
	assert.NotEmpty(t, irs[1].Identity.EnrichErrors)
}

// TestEnrichAttributes_StampsEnrichmentStatusFull pins that a successful
// enrich stamps EnrichmentStatusFull and leaves EnrichErrors empty — the
// machine-readable per-resource signal callers use instead of inspecting
// Attrs or the joined batch error (#654).
func TestEnrichAttributes_StampsEnrichmentStatusFull(t *testing.T) {
	t.Parallel()
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"aws_dynamodb_table": &fakeEnricher{tfType: "aws_dynamodb_table"},
	}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil))
	assert.Equal(t, imported.EnrichmentStatusFull, irs[0].Identity.EnrichmentStatus)
	assert.Empty(t, irs[0].Identity.EnrichErrors)
}

func TestEnrichAttributes_EmitsItemFoundOnSuccess(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{tfType: "aws_dynamodb_table"}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	rec := &recordingEmitter{}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1", Region: "us-east-1"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, rec))
	events := rec.snapshot()
	items := filterEvents(events, "item_found")
	require.Len(t, items, 1)
	assert.Equal(t, "t1", items[0].ImportID)
	assert.Equal(t, "aws_dynamodb_table", items[0].TFType)
	assert.Equal(t, "us-east-1", items[0].Region,
		"ItemFound's region argument must carry the per-resource region for the SSE consumer")
	assert.Equal(t, 1, len(filterEvents(events, "service_finish")),
		"ServiceFinish must fire once per call, regardless of per-item outcomes")
	assert.Equal(t, 1, len(filterEvents(events, "service_start")),
		"ServiceStart must fire exactly once per call")
}

// TestEnrichAttributes_S3ClientUnused asserts that the EnrichClients
// shape is forward-compatible with the aws_s3_bucket enricher that
// arrives after presets bundle #461. Until then no enricher consumes
// the S3 field, but its presence on the struct keeps the consumer-side
// shape stable. This test pins that today's surface compiles when an
// S3 client is plumbed through, so the future S3 enricher can be wired
// up with a one-line registration.
func TestEnrichAttributes_S3ClientUnused(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{tfType: "aws_dynamodb_table"}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}

	clients := EnrichClients{
		S3:        &s3.Client{},
		DynamoDB:  &dynamodb.Client{},
		AccountID: "012345678901",
	}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, clients, nil))
	require.NotEmpty(t, irs[0].Attrs)
}

// throttleErr returns a real AWS throttle-shaped error: a
// smithy.GenericAPIError carrying the ThrottlingException code that
// isThrottleError classifies as a throttle.
func throttleErr() error {
	return &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
}

// TestEnrichWithRetry_RetriesThrottleThenSucceeds proves enrichWithRetry
// retries a throttle error and the resource ends up enriched with no
// error surfaced. The enricher returns a throttle for the first K calls
// then nil.
func TestEnrichWithRetry_RetriesThrottleThenSucceeds(t *testing.T) {
	shrinkEnrichRetryDelays(t)

	const failFirst = 3
	// Distinctive payload written only on the successful (final) attempt
	// so the assertion pins that the retried call's result actually
	// landed — a constant `{}` would pass even if a stale write leaked.
	const successAttrs = `{"retried":true,"table":"t1"}`
	var calls atomic.Int64
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			if calls.Add(1) <= failFirst {
				return throttleErr()
			}
			ir.Attrs = json.RawMessage(successAttrs)
			return nil
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil),
		"a throttle that eventually clears must not surface as an error")
	assert.JSONEq(t, successAttrs, string(irs[0].Attrs),
		"resource must end up enriched with the retried call's exact payload")
	assert.Equal(t, int64(failFirst+1), calls.Load(),
		"enricher must be retried exactly failFirst times then succeed")
}

// TestEnrichWithRetry_NonThrottleNotRetried proves a non-throttle error
// is surfaced immediately without retry — the enricher is called once.
func TestEnrichWithRetry_NonThrottleNotRetried(t *testing.T) {
	shrinkEnrichRetryDelays(t)

	var calls atomic.Int64
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			calls.Add(1)
			return errors.New("403: access denied")
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	err := a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil)
	require.Error(t, err, "a non-throttle error must surface as a per-resource error")
	assert.Equal(t, int64(1), calls.Load(), "a non-throttle error must not be retried")
}

// TestEnrichWithRetry_GivesUpAfterMaxAttempts proves the retry loop
// gives up after enrichRetryMaxAttempts and surfaces the throttle error.
func TestEnrichWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	shrinkEnrichRetryDelays(t)

	var calls atomic.Int64
	enr := &fakeEnricher{
		tfType: "aws_dynamodb_table",
		result: func(ir *imported.ImportedResource) error {
			calls.Add(1)
			return throttleErr()
		},
	}
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{"aws_dynamodb_table": enr}}
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", Address: "aws_dynamodb_table.t1"}},
	}
	err := a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil)
	require.Error(t, err, "an unrelenting throttle must eventually surface as an error")
	assert.Contains(t, err.Error(), "ThrottlingException")
	assert.Equal(t, int64(enrichRetryMaxAttempts), calls.Load(),
		"retry loop must attempt exactly enrichRetryMaxAttempts times before giving up")
}

// TestIsThrottleError covers the AWS throttle classifier across the
// smithy.APIError code set and the HTTP-status path.
func TestIsThrottleError(t *testing.T) {
	t.Parallel()
	assert.False(t, isThrottleError(nil), "nil err must not be a throttle")
	assert.False(t, isThrottleError(errors.New("plain")), "non-AWS error must not be a throttle")
	assert.True(t, isThrottleError(&smithy.GenericAPIError{Code: "ThrottlingException"}))
	assert.True(t, isThrottleError(&smithy.GenericAPIError{Code: "SlowDown"}))
	assert.True(t, isThrottleError(&smithy.GenericAPIError{Code: "RequestLimitExceeded"}))
	assert.False(t, isThrottleError(&smithy.GenericAPIError{Code: "AccessDenied"}),
		"a non-throttle API error code must not classify as a throttle")
	// CloudControl wraps a downstream service throttle as a 400 handler
	// failure whose error CODE is generic (GeneralServiceException) — the
	// throttle signal lives only in the message. Classify it by message so
	// the discovery retry/backoff engages on this shape (the exact error a
	// reverse-import "scan my account" hit on ElastiCache).
	assert.True(t, isThrottleError(&smithy.GenericAPIError{
		Code:    "GeneralServiceException",
		Message: "AWS::ElastiCache::ReplicationGroup Handler returned status FAILED: Rate exceeded (Service: ElastiCache, Status Code: 400)",
	}), "CloudControl 400 handler-FAILED 'Rate exceeded' must classify as a throttle")
	assert.True(t, isThrottleError(errors.New("operation error: Throttling: slow down")),
		"a plain error whose message says Throttling must classify as a throttle")
	assert.False(t, isThrottleError(&smithy.GenericAPIError{Code: "GeneralServiceException", Message: "validation failed"}),
		"a non-throttle handler failure must not be misclassified as a throttle")
}

func TestRetryThrottled_ContextCancelPropagates(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := retryThrottled(ctx, 2, time.Hour, time.Hour, func() error {
		return &smithy.GenericAPIError{Code: "ThrottlingException"}
	})
	assert.ErrorIs(t, err, context.Canceled,
		"parent cancellation must not be returned as the prior throttle error; callers use isThrottleError(err) to soft-skip")
}

// filterEvents returns the subset of recorded events whose Kind matches
// kind. Mirrors gcpdiscover.filterEvents — kept per-package so each
// cloud's test suite is self-contained.
func filterEvents(events []recordedEvent, kind string) []recordedEvent {
	out := make([]recordedEvent, 0, len(events))
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}
