package gcpdiscover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeByIDEnricher is a minimal type that satisfies ByIDEnricher.
// It exists only to back the compile-time `var _ ByIDEnricher`
// assertion below — no production code implements ByIDEnricher yet
// (Phase 2 enricher rollout PRs add real impls one per type).
type fakeByIDEnricher struct{}

func (fakeByIDEnricher) ResourceType() string { return "google_test_fake" }

func (fakeByIDEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// Compile-time assertion. If ByIDEnricher's shape drifts, this fails
// at build time. This is the load-bearing shape lock — no runtime
// "fake calls itself" test is needed because there is nothing to
// observe beyond the compile.
var _ ByIDEnricher = (*fakeByIDEnricher)(nil)

// TestExistingEnrichersDoNotImplementByID pins the per-type
// ByIDEnricher implementation status against the REAL production
// registration in NewGCPDiscoverer. As Phase 2 PRs add real
// EnrichByID impls, the allowlist must shrink in lockstep with the
// production registration. A production-only change (add a
// ByIDEnricher impl, forget to update allowlist; or vice versa) fails
// the test loud. A regression that drops the registration entirely is
// caught by the explicit wantTotal size check below.
//
// CAI HYBRID enricher (this PR): the cloudAssetEnricher implements
// BOTH AttributeEnricher and ByIDEnricher, so every CAI-routed type
// without a hand-rolled override is implicitly off the allowlist.
func TestExistingEnrichersDoNotImplementByID(t *testing.T) {
	// Nil searcher is safe — the constructor only stores it; no
	// SearchAll call fires here.
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})

	// Allowlist: types whose enrichers explicitly DO NOT implement
	// ByIDEnricher yet. Shrink as Phase 2 rollout lands per-type impls.
	// Five pre-Phase-2 hand-rolled enrichers remain on this list;
	// every CAI-routed type registered via cloudAssetTypeConfigs (28
	// additions) implements ByIDEnricher transparently because
	// cloudAssetEnricher implements both interfaces.
	notImplemented := map[string]bool{
		"google_storage_bucket":        true,
		"google_pubsub_topic":          true,
		"google_pubsub_subscription":   true,
		"google_secret_manager_secret": true,
		"google_compute_network":       true,
	}

	// Fail-fast: pin the expected total byTypeEnricher size so a
	// silent drop (or duplicate-key squashing) in production fails the
	// test. The expected total = hand-rolled enricher count +
	// (cloudAssetTypeConfigs entries that aren't hand-rolled overrides).
	// Recomputed at runtime from cloudAssetTypeConfigs so adding a new
	// CAI config entry only requires changing cloudasset_types.go.
	// Hand-rolled count is the literal from gcpdiscover.go's
	// byTypeEnricher initializer: 5 pre-Phase-2 + compute_address +
	// compute_firewall + Bundle G5 (compute_instance, compute_router,
	// kms_crypto_key, service_account, sql_database_instance) = 12.
	const handRolledCount = 12
	caiNonOverlap := 0
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.Skip {
			continue
		}
		switch cfg.TFType {
		case "google_storage_bucket",
			"google_pubsub_topic",
			"google_pubsub_subscription",
			"google_secret_manager_secret",
			"google_compute_network",
			"google_compute_address",
			"google_compute_firewall",
			"google_compute_instance",
			"google_compute_router",
			"google_kms_crypto_key",
			"google_service_account",
			"google_sql_database_instance":
			// Hand-rolled override wins; this CAI entry doesn't add a
			// new map slot.
			continue
		}
		caiNonOverlap++
	}
	wantTotal := handRolledCount + caiNonOverlap
	if got := len(d.byTypeEnricher); got != wantTotal {
		t.Errorf("byTypeEnricher size = %d, want %d (production registration drifted from test)", got, wantTotal)
	}

	for tfType, enr := range d.byTypeEnricher {
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
