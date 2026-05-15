package main

import (
	"flag"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/bindings"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// capabilityRow is one row of the capabilities matrix. JSON tags use
// lowerCamelCase to match the downstream TS consumer shape verbatim
// (same wire convention as labelEntry).
type capabilityRow struct {
	Discoverable     bool `json:"discoverable"`
	Enrichable       bool `json:"enrichable"`
	DriftDetectable  bool `json:"driftDetectable"`
	MetricsAvailable bool `json:"metricsAvailable"`
	RileyEditable    bool `json:"rileyEditable"`
}

// runCapabilities is the `capabilities` subcommand: emit a per-type
// matrix of the five Capabilities flags consumed by
// pkg/imported.Provider. The matrix is computed from the same upstream
// registries the runtime Provider impls dispatch against (registry +
// awsdiscover/gcpdiscover enricher maps + policy registry + bindings
// registry) so codegen output stays in lockstep with runtime behavior
// without a separate manual maintenance burden.
//
// Default destination is stdout; --output <path> writes to a file.
func runCapabilities(args []string) int {
	fs := flag.NewFlagSet("capabilities", flag.ExitOnError)
	out := fs.String("output", "", "path to write JSON to (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return writeJSONOutput(*out, buildCapabilitiesMap())
}

// buildCapabilitiesMap is the pure-data half of runCapabilities. Exposed
// for unit tests so they can assert on the matrix without going through
// the CLI surface.
//
// Construction note: NewAWSDiscoverer(aws.Config{}) /
// NewGCPDiscoverer(nil, "", GCPDiscovererOpts{}) only allocate per-type
// discoverer / enricher structs and the byTypeEnricher map. No SDK calls
// fire and no network is touched — this is the same pattern the
// awsdiscover.TestExistingEnrichersDoNotImplementByID test uses.
func buildCapabilitiesMap() map[string]capabilityRow {
	awsTypes := registry.SupportedDiscoverTypes(registry.ProviderAWS)
	gcpTypes := registry.SupportedDiscoverTypes(registry.ProviderGCP)

	awsDisc := awsdiscover.NewAWSDiscoverer(aws.Config{})
	gcpDisc := gcpdiscover.NewGCPDiscoverer(nil, "", gcpdiscover.GCPDiscovererOpts{})

	awsEnrichable := stringSet(awsDisc.EnricherTypes())
	gcpEnrichable := stringSet(gcpDisc.EnricherTypes())

	all := unionDiscoverTypes()
	out := make(map[string]capabilityRow, len(all))

	awsSet := stringSet(awsTypes)
	gcpSet := stringSet(gcpTypes)

	for _, t := range all {
		row := capabilityRow{
			Discoverable:     contains(awsSet, t) || contains(gcpSet, t),
			Enrichable:       contains(awsEnrichable, t) || contains(gcpEnrichable, t),
			DriftDetectable:  hasDriftSemantic(t),
			MetricsAvailable: hasMetricsBinding(t),
			RileyEditable:    isRileyEditable(t),
		}
		out[t] = row
	}
	return out
}

// hasDriftSemantic reports whether the curated policy.Map for tfType
// (if any) contains at least one entry with a non-empty DriftSemantic
// axis. Per the comparator contract in pkg/composer/imported/policy/
// axes.go, empty DriftSemantic == "no drift comparison for this field";
// a type is DriftDetectable when at least one curated field opts in.
func hasDriftSemantic(tfType string) bool {
	m, ok := policy.Lookup(tfType)
	if !ok {
		return false
	}
	for _, fp := range m {
		if fp.DriftSemantic != "" {
			return true
		}
	}
	return false
}

// hasMetricsBinding reports whether the bindings registry has an
// entry for tfType. Mirrors the pkg/imported.Provider.MetricsBinding
// contract: a registered binding (even one with empty DefaultMetrics)
// is "metrics available — use consumer defaults"; an absent entry is
// "no metrics surface".
func hasMetricsBinding(tfType string) bool {
	_, ok := bindings.Binding(tfType)
	return ok
}

// isRileyEditable reports whether at least one field in the curated
// policy.Map for tfType has Edit == EditChatSafe or EditRequiresApproval.
// These are the two Edit policies that route through Riley's write
// path; the other three (Never, RelationshipOnly, SystemOnly) all
// disallow direct Riley scalar edits.
func isRileyEditable(tfType string) bool {
	m, ok := policy.Lookup(tfType)
	if !ok {
		return false
	}
	for _, fp := range m {
		switch fp.Edit {
		case policy.EditChatSafe, policy.EditRequiresApproval:
			return true
		}
	}
	return false
}

// stringSet builds a set from a slice for O(1) membership checks.
// Used in buildCapabilitiesMap to avoid quadratic scans across the
// AWS/GCP supported-types lists.
func stringSet(ss []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		out[s] = struct{}{}
	}
	return out
}

func contains(set map[string]struct{}, s string) bool {
	_, ok := set[s]
	return ok
}
