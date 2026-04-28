package imported

import (
	"encoding/json"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// ImportedResource is the IR carrier for one cloud resource that lives outside
// the preset module call space — either imported via reverse-Terraform or
// observed by the inspector. It rides alongside composer.Components in the
// stack snapshot. See docs/managed-resource-tiers.md lines 477-500.
//
// Phase 2 stores desired provider attributes in the opaque Attributes bag for
// wire-compatibility with the Phase 1 importer. A future ticket (#145) will
// add a sibling typed Attrs field backed by per-resource-type generated
// structs; the JSON key for that typed shape will differ from "attributes"
// so both can coexist.
type ImportedResource struct {
	// Identity carries the immutable Terraform address and cloud-side
	// correlation identifiers.
	Identity ResourceIdentity `json:"identity"`

	// Tier classifies how InsideOut treats the resource. See Tier consts.
	Tier Tier `json:"tier,omitempty"`

	// Source records who created the resource record (composer | importer
	// | inspector).
	Source Source `json:"source,omitempty"`

	// Attributes is the current desired provider attributes as an opaque
	// bag (Phase 1 / wire-compatible shape). The composer emits HCL from
	// this state. Riley's chat edits update the values here through the
	// model write path; FieldEdits records audit/conflict metadata only —
	// the composer does not emit from FieldEdits.
	Attributes map[string]any `json:"attributes,omitempty"`

	// FieldEdits is audit/conflict metadata for changes made via the model
	// write path since the last successful `terraform apply`. The edited
	// values are already reflected in Attributes; this map exists to detect
	// conflicts between Riley's pending edits and independent cloud changes
	// at re-import time. Cleared when an apply succeeds.
	FieldEdits map[string]FieldEdit `json:"field_edits,omitempty"`

	// GraduationCandidate is populated by the shape-matcher when the
	// imported graph could be wrapped in a preset module call. Phase 3+;
	// see decision #16 in the design doc.
	GraduationCandidate *PresetMatch `json:"graduation_candidate,omitempty"`
}

// FieldEdit is audit/conflict metadata for one model-side edit to a single
// attribute. EditedAt is always serialized as RFC3339Nano UTC regardless of
// the caller-supplied time.Location; see MarshalJSON.
type FieldEdit struct {
	Source   Source    `json:"source,omitempty"`
	EditedAt time.Time `json:"edited_at"`
	OldValue any       `json:"old_value,omitempty"`
	NewValue any       `json:"new_value,omitempty"`
}

// MarshalJSON enforces RFC3339Nano UTC on EditedAt. Without this, a caller
// passing a non-UTC time.Time would serialize with a numeric offset (e.g.
// "-07:00"), which the design contract forbids. The zero value is left
// untouched so the canonical "0001-01-01T00:00:00Z" form is preserved.
func (f FieldEdit) MarshalJSON() ([]byte, error) {
	type alias FieldEdit
	if !f.EditedAt.IsZero() {
		f.EditedAt = f.EditedAt.UTC()
	}
	return json.Marshal(alias(f))
}

// PresetMatch is a forward-declared graduation hint. It is populated by the
// shape-matcher (Phase 3+) when an imported resource could be wrapped in a
// preset module call. Composer never reads this field today.
type PresetMatch struct {
	// PresetKey identifies the candidate preset module.
	PresetKey composer.ComponentKey `json:"preset_key,omitempty"`
	// Confidence is a 0.0-1.0 score from the matcher.
	Confidence float64 `json:"confidence,omitempty"`
	// MovedBlocks are the proposed `moved {}` blocks for promotion.
	MovedBlocks []MovedBlock `json:"moved_blocks,omitempty"`
	// BlockingDeltas are attributes that would have to change to fit the
	// preset shape.
	BlockingDeltas []composer.FieldDiff `json:"blocking_deltas,omitempty"`
}

// MovedBlock is the minimal shape of a Terraform `moved {}` block: a from /
// to address pair. Declared locally because pkg/composer does not yet expose
// an equivalent type (only an unexported test helper). When #147 lands the
// composer-side emission, it can promote this to pkg/composer if useful.
type MovedBlock struct {
	From string `json:"from"`
	To   string `json:"to"`
}
