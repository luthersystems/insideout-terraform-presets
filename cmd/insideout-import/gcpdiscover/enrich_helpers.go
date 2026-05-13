package gcpdiscover

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// stringSliceToValues lifts a []string into the Layer 1 typed shape
// []*Value[string]. Shared across all generated enrichers
// (cmd/enrichgen output) so each .gen.go file doesn't have to redefine
// it. Returns nil for an empty/nil input so the caller can leave the
// destination field unset (omits the corresponding HCL attribute).
func stringSliceToValues(in []string) []*generated.Value[string] {
	if len(in) == 0 {
		return nil
	}
	out := make([]*generated.Value[string], len(in))
	for i, s := range in {
		out[i] = generated.LiteralOf(s)
	}
	return out
}
