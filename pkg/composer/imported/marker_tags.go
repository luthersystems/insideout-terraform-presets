package imported

// Canonical InsideOut import-provenance marker tag/label keys.
//
// Every taggable resource adopted by `insideout-import` is stamped with these
// keys on first apply. Downstream consumers (the reliable backend, the
// imported-resource drift classifier, the UI) need the same literals to
// classify plan diffs as "expected provenance write" vs. real drift — see
// issue #679 for the duplication that prompted the original export from
// pkg/composer.
//
// This package is the single definition site (reliable#2230 consolidation):
// pkg/composer re-exports these constants for its established public surface,
// and importability.go's un-importability classifier reads the same literals —
// previously it carried a private duplicate set. pkg/composer/imported cannot
// import pkg/composer (cycle), so the canonical home is here, at the bottom of
// the dependency graph.
//
// AWS uses CamelCase tag keys; GCP labels are restricted to lowercase letters,
// digits, `-`, and `_`, so the GCP mirror uses kebab-case.
const (
	// AWSTagKeyImportProject identifies the InsideOut stack/import-project
	// that owns this resource. Required on every adopted AWS resource.
	AWSTagKeyImportProject = "InsideOutImportProject"

	// AWSTagKeyImportSession identifies the specific import session that
	// adopted this resource. Optional — omitted when the caller did not
	// supply a session ID.
	AWSTagKeyImportSession = "InsideOutImportSession"

	// AWSTagKeyImported is the canonical boolean marker stamped on every
	// adopted AWS resource. Its value is always "true".
	AWSTagKeyImported = "InsideOutImported"

	// AWSTagKeyImportedAt is the RFC3339 UTC timestamp recorded when the
	// resource was first adopted.
	AWSTagKeyImportedAt = "InsideOutImportedAt"

	// GCPLabelKeyImportProject is the GCP-label mirror of
	// AWSTagKeyImportProject.
	GCPLabelKeyImportProject = "insideout-import-project"

	// GCPLabelKeyImportSession is the GCP-label mirror of
	// AWSTagKeyImportSession.
	GCPLabelKeyImportSession = "insideout-import-session"

	// GCPLabelKeyImported is the GCP-label mirror of AWSTagKeyImported.
	GCPLabelKeyImported = "insideout-imported"

	// GCPLabelKeyImportedAt is the GCP-label mirror of AWSTagKeyImportedAt.
	// The value is RFC3339 UTC, downcased with `:` and `.` replaced by `-`
	// to satisfy the GCP label charset.
	GCPLabelKeyImportedAt = "insideout-imported-at"
)
