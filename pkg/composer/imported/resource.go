package imported

import (
	"encoding/json"
	"time"
)

// Note on Attrs vs Attributes: ImportedResource carries two attribute
// representations to support a phased migration to the typed Layer 1 model.
// Attributes is the Phase 1 opaque map; Attrs holds the typed shape decoded
// from a generated struct (see pkg/composer/imported/generated). Storing
// Attrs as json.RawMessage keeps this package free of any dependency on the
// generated package and avoids future import cycles. Decoding goes through
// generated.UnmarshalAttrs(id.Type, ir.Attrs).

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

	// Attrs is the typed Layer 1 attribute payload for this resource,
	// stored as raw JSON to keep the carrier free of any dependency on
	// pkg/composer/imported/generated. Decode via
	// generated.UnmarshalAttrs(Identity.Type, Attrs). When both Attributes
	// (opaque map) and Attrs are present, callers should treat Attrs as
	// the authoritative desired state and Attributes as the Phase 1
	// fallback for consumers that have not yet adopted the typed model.
	Attrs json.RawMessage `json:"attrs,omitempty"`

	// GraduationCandidate is populated by the shape-matcher when the
	// imported graph could be wrapped in a preset module call. Phase 3+;
	// see decision #16 in the design doc.
	GraduationCandidate *PresetMatch `json:"graduation_candidate,omitempty"`

	// Remediation is the operator-chosen action for a TierImportedMissing
	// resource. Empty for non-Missing tiers; the composer blocks emission
	// of a TierImportedMissing resource until Remediation is set. See
	// docs/managed-resource-tiers.md "ImportedMissing operator actions".
	Remediation MissingAction `json:"remediation,omitempty"`

	// ForceTakeover is the audited operator override for a provenance
	// conflict — i.e. the composer observed an InsideOutImportProject /
	// insideout-import-project tag on this resource that names a different
	// import project than the current compose pass. Setting this field
	// suppresses imported_resource_provenance_conflict for this resource and
	// instructs the provenance injector to overwrite the existing tag value.
	// All four sub-fields are required. See docs/managed-resource-tiers.md
	// "Provenance tagging policy" decision #45.
	ForceTakeover *ForceTakeover `json:"force_takeover,omitempty"`

	// WeakLocked is set by the provenance injector to true when this
	// resource's Terraform type does not support tags/labels. Mutual
	// exclusion is then a best-effort check based on ResourceIdentity alone
	// (see imported_resource_address_collision); the four-key provenance
	// schema is not emitted. Read-only metadata; callers should not set it.
	WeakLocked bool `json:"weak_locked,omitempty"`
}

// ForceTakeover is the audited operator override for a cross-session
// provenance conflict. Every field is required: a takeover with missing
// metadata is rejected by ValidateProvenanceConflicts as
// imported_resource_force_takeover_invalid. ApprovedAt is always serialized
// as RFC3339Nano UTC regardless of the caller-supplied time.Location;
// see MarshalJSON.
type ForceTakeover struct {
	Actor         string    `json:"actor"`
	Reason        string    `json:"reason"`
	PreviousOwner string    `json:"previous_owner"`
	ApprovedAt    time.Time `json:"approved_at"`
}

// MarshalJSON enforces RFC3339Nano UTC on ApprovedAt. Mirrors FieldEdit's
// MarshalJSON — without this, a caller passing a non-UTC time.Time would
// serialize with a numeric offset (e.g. "-07:00"), which the design contract
// forbids. The zero value is left untouched so the canonical
// "0001-01-01T00:00:00Z" form is preserved.
func (f ForceTakeover) MarshalJSON() ([]byte, error) {
	type alias ForceTakeover
	if !f.ApprovedAt.IsZero() {
		f.ApprovedAt = f.ApprovedAt.UTC()
	}
	return json.Marshal(alias(f))
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
//
// PresetKey and BlockingDeltas use plain types (string and FieldDelta) rather
// than composer.ComponentKey / composer.FieldDiff so this package does not
// import composer. That keeps the dependency direction one-way (composer →
// imported) and lets the composer call into emission/validation helpers
// without import cycles. The JSON shape matches composer.FieldDiff exactly,
// so callers reading or writing JSON across the boundary need no change.
type PresetMatch struct {
	// PresetKey identifies the candidate preset module. Wire format matches
	// composer.ComponentKey (which is type ComponentKey string).
	PresetKey string `json:"preset_key,omitempty"`
	// Confidence is a 0.0-1.0 score from the matcher.
	Confidence float64 `json:"confidence,omitempty"`
	// MovedBlocks are the proposed `moved {}` blocks for promotion.
	MovedBlocks []MovedBlock `json:"moved_blocks,omitempty"`
	// BlockingDeltas are attributes that would have to change to fit the
	// preset shape. JSON shape matches composer.FieldDiff.
	BlockingDeltas []FieldDelta `json:"blocking_deltas,omitempty"`
}

// FieldDelta is a per-attribute before/after pair for graduation diffs. Its
// JSON shape matches composer.FieldDiff so consumers that already read those
// fields by name see no change.
type FieldDelta struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// MovedBlock is the minimal shape of a Terraform `moved {}` block: a from /
// to address pair. Declared locally because pkg/composer does not yet expose
// an equivalent type (only an unexported test helper). When #147 lands the
// composer-side emission, it can promote this to pkg/composer if useful.
type MovedBlock struct {
	From string `json:"from"`
	To   string `json:"to"`
}
