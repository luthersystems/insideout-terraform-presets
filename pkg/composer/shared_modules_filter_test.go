package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestListPresetKeysForCloud_SkipsSharedBucket pins the issue #203 contract
// that internal helper buckets under aws/_shared/, gcp/_shared/ are NOT
// returned as top-level preset keys. The composer must skip any directory
// whose name begins with `_` so the helper trees never appear as
// `module "<key>" {}` blocks in the composed root.
//
// Mutation coverage: deleting the leading-underscore filter in
// presets.go::ListPresetKeysForCloud or relaxing it to a different sigil
// (e.g. `.shared`) would let aws/_shared and gcp/_shared leak through and
// fail this assertion.
func TestListPresetKeysForCloud_SkipsSharedBucket(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	for _, cloud := range []string{"aws", "gcp"} {
		keys, err := c.ListPresetKeysForCloud(cloud)
		require.NoError(t, err, "ListPresetKeysForCloud(%s)", cloud)

		for _, k := range keys {
			// keys are returned as "<cloud>/<name>"; strip the cloud prefix
			// and ensure the leaf does not begin with `_`.
			name := strings.TrimPrefix(k, cloud+"/")
			require.False(t, strings.HasPrefix(name, "_"),
				"%s leaked an internal/_shared key %q — composer must skip leading-underscore dirs (issue #203)",
				cloud, k)
		}

		// Cardinality floor: the filter must not eat every preset.
		require.Greater(t, len(keys), 0,
			"ListPresetKeysForCloud(%s) returned zero keys; the _shared filter is over-aggressive", cloud)
	}
}

// TestListClouds_SkipsSharedBucket pins that the top-level cross-cloud
// `_shared/` bucket is NOT enumerated as a cloud. Without this guard a
// caller iterating `ListClouds()` would loop over `_shared` and treat its
// helper modules as if they were a third cloud.
func TestListClouds_SkipsSharedBucket(t *testing.T) {
	t.Parallel()

	clouds, err := newTestClient().ListClouds()
	require.NoError(t, err)
	for _, c := range clouds {
		require.False(t, strings.HasPrefix(c, "_"),
			"ListClouds returned an internal bucket %q — composer must skip leading-underscore dirs (issue #203)", c)
	}
	require.Contains(t, clouds, "aws")
	require.Contains(t, clouds, "gcp")
	require.NotContains(t, clouds, "_shared",
		"top-level _shared/ must not appear in ListClouds (issue #203)")
}

// TestSharedSmokeFixturesAreEmbedded asserts the placeholder fixtures
// shipped with the issue #203 plumbing are reachable via the embedded preset
// FS. If a future PR deletes or moves them, the embed glob in zz_embed.go
// would silently fail to compile (Go's embed requires every glob to match
// at least one file) — but if someone adds another file that matches the
// glob and removes the fixtures without updating the embed line, the
// fixtures could disappear without alerting anyone. This test pins their
// presence.
func TestSharedSmokeFixturesAreEmbedded(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	for _, path := range []string{
		"aws/_shared/_smoke",
		"gcp/_shared/_smoke",
		"_shared/_smoke",
	} {
		files, err := c.GetPresetFiles(path)
		require.NoError(t, err, "GetPresetFiles(%s)", path)
		require.NotEmpty(t, files, "%s smoke fixture has no embedded files", path)
		_, hasMain := files["/main.tf"]
		require.True(t, hasMain, "%s smoke fixture missing main.tf", path)
	}
}
