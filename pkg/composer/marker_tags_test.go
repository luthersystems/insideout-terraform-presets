package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMarkerTagKeyValues pins the literal values of the exported provenance
// keys. Downstream consumers (reliable2, ui-core) depend on these exact
// strings to classify plan diffs as expected-provenance writes vs. real
// drift — see issue #679. Any rename here is a breaking change for those
// callers and must be coordinated.
func TestMarkerTagKeyValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"AWSTagKeyImportProject", AWSTagKeyImportProject, "InsideOutImportProject"},
		{"AWSTagKeyImportSession", AWSTagKeyImportSession, "InsideOutImportSession"},
		{"AWSTagKeyImported", AWSTagKeyImported, "InsideOutImported"},
		{"AWSTagKeyImportedAt", AWSTagKeyImportedAt, "InsideOutImportedAt"},
		{"GCPLabelKeyImportProject", GCPLabelKeyImportProject, "insideout-import-project"},
		{"GCPLabelKeyImportSession", GCPLabelKeyImportSession, "insideout-import-session"},
		{"GCPLabelKeyImported", GCPLabelKeyImported, "insideout-imported"},
		{"GCPLabelKeyImportedAt", GCPLabelKeyImportedAt, "insideout-imported-at"},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, tc.got, "marker key %s drifted from its pinned literal", tc.name)
	}
}
