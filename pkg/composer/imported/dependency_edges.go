package imported

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/dependencies"
)

// dependency_edges.go bridges a discovered []ImportedResource set to the
// enrich-free cross-reference resolver in pkg/imported/dependencies. It
// lets reliable's import wizard compute the picker's "auto-included N
// dependencies" closure from discover-time data alone — no per-resource
// EnrichAttributes describe call and no Attrs (presets#733 /
// reliable#2040).
//
// The edges produced here are the free-form foreign-key edges
// (role → aws_iam_role, kms_key_arn → aws_kms_key, vpc_id → aws_vpc, …)
// declared in dependencies.FieldRefs(). They are the discover-only
// equivalent of reliable's Attrs-based resolveImportedDependencies,
// derived from Identity.NativeIDs that the discoverers lift at discover
// time. They are complementary to the parent/child ParentAddress edges
// stamped by the AWS parent resolver — together the two cover the full
// closure the picker shows today.

// DependencyEdges returns a map keyed by each resource's
// Identity.Address with the sorted, de-duplicated list of sibling
// Addresses its cross-reference FK fields resolve to. Pure: no I/O, no
// Attrs — it reads Identity.NativeIDs only, so it works on a discover-only
// IR set whose Attrs are empty.
//
// This is the parity surface for the discovery fast mode: given a
// discovered set with empty Attrs, the edges returned here match what
// reliable's enriched-Attrs resolver produces today, provided the
// discoverers lifted the FieldRefs() FK fields into NativeIDs (the AWS
// Cloud Control extractors do — see
// cmd/insideout-import/awsdiscover/cloudcontrol_types.go).
//
// Returns nil when no edges resolve.
func DependencyEdges(irs []ImportedResource) map[string][]string {
	if len(irs) == 0 {
		return nil
	}
	sources := make([]dependencies.EdgeSource, 0, len(irs))
	for _, ir := range irs {
		sources = append(sources, dependencies.EdgeSource{
			Address:   ir.Identity.Address,
			Type:      ir.Identity.Type,
			NativeIDs: ir.Identity.NativeIDs,
			ImportID:  ir.Identity.ImportID,
		})
	}
	return dependencies.ResolveFromIdentities(sources)
}
