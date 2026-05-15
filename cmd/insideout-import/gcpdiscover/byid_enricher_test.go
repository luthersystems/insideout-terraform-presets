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
func TestExistingEnrichersDoNotImplementByID(t *testing.T) {
	// Nil searcher is safe — the constructor only stores it; no
	// SearchAll call fires here.
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})

	// Allowlist: types whose enrichers explicitly DO NOT implement
	// ByIDEnricher yet. Shrink as Phase 2 rollout lands per-type impls.
	// The 5 hand-rolled enrichers below are the original pre-Phase-2
	// set; the two newer hand-rolled enrichers (compute_address,
	// compute_firewall) DO implement ByIDEnricher and stay off the
	// allowlist. Every cloudAssetEnricher implements ByIDEnricher by
	// construction (compile-time pin in cloudasset_enricher.go), so
	// CAI-routed types are never on this allowlist.
	notImplemented := map[string]bool{
		"google_storage_bucket":        true,
		"google_pubsub_topic":          true,
		"google_pubsub_subscription":   true,
		"google_secret_manager_secret": true,
		"google_compute_network":       true,
	}

	// Fail-fast: pin the expected total byTypeEnricher size so a
	// silent drop (or duplicate-key squashing) in production fails the
	// test. The expected total = 7 hand-rolled enrichers + every type
	// in cloudAssetTypeConfigs that doesn't have a hand-rolled override.
	// The latter is computed at test time so an addition to
	// cloudAssetTypeConfigs doesn't silently flow into the production
	// enricher coverage without a deliberate test update — the math
	// below makes that change visible as a numeric diff in the test
	// failure message. Mirrors the AWS byid_enricher_test pattern.
	handRolledTypes := map[string]bool{
		"google_compute_address":       true,
		"google_compute_firewall":      true,
		"google_compute_network":       true,
		"google_pubsub_subscription":   true,
		"google_pubsub_topic":          true,
		"google_secret_manager_secret": true,
		"google_storage_bucket":        true,
	}
	handRolled := len(handRolledTypes)
	caiOverrides := 0
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.Skip {
			continue
		}
		if handRolledTypes[cfg.TFType] {
			caiOverrides++
		}
	}
	caiActive := 0
	for _, cfg := range cloudAssetTypeConfigs {
		if !cfg.Skip {
			caiActive++
		}
	}
	wantTotal := handRolled + caiActive - caiOverrides
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

// TestCloudAssetEnricherCoversEveryCAIRoutedType asserts that every
// TF type in cloudAssetTypeConfigs (with Skip=false) has a registered
// AttributeEnricher in NewGCPDiscoverer.byTypeEnricher — either a
// hand-rolled override or a generic cloudAssetEnricher. A regression
// that drops the cloudAssetEnricher wiring loop in NewGCPDiscoverer
// would silently strip CAI coverage from ~28 types; this test catches
// that as a per-type miss rather than waiting for a downstream
// integration test to surface the regression. Mirrors AWS's
// TestCloudControlEnricherCoversEveryCCRoutedType.
func TestCloudAssetEnricherCoversEveryCAIRoutedType(t *testing.T) {
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.Skip {
			continue
		}
		if _, ok := d.byTypeEnricher[cfg.TFType]; !ok {
			t.Errorf("cloudasset-routed TFType %q has no registered AttributeEnricher", cfg.TFType)
		}
	}
}

// TestCloudAssetEnricherSkipsHandRolledOverrides asserts the
// override-wins invariant in NewGCPDiscoverer's wiring loop: for every
// TF type that has a hand-rolled enricher AND a cloudAssetTypeConfigs
// entry, the registered enricher must be the hand-rolled one (not the
// generic cloudAssetEnricher). A silent regression that flips the
// override order would replace the higher-fidelity hand-rolled payloads
// with the lower-fidelity CAI payloads. Mirrors AWS's
// TestCloudControlEnricherSkipsHandRolledOverrides.
func TestCloudAssetEnricherSkipsHandRolledOverrides(t *testing.T) {
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	// Mirrors the hand-rolled set in buildByTypeEnricher. Kept as a
	// literal slice rather than a reflection-based scan so adding a new
	// hand-rolled enricher requires an explicit update here — the next
	// reviewer sees the intent in the test diff.
	handRolled := []string{
		"google_compute_address",
		"google_compute_firewall",
		"google_compute_network",
		"google_pubsub_subscription",
		"google_pubsub_topic",
		"google_secret_manager_secret",
		"google_storage_bucket",
	}
	for _, tfType := range handRolled {
		enr, ok := d.byTypeEnricher[tfType]
		if !ok {
			t.Errorf("%s: missing from byTypeEnricher", tfType)
			continue
		}
		if _, isCAI := enr.(*cloudAssetEnricher); isCAI {
			t.Errorf("%s: registered enricher is cloudAssetEnricher, want hand-rolled override", tfType)
		}
	}
}
