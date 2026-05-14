package main

import (
	"path/filepath"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionDrift_GeneratedMatchesProvidersTF is the drift gate
// between schemas/providers.tf (the committed source of truth for
// provider pins) and pkg/composer/imported/generated/version.gen.go
// (the committed codegen output).
//
// Failure scenarios this catches:
//
//   - Edit schemas/providers.tf (bump aws to 5.71.0) without running
//     `make gen-imported`. version.gen.go still says 5.70.0; the
//     matching subtest flips red.
//   - Edit version.gen.go by hand. Same failure.
//
// `make verify-gen` already gates on a git diff after re-running the
// generator and covers the same regression class — this test is the
// faster-failing in-package signal that names the exact provider
// without shelling out to `make`.
func TestVersionDrift_GeneratedMatchesProvidersTF(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	pins, err := LoadProviderPins(filepath.Join(root, "schemas", "providers.tf"))
	require.NoError(t, err)

	cases := []struct {
		name  string
		pin   string
		genGo string
	}{
		{"aws", pins.AWS, generated.AWSProviderVersion},
		{"google", pins.Google, generated.GoogleProviderVersion},
		{"google-beta", pins.GoogleBeta, generated.GoogleBetaProviderVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equalf(t, tc.pin, tc.genGo,
				"provider %q: schemas/providers.tf pins %q but generated/version.gen.go has %q — run `make gen-imported` and commit",
				tc.name, tc.pin, tc.genGo)
		})
	}
}
