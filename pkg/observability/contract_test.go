package observability

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
)

// TestObservabilityCoversEveryAWSKey ensures every AWS-backed
// ComponentKey in composer.AllComponentKeys has an entry in Observability.
// Mirrors pkg/composer/iam_actions_test.go::TestAWSIAMActions_CoverAllAWSKeys.
//
// New AWS components added without an Observability entry break this
// test loudly — without it the metric-fetch and per-component alarm
// authoring would silently skip the new component.
func TestObservabilityCoversEveryAWSKey(t *testing.T) {
	for _, k := range composer.AllComponentKeys {
		if composer.CloudFor(k) != "aws" {
			continue
		}
		_, ok := Observability[k]
		assert.True(t, ok,
			"Observability is missing %s — metric-fetch and alarm authoring will silently skip it; add an entry (zero-value ComponentObservability{} is permitted, paired with an observabilityDeferred row) in pkg/observability/component_observability.go",
			k)
	}
}

// TestObservabilityCoversEveryGCPKey is the GCP analog.
func TestObservabilityCoversEveryGCPKey(t *testing.T) {
	for _, k := range composer.AllComponentKeys {
		if composer.CloudFor(k) != "gcp" {
			continue
		}
		_, ok := Observability[k]
		assert.True(t, ok,
			"Observability is missing %s — metric-fetch and alarm authoring will silently skip it; add an entry (zero-value ComponentObservability{} is permitted, paired with an observabilityDeferred row) in pkg/observability/component_observability.go",
			k)
	}
}

// TestObservabilityNoUnknownKeys ensures every key in Observability
// resolves to a known ComponentKey in composer.AllComponentKeys. Catches
// typos and stale entries left after a key rename or removal. Mirrors
// pkg/composer/iam_actions_test.go::TestAWSIAMActions_NoUnknownKeys.
func TestObservabilityNoUnknownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range Observability {
		assert.True(t, known[k],
			"Observability[%s] is not in composer.AllComponentKeys — stale or typo'd key; remove or fix in pkg/observability/component_observability.go",
			k)
	}
}

// TestObservabilityDeferred_AllHaveIssueRef ensures every deferred entry
// carries a non-empty issue ref so reviewers can tell "deliberately
// deferred" from "forgot to seed". Empty values are how stubs sneak in.
func TestObservabilityDeferred_AllHaveIssueRef(t *testing.T) {
	for k, ref := range observabilityDeferred {
		assert.NotEmpty(t, ref,
			"observabilityDeferred[%s] has empty issue ref — every deferred entry must justify itself with a #N reference",
			k)
	}
}

// TestObservabilityDeferred_OnlyKnownKeys ensures every deferred entry
// resolves to a known ComponentKey. Catches typos in the deferred list
// after a key rename or removal.
func TestObservabilityDeferred_OnlyKnownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range observabilityDeferred {
		assert.True(t, known[k],
			"observabilityDeferred[%s] is not in composer.AllComponentKeys — stale or typo'd key; remove or fix in pkg/observability/component_observability.go",
			k)
	}
}

// TestObservabilityDeferred_OnlyForEmptyEntries ensures the deferred
// allowlist is not used to silence drift on entries that already have
// real data. An entry with non-empty Service / non-nil AWS / non-nil GCP
// is "live" — it must not appear in observabilityDeferred. This keeps
// the deferred list shrinking monotonically as data lands across PRs.
func TestObservabilityDeferred_OnlyForEmptyEntries(t *testing.T) {
	for k := range observabilityDeferred {
		o, ok := Observability[k]
		if !ok {
			continue // covered by TestObservabilityDeferred_OnlyKnownKeys
		}
		isEmpty := o.Service == "" && o.AWS == nil && o.GCP == nil
		assert.True(t, isEmpty,
			"observabilityDeferred[%s] is set but Observability[%s] already has live data (Service=%q AWS=%v GCP=%v) — remove the deferred entry once data lands",
			k, k, o.Service, o.AWS != nil, o.GCP != nil)
	}
}

// TestLookup_KnownKey verifies Lookup returns the same record as direct
// map access for a known key.
func TestLookup_KnownKey(t *testing.T) {
	o, ok := Lookup(composer.KeyAWSEC2)
	assert.True(t, ok, "Lookup(KeyAWSEC2) should report ok=true")
	want := Observability[composer.KeyAWSEC2]
	assert.Equal(t, want, o, "Lookup result should match direct map access")
}

// TestLookup_UnknownKey verifies Lookup returns a zero record + false
// for an unknown key. Mirrors the forward-compat shape of
// pkg/composer/iam_actions.go::RequiredAWSIAMActions, which silently
// ignores unknown keys.
func TestLookup_UnknownKey(t *testing.T) {
	const phantom composer.ComponentKey = "aws_does_not_exist_yet"
	o, ok := Lookup(phantom)
	assert.False(t, ok, "Lookup of unknown key should report ok=false")
	assert.Equal(t, ComponentObservability{}, o,
		"Lookup of unknown key should return zero-value record")
}

// TestServicesForKeys_StableOrder verifies output is sorted, so test
// snapshots compare cleanly across runs.
func TestServicesForKeys_StableOrder(t *testing.T) {
	got := ServicesForKeys([]composer.ComponentKey{
		composer.KeyAWSS3, composer.KeyAWSVPC, composer.KeyAWSKMS, composer.KeyAWSALB,
	})
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1], got[i],
			"ServicesForKeys output must be sorted; %q > %q at index %d (full: %v)",
			got[i-1], got[i], i-1, got)
	}
}

// TestServicesForKeys_DedupsAcrossComponents verifies that two component
// keys mapping to the same service collapse to a single output entry.
// (Effective once C2 fills in Service fields; until then the output is
// empty and the test trivially holds.)
func TestServicesForKeys_DedupsAcrossComponents(t *testing.T) {
	got := ServicesForKeys([]composer.ComponentKey{
		composer.KeyAWSEC2, composer.KeyAWSEC2, composer.KeyAWSBastion,
	})
	seen := make(map[string]int, len(got))
	for _, s := range got {
		seen[s]++
	}
	for s, n := range seen {
		assert.Equal(t, 1, n,
			"service %q should appear exactly once in ServicesForKeys output, got %d (full: %v)",
			s, n, got)
	}
}

// TestServicesForKeys_UnknownComponentIgnored verifies forward-compat:
// a future composer release introducing a new ComponentKey shouldn't
// break callers passing that key here.
func TestServicesForKeys_UnknownComponentIgnored(t *testing.T) {
	const phantom composer.ComponentKey = "aws_does_not_exist_yet"
	got := ServicesForKeys([]composer.ComponentKey{phantom, composer.KeyAWSEC2})
	// Should not panic; should silently drop the unknown key and return
	// whatever known keys produce. Empty result is acceptable while C2
	// hasn't seeded Service fields yet.
	_ = got
}

// allComponentKeysSet returns composer.AllComponentKeys as a presence
// map for the reverse-direction drift gates. Mirrors the helper in
// pkg/composer/iam_actions_test.go.
func allComponentKeysSet() map[composer.ComponentKey]bool {
	out := make(map[composer.ComponentKey]bool, len(composer.AllComponentKeys))
	for _, k := range composer.AllComponentKeys {
		out[k] = true
	}
	return out
}
