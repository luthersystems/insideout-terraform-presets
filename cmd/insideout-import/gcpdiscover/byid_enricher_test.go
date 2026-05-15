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

// TestExistingEnrichersDoNotImplementByID confirms the additive
// nature of ByIDEnricher: the 5 existing enrichers today implement
// only AttributeEnricher. The test pins against the REAL production
// registration in NewGCPDiscoverer — not a hand-rolled map — so when
// Phase 2 PRs add real EnrichByID impls, the allowlist must shrink
// in lockstep with the production registration. A production-only
// change (add a ByIDEnricher impl, forget to update allowlist) fails
// the test loud. A regression that drops the registration entirely
// is caught by the size sanity check below.
func TestExistingEnrichersDoNotImplementByID(t *testing.T) {
	// Nil searcher is safe — the constructor only stores it; no
	// SearchAll call fires here.
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})

	// Allowlist: types whose enrichers explicitly DO NOT implement
	// ByIDEnricher yet. Shrink as Phase 2 rollout lands per-type impls.
	notImplemented := map[string]bool{
		"google_storage_bucket":        true,
		"google_pubsub_topic":          true,
		"google_pubsub_subscription":   true,
		"google_secret_manager_secret": true,
		"google_compute_network":       true,
	}

	// Fail-fast: if the constructor silently dropped the registration,
	// the for-range below iterates zero times and reports a green
	// non-test. Pin the expected size to the allowlist length so a
	// "silent drop in production, allowlist untouched" regression
	// fails loud.
	if got, want := len(d.byTypeEnricher), len(notImplemented); got != want {
		t.Errorf("byTypeEnricher size = %d, want %d (production registration and notImplemented allowlist drifted)", got, want)
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
