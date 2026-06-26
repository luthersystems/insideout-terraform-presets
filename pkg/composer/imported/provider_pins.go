package imported

import "strings"

// Provider version pinning for composed / import Terraform archives (#786).
//
// Composed archives historically constrained hashicorp/aws to an open
// `>= 6.0` range with no lock file, so every customer deploy resolved the
// "newest available at run time". When HashiCorp shipped 6.50.0 mid-day the
// same project flipped from green to red within an hour (the 6.49→6.50
// cross-region S3 import breakage). To make deploys deterministic AND to hit
// the Terraform provider plugin cache baked into the luthersystems/mars
// container (a filesystem_mirror over an exact per-version directory), every
// archive emitter pins the cloud's BASE provider to the SAME exact version
// mars bakes — so `terraform init` symlinks from the mirror instead of
// downloading.
//
// This map is the SINGLE SOURCE OF TRUTH for those base-provider pins. It is
// consumed by:
//   - pkg/composer (composed deploy archive /providers.tf),
//   - pkg/reverseimport (reverse-import combined-stack providers.tf),
//   - cmd/insideout-import/genconfig (generate-config readback stack).
//
// The versions MUST equal the luthersystems/mars provider-mirror bake
// (mars/Dockerfile `AWS_PROVIDER_VERSION` / `GOOGLE_PROVIDER_VERSION`). As of
// mars v0.125.0 that bake is aws 6.52.0 and google/google-beta 6.10.0. Bump
// these AND the mars bake together; the cross-emitter drift guards
// (TestBaseProviderPins_* and the per-emitter pin tests) fail if either side
// drifts back to an open range. Note this is intentionally DISTINCT from
// generated.AWSProviderVersion (the codegen schema version), which tracks
// schemas/providers.tf and need not equal the runtime deploy pin.
var baseProviderPins = map[string]map[string]string{
	"aws": {
		"aws": "= 6.52.0",
	},
	"gcp": {
		"google":      "= 6.10.0",
		"google-beta": "= 6.10.0",
	},
}

// BaseProviderPin returns the exact version constraint (e.g. "= 6.52.0") this
// repo pins for a cloud's base Terraform provider, matching the
// luthersystems/mars provider-mirror bake so terraform init hits the cache.
// cloud is "aws" or "gcp"; provider is the required_providers key ("aws",
// "google", "google-beta"). Returns "" for an unknown cloud/provider pair so
// callers fall back to whatever default they had — but every known base
// provider is covered, so a "" return at a wired call site is a bug the
// emitter tests catch.
func BaseProviderPin(cloud, provider string) string {
	return baseProviderPins[strings.ToLower(strings.TrimSpace(cloud))][strings.ToLower(strings.TrimSpace(provider))]
}

// BaseProviderPins returns a copy of the pin map for a cloud ("aws"/"gcp"),
// so callers that re-assert every base provider after a discovered-provider
// union (pkg/composer) can iterate without mutating the source of truth.
// Returns nil for an unknown cloud.
func BaseProviderPins(cloud string) map[string]string {
	src := baseProviderPins[strings.ToLower(strings.TrimSpace(cloud))]
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// AllBaseProviderPins returns a flattened provider-name → exact-constraint map
// across every cloud (aws, gcp), e.g. {"aws": "= 6.52.0", "google": "= 6.10.0",
// "google-beta": "= 6.10.0"}. The composer's pre-init provider-conflict
// validator seeds these into its constraint union so a preset pinning a range
// incompatible with the exact emitted pin is caught before terraform init —
// for EVERY base provider the emitter pins, including google-beta. Deriving the
// seed from this accessor (rather than a hand-maintained copy) keeps the
// validator faithful to the emitter by construction. Provider names are unique
// across clouds today; if that ever changes this must key by cloud instead.
func AllBaseProviderPins() map[string]string {
	out := map[string]string{}
	for _, byProvider := range baseProviderPins {
		for name, constraint := range byProvider {
			out[name] = constraint
		}
	}
	return out
}
