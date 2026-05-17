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
//
// Issue #571: the five pre-Phase-2 enrichers (storage_bucket,
// pubsub_topic, pubsub_subscription, secret_manager_secret,
// compute_network) now implement ByIDEnricher, so the allowlist is
// empty. The map is retained as a structural placeholder so a future
// regression that re-introduces an AttributeEnricher-only enricher
// fails this test loudly (and the fix is obvious: add the type here
// only as a deliberate, time-bounded exception).
func TestExistingEnrichersDoNotImplementByID(t *testing.T) {
	// Nil searcher is safe — the constructor only stores it; no
	// SearchAll call fires here.
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})

	// Allowlist: types whose enrichers explicitly DO NOT implement
	// ByIDEnricher yet. Empty post-#571 — keep the map structure so
	// future regressions land here visibly.
	notImplemented := map[string]bool{}

	// Fail-fast: pin the expected total byTypeEnricher size so a
	// silent drop (or duplicate-key squashing) in production fails the
	// test. The expected total = hand-rolled enricher count +
	// (cloudAssetTypeConfigs entries that aren't hand-rolled overrides).
	// Recomputed at runtime from cloudAssetTypeConfigs so adding a new
	// CAI config entry only requires changing cloudasset_types.go.
	// Hand-rolled count is the literal from gcpdiscover.go's
	// byTypeEnricher initializer: was 31 pre-#581; minus the three
	// retired hand-rolled enrichers (compute_address, pubsub_topic,
	// pubsub_subscription) whose CAI fallback now achieves byte-equal
	// parity via the #581 computed-only filter + #580 Normalizer kit
	// (see computed_only_parity_test.go for the regression guard) = 28.
	const handRolledCount = 28
	caiNonOverlap := 0
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.Skip {
			continue
		}
		switch cfg.TFType {
		case "google_storage_bucket",
			"google_secret_manager_secret",
			"google_compute_network",
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
