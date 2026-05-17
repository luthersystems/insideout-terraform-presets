package gcpdiscover

import (
	"errors"
	"net/http"

	"google.golang.org/api/googleapi"

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

// isComputeNotFound reports whether err is a googleapi.Error with HTTP
// 404. The compute API returns a structured *googleapi.Error on every
// REST call; treating that as the not-found signal keeps the
// EnrichByID contract precise (ErrNotFound is reserved for confirmed
// absence — any other 4xx / 5xx falls through to a wrapped error).
//
// Shared by every per-type compute enricher (compute_instance,
// compute_firewall, compute_network, compute_router). Originally
// defined on compute_address_enrich.go; lifted here when #581 retired
// that file so the surviving enrichers don't carry a dangling
// reference.
func isComputeNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}
