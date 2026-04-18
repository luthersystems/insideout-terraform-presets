package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNew_DefaultsToBundledPresets pins the contract that composer.New()
// with zero options is a fully-usable client: the Client's preset FS
// defaults to the bundle embedded in this repository. Without this test,
// a regression that deletes the default assignment in New() would pass
// the rest of the suite unchallenged because every other test goes
// through newTestClient (which historically passed WithPresets
// explicitly and still works either way).
func TestNew_DefaultsToBundledPresets(t *testing.T) {
	t.Parallel()
	c := New()
	clouds, err := c.ListClouds()
	require.NoError(t, err)
	require.Contains(t, clouds, "aws")
	require.Contains(t, clouds, "gcp")
}
