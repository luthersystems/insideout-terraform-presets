package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeEnricher is a minimal AttributeEnricher that records its calls
// and returns a configurable result. Used to exercise EnrichAttributes
// dispatch / ordering / error-accumulation semantics without standing
// up real SDK clients.
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
	require.NoError(t, g.EnrichAttributes(context.Background(), irs, EnrichClients{Storage: &storagev1.Service{}}, nil))
	assert.Equal(t, []string{"a", "m", "z"}, enr.calls,
		"enrich must dispatch in sorted (type, address) order regardless of input order")

	// Guard against an idx-vs-i writeback bug: each IR must be marked
	// Full at its original input position, not at the sorted-dispatch
	// position. A regression here (e.g. `for i := range irs` instead
	// of `for _, i := range idx`) would still pass the dispatch-order
	// assertion above but corrupt the per-IR EnrichmentStatus.
	for i, ir := range irs {
		assert.Equal(t, imported.EnrichmentStatusFull, ir.Identity.EnrichmentStatus,
			"irs[%d] (ImportID=%q) must be Full at its original input position", i, ir.Identity.ImportID)
	}
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
