package composer

// Exported InsideOut import-provenance marker tag/label keys.
//
// Every taggable resource adopted by `insideout-import` is stamped with these
// keys on first apply. Downstream consumers (the reliable backend, the
// imported-resource drift classifier, the UI) need the same literals to
// classify plan diffs as "expected provenance write" vs. real drift. The
// canonical source of truth lives here so a rename never has to land in
// multiple repos by grep — see issue #679 for the duplication that prompted
// this export.
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

// markerValueTrue is the canonical value of AWSTagKeyImported /
// GCPLabelKeyImported. Kept unexported because it is the same literal in
// both clouds and downstream classifiers only need to compare against the
// constant `"true"`.
const markerValueTrue = "true"
