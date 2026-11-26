package registry_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// TestEveryDiscoverableTypeHasTypedModel pins that reverse import can write
// typed Attrs for every type it can discover. genconfig writes the legacy
// Attributes map only for a type with no generated model (the plan backfill
// never writes it), so a discoverable type without one would keep the
// deprecated field alive
// (luthersystems/insideout-terraform-presets#921). A type that fails here
// needs adding to the codegen input (registry.AWSCodegenTypes or
// GoogleCodegenTypes), then `make refresh-schemas gen-imported`.
func TestEveryDiscoverableTypeHasTypedModel(t *testing.T) {
	t.Parallel()
	for _, provider := range registry.SupportedProviders() {
		for _, tfType := range registry.SupportedDiscoverTypes(provider) {
			_, _, ok := generated.Lookup(tfType)
			require.Truef(t, ok, "%s type %q is discoverable but has no typed model in pkg/composer/imported/generated", provider, tfType)
		}
	}
}

// TestEveryDiscoverableTypeIsCodegenInput pins that the codegen input covers
// every discoverable type, so a regen refreshes each model instead of leaving
// a stale file behind (the three AWS types the old awsDiscoverButNotCodegen
// list skipped had models frozen at an older provider version).
func TestEveryDiscoverableTypeIsCodegenInput(t *testing.T) {
	t.Parallel()
	inputs := map[string]bool{}
	for _, list := range [][]string{registry.AWSCodegenTypes(), registry.GoogleCodegenTypes(), registry.GoogleBetaCodegenTypes()} {
		for _, tfType := range list {
			inputs[tfType] = true
		}
	}
	for _, provider := range registry.SupportedProviders() {
		for _, tfType := range registry.SupportedDiscoverTypes(provider) {
			require.Truef(t, inputs[tfType], "%s type %q is discoverable but not a codegen input", provider, tfType)
		}
	}
}
