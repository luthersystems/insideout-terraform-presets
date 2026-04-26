package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKnownFieldsNoShrink locks the validator-covered IR field surface
// against silent erosion. Every PR that adds, removes, or renames an entry
// in componentFieldValidators / configFieldValidators must also bump
// pkg/composer/testdata/known_fields.golden — making the surface change
// explicit in the diff. Set UPDATE_GOLDEN=1 when running tests to overwrite
// the fixture intentionally.
func TestKnownFieldsNoShrink(t *testing.T) {
	t.Parallel()

	goldenPath := filepath.Join("testdata", "known_fields.golden")
	current := strings.Join(KnownFields(), "\n") + "\n"

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(current), 0o644))
		t.Logf("wrote golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/composer/ -run TestKnownFieldsNoShrink` to seed it")
	require.Equal(t, string(want), current,
		"KnownFields surface drifted from %s. If this is intentional, re-seed via UPDATE_GOLDEN=1.",
		goldenPath)
}
