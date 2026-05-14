package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_resource_policy (Bundle G4, #478).
//
// Cloud Asset Inventory: compute.googleapis.com/ResourcePolicy
// Asset name shape:      //compute.googleapis.com/projects/<proj>/regions/<r>/resourcePolicies/<name>
// Terraform import ID:   projects/<proj>/regions/<r>/resourcePolicies/<name>
//
// Resource policies are regional (snapshot schedules, group placement
// policies) and don't carry GCP labels per the provider schema →
// ScopeStyleNamePrefix. Region surfaces on Identity.Location for UI
// grouping; mirror of compute_router / compute_forwarding_rule.

const (
	computeResourcePolicyTFType    = "google_compute_resource_policy"
	computeResourcePolicyAssetType = "compute.googleapis.com/ResourcePolicy"
)

type computeResourcePolicyDiscoverer struct{}

func newComputeResourcePolicyDiscoverer() Discoverer { return &computeResourcePolicyDiscoverer{} }

func (computeResourcePolicyDiscoverer) ResourceType() string   { return computeResourcePolicyTFType }
func (computeResourcePolicyDiscoverer) AssetType() string      { return computeResourcePolicyAssetType }
func (computeResourcePolicyDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeResourcePolicyDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	region := a.Location
	if region == "" {
		region = regionFromComputeRegionalAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/resourcePolicies/%s", projectID, region, name)
	return makeImportedResource(book, computeResourcePolicyTFType, name, importID, projectID, region, map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/resourcePolicies/%s", projectID, region, name),
	}, a.Labels)
}

func (computeResourcePolicyDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_resource_policy: empty id: %w", ErrNotSupported)
	}
	region, name := parseRegionAndTrailing(id, "/resourcePolicies/")
	if region == "" || name == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_resource_policy: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/resourcePolicies/%s", projectID, region, name)
	return makeImportedResource(addressBook{}, computeResourcePolicyTFType, name, importID, projectID, region, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/regions/%s/resourcePolicies/%s", computeAssetHost, projectID, region, name),
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/resourcePolicies/%s", projectID, region, name),
	}, nil), nil
}
