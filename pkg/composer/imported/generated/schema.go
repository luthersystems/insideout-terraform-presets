package generated

// ReplacementBehavior describes whether changing an attribute forces
// Terraform to destroy and recreate the containing resource.
//
//   - ReplacementNever — schema marks force_new=false; changes apply in place.
//   - ReplacementAlwaysReplace — schema marks force_new=true; any change
//     destroys and recreates.
//   - ReplacementMayReplace — plan-time-only behavior (e.g. shrinking
//     storage). Static schemas do not expose this state; the codegen never
//     emits it. Runtime callers (Riley's planner) may set it on a copy of
//     the FieldSchema after inspecting a concrete plan.
//   - ReplacementUnknown — the schema does not expose force_new for this
//     attribute (typically nested-block fields in newer provider schemas).
type ReplacementBehavior string

const (
	ReplacementUnknown       ReplacementBehavior = "unknown"
	ReplacementNever         ReplacementBehavior = "never"
	ReplacementMayReplace    ReplacementBehavior = "may_replace"
	ReplacementAlwaysReplace ReplacementBehavior = "always_replace"
)

// FieldSchema captures the per-attribute provider metadata that the
// composer, validators, and Riley's edit path need to make decisions about
// a field without re-reading the original provider schema. One FieldSchema
// per attribute is emitted into the generated <Type>Schema map.
//
// Composer emission rule (per docs/managed-resource-tiers.md lines 167-171):
// emit configurable fields (Required or Optional) from the desired model;
// do not emit Computed=true-only fields.
//
// Sensitivity rule: Sensitive=true defaults to hidden / system-owned /
// redacted diffs unless an explicit Layer 2 field policy overrides.
type FieldSchema struct {
	Required    bool                `json:"required,omitempty"`
	Optional    bool                `json:"optional,omitempty"`
	Computed    bool                `json:"computed,omitempty"`
	Sensitive   bool                `json:"sensitive,omitempty"`
	Replacement ReplacementBehavior `json:"replacement,omitempty"`
}

// Configurable reports whether the composer is allowed to emit this
// attribute when serializing the desired state. Computed-only attributes
// (Required=false, Optional=false, Computed=true) are kept in the model
// for inspection but never emitted.
func (s FieldSchema) Configurable() bool {
	return s.Required || s.Optional
}
