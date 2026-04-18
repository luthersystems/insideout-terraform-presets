package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNew_DefaultsToBundledPresets pins the contract that composer.New()
// with zero options uses the preset bundle embedded in this repository.
// Without this test, a regression that deletes the default assignment in
// New() — or silently rewires it to an unrelated fs.FS that happens to
// expose aws/ and gcp/ roots — would pass the rest of the suite because
// every other test routes through newTestClient.
//
// Mutation coverage: the assertions below fail if New() returns a Client
// whose presets are nil, empty, or missing any of the well-known modules
// that ship in this repo.
func TestNew_DefaultsToBundledPresets(t *testing.T) {
	t.Parallel()

	c := New()

	clouds, err := c.ListClouds()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"aws", "gcp"}, clouds,
		"default FS must expose exactly aws and gcp as top-level clouds")

	awsKeys, err := c.ListPresetKeysForCloud("aws")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(awsKeys), 20,
		"default FS must expose the full AWS preset catalogue (got %d)", len(awsKeys))
	for _, want := range []string{"aws/vpc", "aws/bedrock", "aws/opensearch"} {
		require.Contains(t, awsKeys, want,
			"default FS missing well-known AWS preset %q", want)
	}

	gcpKeys, err := c.ListPresetKeysForCloud("gcp")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(gcpKeys), 15,
		"default FS must expose the full GCP preset catalogue (got %d)", len(gcpKeys))
	for _, want := range []string{"gcp/vpc", "gcp/gke", "gcp/cloudsql"} {
		require.Contains(t, gcpKeys, want,
			"default FS missing well-known GCP preset %q", want)
	}
}
