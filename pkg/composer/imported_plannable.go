package composer

import (
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// MissingRequiredAttrs reports the schema-Required Terraform arguments that
// discovery failed to capture in ir.Attrs. An empty result means the
// resource is plannable. Unlike ValidateImportedResources, this does NOT
// require ir.Tier to be stamped — it works directly against raw discovery
// output (issue #656). When ir.Identity.Type is not a registered imported
// type, plannability cannot be assessed and an empty slice is returned (so
// callers do not disable rows on an absent signal).
//
// This is the public, durable contract that replaces downstream
// (luthersystems/reliable) reaching into emit-mode classification or
// stamping a synthetic Tier to detect un-plannable resources. The detection
// runs entirely off ir.Identity.Type + ir.Attrs; see
// generated.MissingRequiredAttrs for the per-attribute presence rule.
func MissingRequiredAttrs(ir imported.ImportedResource) []string {
	missing, err := generated.MissingRequiredAttrs(ir.Identity.Type, ir.Attrs)
	if err != nil {
		// Unregistered type: plannability cannot be assessed. Return an
		// empty slice rather than an error so callers treat it as an
		// absent signal — not as "un-plannable" — and do not disable the
		// resource's row on the strength of a missing schema.
		return nil
	}
	return missing
}

// Plannable reports whether every schema-Required Terraform argument for ir
// was captured by discovery. See MissingRequiredAttrs — like it, Plannable
// does NOT require ir.Tier to be stamped. A resource whose type is not a
// registered imported type is reported plannable (the signal is absent, not
// negative).
func Plannable(ir imported.ImportedResource) bool {
	return len(MissingRequiredAttrs(ir)) == 0
}

// UnplannableReason returns a human-readable explanation when ir is not
// plannable, or "" when it is. The wording matches the
// imported_resource_missing_required_attr validation message
// (ValidateImportedEmitReadiness) so downstream UI copy stays consistent
// whether it is sourced from a ValidationIssue or from this Tier-independent
// helper.
func UnplannableReason(ir imported.ImportedResource) string {
	missing := MissingRequiredAttrs(ir)
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"imported %s %q is missing required argument(s) %s; discovery did not capture them and Terraform plan will fail — the resource block is not plannable",
		ir.Identity.Type, ir.Identity.Address, strings.Join(missing, ", "))
}
