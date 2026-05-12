package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_forwarding_rule.
//
// Cloud Asset Inventory: compute.googleapis.com/ForwardingRule
// Asset name shape:      //compute.googleapis.com/projects/<proj>/regions/<r>/forwardingRules/<name>
// Terraform import ID:   projects/<proj>/regions/<r>/forwardingRules/<name>
//
// Regional forwarding rules carry labels per the provider schema →
// ScopeStyleLabels. Global forwarding rules use the parallel TF type
// google_compute_global_forwarding_rule (#384), processed by the
// companion discoverer via the inverse filter against the same shared
// compute.googleapis.com/ForwardingRule asset bucket.

const (
	computeForwardingRuleTFType    = "google_compute_forwarding_rule"
	computeForwardingRuleAssetType = "compute.googleapis.com/ForwardingRule"
)

type computeForwardingRuleDiscoverer struct{}

func newComputeForwardingRuleDiscoverer() Discoverer { return &computeForwardingRuleDiscoverer{} }

func (computeForwardingRuleDiscoverer) ResourceType() string   { return computeForwardingRuleTFType }
func (computeForwardingRuleDiscoverer) AssetType() string      { return computeForwardingRuleAssetType }
func (computeForwardingRuleDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (computeForwardingRuleDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	// Global rows are processed by computeGlobalForwardingRuleDiscoverer
	// (#384) via the same shared compute.googleapis.com/ForwardingRule
	// asset bucket.
	if isGlobalAddressOrForwardingRule(a) {
		return imported.ImportedResource{}
	}
	name := shortName(a.Name)
	region := a.Location
	if region == "" {
		region = regionFromComputeRegionalAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/forwardingRules/%s", projectID, region, name)
	return makeImportedResource(book, computeForwardingRuleTFType, name, importID, projectID, region, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeForwardingRuleDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_forwarding_rule: empty id: %w", ErrNotSupported)
	}
	region, name := parseRegionAndTrailing(id, "/forwardingRules/")
	if region == "" || name == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_forwarding_rule: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/forwardingRules/%s", projectID, region, name)
	return makeImportedResource(addressBook{}, computeForwardingRuleTFType, name, importID, projectID, region, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/regions/%s/forwardingRules/%s", computeAssetHost, projectID, region, name),
	}, nil), nil
}
