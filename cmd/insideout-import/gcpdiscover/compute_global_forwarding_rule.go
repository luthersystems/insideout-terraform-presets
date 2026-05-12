package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_global_forwarding_rule.
//
// Cloud Asset Inventory: compute.googleapis.com/ForwardingRule (same
// slug as the regional sibling — Cloud Asset doesn't separate
// regional from global forwarding rules by asset type, only by path
// shape).
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/forwardingRules/<name>
// Terraform import ID:   projects/<proj>/global/forwardingRules/<name>
//
// Global forwarding rules carry labels per the provider schema, so
// this discoverer uses ScopeStyleLabels. The shared asset-type slug
// with google_compute_forwarding_rule is intentional — see
// compute_global_address.go's docblock for the dispatch shape.
//
// Companion to compute_forwarding_rule.go (#375, #384). Adding this
// discoverer converts the post-#380 zero-Identity skip on global rows
// into a genuine discovery.

const (
	computeGlobalForwardingRuleTFType    = "google_compute_global_forwarding_rule"
	computeGlobalForwardingRuleAssetType = "compute.googleapis.com/ForwardingRule"
)

type computeGlobalForwardingRuleDiscoverer struct{}

func newComputeGlobalForwardingRuleDiscoverer() Discoverer {
	return &computeGlobalForwardingRuleDiscoverer{}
}

func (computeGlobalForwardingRuleDiscoverer) ResourceType() string {
	return computeGlobalForwardingRuleTFType
}
func (computeGlobalForwardingRuleDiscoverer) AssetType() string {
	return computeGlobalForwardingRuleAssetType
}
func (computeGlobalForwardingRuleDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (computeGlobalForwardingRuleDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	// Inverse of the regional discoverer's filter: keep only global
	// rows.
	if !isGlobalAddressOrForwardingRule(a) {
		return imported.ImportedResource{}
	}
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/forwardingRules/%s", projectID, name)
	return makeImportedResource(book, computeGlobalForwardingRuleTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeGlobalForwardingRuleDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := computeGlobalForwardingRuleNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/global/forwardingRules/%s", projectID, name)
	return makeImportedResource(addressBook{}, computeGlobalForwardingRuleTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/forwardingRules/%s", computeAssetHost, projectID, name),
	}, nil), nil
}

// computeGlobalForwardingRuleNameFromID parses the global shape only.
// Regional inputs are rejected with ErrNotSupported — they belong to
// google_compute_forwarding_rule, the regional sibling.
func computeGlobalForwardingRuleNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("compute_global_forwarding_rule: empty id: %w", ErrNotSupported)
	}
	_, after, ok := strings.Cut(id, "/global/forwardingRules/")
	if !ok {
		return "", fmt.Errorf("compute_global_forwarding_rule: unrecognized id %q (expected /global/forwardingRules/ shape): %w", id, ErrNotSupported)
	}
	name, _, _ := strings.Cut(after, "/")
	if name == "" {
		return "", fmt.Errorf("compute_global_forwarding_rule: empty name in id %q: %w", id, ErrNotSupported)
	}
	return name, nil
}
