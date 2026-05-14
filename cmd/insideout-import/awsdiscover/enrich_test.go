package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeEnricher is a minimal AttributeEnricher that records its calls and
// returns a configurable result. Used to exercise EnrichAttributes
// dispatch / ordering / error-accumulation semantics without standing
// up real SDK clients. Mirrors gcpdiscover.fakeEnricher.
type fakeEnricher struct {
	tfType string
	calls  []string                               // bucket-of-Identity.ImportID per call
	result func(*imported.ImportedResource) error // per-call result
}

func (f *fakeEnricher) ResourceType() string { return f.tfType }
func (f *fakeEnricher) Enrich(_ context.Context, ir *imported.ImportedResource, _ EnrichClients) error {
	f.calls = append(f.calls, ir.Identity.ImportID)
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
	irs := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "z", Address: "aws_dynamodb_table.z"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "a", Address: "aws_dynamodb_table.a"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "m", Address: "aws_dynamodb_table.m"}},
	}
	require.NoError(t, a.EnrichAttributes(context.Background(), irs, EnrichClients{DynamoDB: &dynamodb.Client{}}, nil))
	assert.Equal(t, []string{"a", "m", "z"}, enr.calls,
		"enrich must dispatch in sorted (type, address) order regardless of input order")
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
