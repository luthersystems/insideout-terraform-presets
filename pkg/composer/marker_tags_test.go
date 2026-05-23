package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMarkerTagKeyValues_PinnedLiterals pins the literal values of the
// exported provenance keys (and the unexported marker value). Downstream
// consumers (reliable2, ui-core) depend on these exact strings to classify
// plan diffs as expected-provenance writes vs. real drift — see issue #679.
// Any rename here is a breaking change for those callers and must be
// coordinated.
//
// markerValueTrue is unexported but still crosses the repo boundary via
// the emitted Terraform — downstream classifiers compare against `"true"`
// — so it is pinned here too.
func TestMarkerTagKeyValues_PinnedLiterals(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "InsideOutImportProject", AWSTagKeyImportProject)
	assert.Equal(t, "InsideOutImportSession", AWSTagKeyImportSession)
	assert.Equal(t, "InsideOutImported", AWSTagKeyImported)
	assert.Equal(t, "InsideOutImportedAt", AWSTagKeyImportedAt)

	assert.Equal(t, "insideout-import-project", GCPLabelKeyImportProject)
	assert.Equal(t, "insideout-import-session", GCPLabelKeyImportSession)
	assert.Equal(t, "insideout-imported", GCPLabelKeyImported)
	assert.Equal(t, "insideout-imported-at", GCPLabelKeyImportedAt)

	assert.Equal(t, "true", markerValueTrue)
}
