package labels

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tfregistry "github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// reseed wipes the live registry and re-applies the package's seeded
// overrides from seededLabels. Necessary because other tests in this
// package call resetForTest, which empties the registry mid-suite —
// so we cannot rely on init()'s state surviving when this test runs.
func reseed(t *testing.T) {
	t.Helper()
	regMu.Lock()
	registry = map[string]entry{}
	for tfType, e := range seededLabels {
		registry[tfType] = e
	}
	regMu.Unlock()
}

// TestSeededLabels_BasicShape pins that every seeded entry has both
// Label and IconKey set (i.e. the curated entries fully specify both
// sides rather than relying on the default-rule fallthrough for one
// side). Curated entries with one empty side should be intentional and
// added to a documented allowlist; we don't have any today.
func TestSeededLabels_BasicShape(t *testing.T) {
	reseed(t)
	require.GreaterOrEqual(t, len(RegisteredTypes()), 30,
		"expected at least 30 curated entries; got %d", len(RegisteredTypes()))
	for tfType, e := range seededLabels {
		tfType, e := tfType, e
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, e.Label, "%s: curated Label empty (use the default rule by omitting the entry instead)", tfType)
			assert.NotEmpty(t, e.IconKey, "%s: curated IconKey empty (use the default rule by omitting the entry instead)", tfType)
		})
	}
}

// TestRegistryCoverage_EveryKnownTypeResolves pins the contract that
// every TF type in the canonical registry resolves to a non-empty
// Label and IconKey — either via a curated override here or the
// default rule. Catches the bundle-12-style regression where a new
// type lands in the registry but no upstream surface has a label,
// breaking downstream UI rendering.
func TestRegistryCoverage_EveryKnownTypeResolves(t *testing.T) {
	reseed(t)
	types := tfregistry.KnownTypes()
	require.NotEmpty(t, types, "tfregistry.KnownTypes() returned empty; cannot assert coverage")
	for _, tfType := range types {
		tfType := tfType
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, Label(tfType), "Label(%q) is empty", tfType)
			assert.NotEmpty(t, IconKey(tfType), "IconKey(%q) is empty", tfType)
		})
	}
}

// TestSeededLabels_AllTypesInRegistry pins that every curated entry
// targets a tfType that actually exists in the canonical registry. A
// curated entry for a stale or typo'd type silently has no effect on
// downstream output, so we want a hard failure at unit-test time.
func TestSeededLabels_AllTypesInRegistry(t *testing.T) {
	known := map[string]struct{}{}
	for _, t := range tfregistry.KnownTypes() {
		known[t] = struct{}{}
	}
	for tfType := range seededLabels {
		tfType := tfType
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			_, ok := known[tfType]
			assert.True(t, ok, "%s: curated in seededLabels but not in tfregistry.KnownTypes() — typo or stale entry", tfType)
		})
	}
}
