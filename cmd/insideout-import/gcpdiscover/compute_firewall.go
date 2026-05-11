package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_firewall.
//
// Cloud Asset Inventory: compute.googleapis.com/Firewall
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/firewalls/<name>
// Terraform import ID:   projects/<proj>/global/firewalls/<name>
//
// Firewall rules are project-global (the "global" segment is part of
// the path, not a location qualifier) and don't carry GCP labels. The
// CLAUDE.md label-less-resource convention requires the firewall name
// to contain the stack project — name-prefix scoping attributes it.

const (
	computeFirewallTFType    = "google_compute_firewall"
	computeFirewallAssetType = "compute.googleapis.com/Firewall"
)

type computeFirewallDiscoverer struct{}

func newComputeFirewallDiscoverer() Discoverer { return &computeFirewallDiscoverer{} }

func (computeFirewallDiscoverer) ResourceType() string   { return computeFirewallTFType }
func (computeFirewallDiscoverer) AssetType() string      { return computeFirewallAssetType }
func (computeFirewallDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeFirewallDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/firewalls/%s", projectID, name)
	return makeImportedResource(book, computeFirewallTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/firewalls/%s", projectID, name),
	}, a.Labels)
}

func (computeFirewallDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := computeFirewallNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/global/firewalls/%s", projectID, name)
	return makeImportedResource(addressBook{}, computeFirewallTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/firewalls/%s", computeAssetHost, projectID, name),
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/firewalls/%s", projectID, name),
	}, nil), nil
}

// computeFirewallNameFromID extracts the firewall name from one of
// the accepted inputs: Cloud Asset full resource name, self-link, or
// the projects/<p>/global/firewalls/<n> Terraform import-ID form.
// Bare names fall through.
func computeFirewallNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("compute_firewall: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/global/firewalls/"); idx >= 0 {
		rest := id[idx+len("/global/firewalls/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("compute_firewall: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
