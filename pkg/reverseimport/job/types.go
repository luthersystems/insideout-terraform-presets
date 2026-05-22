// Package job defines the pure reverse-import job contract shared by
// presets, Mars, UI Core, and Reliable.
//
// Keep this package free of Terraform execution behavior. The runtime engine
// belongs in pkg/reverseimport; this package is the stable JSON surface that
// adapters can pass around without depending on CLI-private structs.
package job

import "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"

// Version is the current reverse-import job contract version.
const Version = 1

// Request is the JSON payload Reliable sends to a reverse-import job.
//
// It contains selected resource identities, not just ARNs. Terraform import
// IDs vary by resource type, and the reverse-import engine needs stable
// Terraform addresses plus cloud scope to render import blocks and bind the
// returned IR.
type Request struct {
	Version   int            `json:"version"`
	Resources []ResourceSpec `json:"resources"`
}

// ResourceSpec is one selected cloud resource that should be reverse-imported.
type ResourceSpec struct {
	Identity imported.ResourceIdentity `json:"identity"`
	Tier     imported.Tier             `json:"tier,omitempty"`
	Source   imported.Source           `json:"source,omitempty"`
}

// Status is the lifecycle status of a reverse-import job result.
type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusPartial   Status = "partial"
)

// Result is the structured output returned by the reverse-import job.
type Result struct {
	Version          int                         `json:"version"`
	Status           Status                      `json:"status"`
	Imported         []imported.ImportedResource `json:"imported,omitempty"`
	PlanSummary      PlanSummary                 `json:"plan_summary"`
	Artifacts        ArtifactSet                 `json:"artifacts,omitempty"`
	Resources        []ResourceResult            `json:"resources,omitempty"`
	Diagnostics      []Diagnostic                `json:"diagnostics,omitempty"`
	ValidationIssues []Issue                     `json:"validation_issues,omitempty"`
}

// PlanSummary is the small plan-count surface Reliable and UI Core need for
// import UX and validation.
//
// ImportCount is intentionally exported. The current CLI can compute this
// internally, but Mars and Reliable need it in the SDK result contract to
// report "N imported, 0 added, 0 changed, 0 destroyed" without scraping logs.
type PlanSummary struct {
	ImportCount  int `json:"import_count"`
	AddCount     int `json:"add_count"`
	ChangeCount  int `json:"change_count"`
	DestroyCount int `json:"destroy_count"`
	ReplaceCount int `json:"replace_count"`
	ReadCount    int `json:"read_count"`
}

// HasNoNonImportChanges reports whether the plan summary is clean aside from
// imports. Callers still need the full plan validator for the authoritative
// contract; this helper is for status display and cheap result checks.
func (s PlanSummary) HasNoNonImportChanges() bool {
	return s.AddCount == 0 &&
		s.ChangeCount == 0 &&
		s.DestroyCount == 0 &&
		s.ReplaceCount == 0
}

// ArtifactSet names the important files produced by a reverse-import job.
type ArtifactSet struct {
	ImportedJSON    *Artifact  `json:"imported_json,omitempty"`
	ImportedTF      *Artifact  `json:"imported_tf,omitempty"`
	ValidateJSON    *Artifact  `json:"validate_json,omitempty"`
	TFPlanJSON      *Artifact  `json:"tfplan_json,omitempty"`
	PlanSummaryJSON *Artifact  `json:"plan_summary_json,omitempty"`
	ResultJSON      *Artifact  `json:"result_json,omitempty"`
	Debug           []Artifact `json:"debug,omitempty"`
}

// Artifact describes one file produced by the job.
type Artifact struct {
	Name      string `json:"name,omitempty"`
	Path      string `json:"path,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// ResourceStatus is the per-resource status in a reverse-import result.
type ResourceStatus string

const (
	ResourceStatusImported ResourceStatus = "imported"
	ResourceStatusSkipped  ResourceStatus = "skipped"
	ResourceStatusFailed   ResourceStatus = "failed"
)

// ResourceResult records per-resource reverse-import details.
type ResourceResult struct {
	Identity     imported.ResourceIdentity   `json:"identity"`
	Status       ResourceStatus              `json:"status"`
	Imported     *imported.ImportedResource  `json:"imported,omitempty"`
	Dependencies []imported.ResourceIdentity `json:"dependencies,omitempty"`
	Fixups       []Fixup                     `json:"fixups,omitempty"`
	Diagnostics  []Diagnostic                `json:"diagnostics,omitempty"`
}

// Fixup describes a provider-specific normalization or repair applied by the
// presets reverse-import engine.
type Fixup struct {
	Code    string   `json:"code"`
	Message string   `json:"message,omitempty"`
	Fields  []string `json:"fields,omitempty"`
}

// Diagnostic is a non-fatal note emitted by the reverse-import job.
type Diagnostic struct {
	Severity string `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

// Issue mirrors the composer validation issue JSON shape without importing
// the full composer package into the pure job contract package.
type Issue struct {
	Field      string   `json:"field"`
	Value      string   `json:"value,omitempty"`
	Allowed    []string `json:"allowed,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
	Code       string   `json:"code"`
	Reason     string   `json:"reason"`
}
