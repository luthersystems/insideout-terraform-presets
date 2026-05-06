package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_network.
//
// Cloud Asset Inventory: compute.googleapis.com/Network
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/networks/<name>
// Terraform import ID:   projects/<proj>/global/networks/<name>
//
// VPC networks are project-global (the asset-name `global` segment is
// part of the path, not a location qualifier). Identity.Location stays
// empty.

const (
	computeNetworkTFType    = "google_compute_network"
	computeNetworkAssetType = "compute.googleapis.com/Network"

	computeAssetHost = "compute.googleapis.com"
)

type computeNetworkDiscoverer struct{}

func newComputeNetworkDiscoverer() Discoverer { return &computeNetworkDiscoverer{} }

func (computeNetworkDiscoverer) ResourceType() string { return computeNetworkTFType }
func (computeNetworkDiscoverer) AssetType() string    { return computeNetworkAssetType }

func (computeNetworkDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/networks/%s", projectID, name)
	return makeImportedResource(book, computeNetworkTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s", projectID, name),
	})
}

func (computeNetworkDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := computeNetworkNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/global/networks/%s", projectID, name)
	return makeImportedResource(addressBook{}, computeNetworkTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/networks/%s", computeAssetHost, projectID, name),
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s", projectID, name),
	}), nil
}

// computeNetworkNameFromID extracts the network name from one of three
// accepted inputs: a Cloud Asset full resource name
// (//compute.googleapis.com/projects/<p>/global/networks/<n>), a
// self-link (https://www.googleapis.com/compute/v1/projects/<p>/global/networks/<n>),
// or the projects/<p>/global/networks/<n> Terraform import-ID form.
// Bare names are accepted as a fallback.
func computeNetworkNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("compute_network: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/global/networks/"); idx >= 0 {
		rest := id[idx+len("/global/networks/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("compute_network: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
