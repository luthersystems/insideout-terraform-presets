package gcpdiscover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeByIDEnricher is a minimal type that satisfies ByIDEnricher.
// It exists only to assert that the interface compiles and is
// satisfiable — no production code implements ByIDEnricher yet
// (Phase 2 enricher rollout PRs add real impls one per type).
type fakeByIDEnricher struct{}

func (fakeByIDEnricher) ResourceType() string { return "google_test_fake" }

func (fakeByIDEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// Compile-time assertion. If ByIDEnricher's shape drifts, this fails
// at build time, not at runtime.
var _ ByIDEnricher = (*fakeByIDEnricher)(nil)

func TestByIDEnricher_FakeImplementation(t *testing.T) {
	var e ByIDEnricher = fakeByIDEnricher{}
	if got, want := e.ResourceType(), "google_test_fake"; got != want {
		t.Errorf("ResourceType() = %q, want %q", got, want)
	}
	raw, err := e.EnrichByID(t.Context(), &imported.ResourceIdentity{Type: "google_test_fake"}, EnrichClients{})
	if err != nil {
		t.Fatalf("EnrichByID returned err: %v", err)
	}
	if string(raw) != "{}" {
		t.Errorf("EnrichByID payload = %q, want %q", string(raw), "{}")
	}
}

// TestExistingEnrichersDoNotImplementByID confirms the additive
// nature of ByIDEnricher: the 5 existing enrichers today implement
// only AttributeEnricher. When Phase 2 PRs land real EnrichByID
// impls, this test's allowlist shrinks. The test fails loud if a
// future PR claims to add EnrichByID but forgets to update the
// allowlist — i.e. ByIDEnricher coverage is observable.
func TestExistingEnrichersDoNotImplementByID(t *testing.T) {
	// Mirror the production registration (gcpdiscover.go:343-349) so
	// the test exercises real enrichers without needing a full
	// GCPDiscoverer construction.
	enrichers := map[string]AttributeEnricher{
		"google_storage_bucket":        newStorageBucketEnricher(),
		"google_pubsub_topic":          newPubsubTopicEnricher(),
		"google_pubsub_subscription":   newPubsubSubscriptionEnricher(),
		"google_secret_manager_secret": newSecretManagerSecretEnricher(),
		"google_compute_network":       newComputeNetworkEnricher(),
	}
	// Allowlist: types whose enrichers explicitly DO NOT implement
	// ByIDEnricher yet. Shrink as Phase 2 rollout lands per-type impls.
	notImplemented := map[string]bool{
		"google_storage_bucket":        true,
		"google_pubsub_topic":          true,
		"google_pubsub_subscription":   true,
		"google_secret_manager_secret": true,
		"google_compute_network":       true,
	}
	for tfType, enr := range enrichers {
		_, implementsByID := enr.(ByIDEnricher)
		expectNot := notImplemented[tfType]
		switch {
		case expectNot && implementsByID:
			t.Errorf("%s: enricher now implements ByIDEnricher — remove from notImplemented allowlist", tfType)
		case !expectNot && !implementsByID:
			t.Errorf("%s: enricher must implement ByIDEnricher (not in notImplemented allowlist)", tfType)
		}
	}
}
