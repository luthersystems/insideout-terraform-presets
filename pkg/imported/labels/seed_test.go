package labels

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	typeregistry "github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// TestEveryKnownTypeHasNonEmptyLabel pins the invariant that every
// type in registry.KnownTypes() — the full union of discoverable and
// codegen-only Terraform resource types — has a non-empty display
// label and icon key. The package's default rule (strip cloud prefix,
// humanize) covers the long tail; curated overrides (Register) win
// where the default produces a worse label.
//
// Rationale: the downstream UI (luthersystems/reliable) renders these
// strings in resource pickers and inventory tables. An empty label
// would render as a blank row with no clue what type it is. The
// invariant turns "forgot to override after renaming a type" or
// "default rule blew up on a new prefix" into a test failure.
//
// This test does NOT require a curated Register entry — most types
// are well-served by the default rule. It only requires the result of
// Label(tfType) / IconKey(tfType) to be non-empty.
func TestEveryKnownTypeHasNonEmptyLabel(t *testing.T) {
	t.Parallel()

	missingLabel := []string{}
	missingIcon := []string{}
	for _, tfType := range typeregistry.KnownTypes() {
		if Label(tfType) == "" {
			missingLabel = append(missingLabel, tfType)
		}
		if IconKey(tfType) == "" {
			missingIcon = append(missingIcon, tfType)
		}
	}
	sort.Strings(missingLabel)
	sort.Strings(missingIcon)

	require.Empty(t, missingLabel,
		"%d known types produced empty Label() — either add a Register override "+
			"or fix the default rule:\n  %s",
		len(missingLabel), strings.Join(missingLabel, "\n  "))
	require.Empty(t, missingIcon,
		"%d known types produced empty IconKey() — either add a Register override "+
			"or fix the default rule:\n  %s",
		len(missingIcon), strings.Join(missingIcon, "\n  "))
}
