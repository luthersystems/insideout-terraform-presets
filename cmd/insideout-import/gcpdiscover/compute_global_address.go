package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_global_address.
//
// Cloud Asset Inventory: compute.googleapis.com/Address (same slug as
// the regional sibling — Cloud Asset doesn't separate regional from
// global addresses by asset type, only by path shape).
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/addresses/<name>
// Terraform import ID:   projects/<proj>/global/addresses/<name>
//
// google_compute_global_address resources carry labels per the
// provider schema (the `labels` attribute on the resource block), so
// this discoverer uses ScopeStyleLabels. The shared asset-type slug
// with google_compute_address is intentional — both discoverers
// register the same CAI slug and the search-time bucket is one call,
// not two (see assetTypesOf's dedup in gcpdiscover.go). Per-discoverer
// FromAsset filters select the regional vs global subset.
//
// Companion to compute_address.go (#369, #384). Adding this discoverer
// converts the post-#380 zero-Identity skip on global rows into a
// genuine discovery — pre-PR, the diagramtest2025-09-14 stack's global
// LB IP discovered as 0; post-PR, 1.

const (
	computeGlobalAddressTFType    = "google_compute_global_address"
	computeGlobalAddressAssetType = "compute.googleapis.com/Address"
)

type computeGlobalAddressDiscoverer struct{}

func newComputeGlobalAddressDiscoverer() Discoverer { return &computeGlobalAddressDiscoverer{} }

func (computeGlobalAddressDiscoverer) ResourceType() string   { return computeGlobalAddressTFType }
func (computeGlobalAddressDiscoverer) AssetType() string      { return computeGlobalAddressAssetType }
func (computeGlobalAddressDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (computeGlobalAddressDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	// Inverse of the regional discoverer's filter: keep only global
	// rows. Regional rows are processed by computeAddressDiscoverer
	// and skipped here via the zero-Identity sentinel.
	if !isGlobalAddressOrForwardingRule(a) {
		return imported.ImportedResource{}
	}
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/addresses/%s", projectID, name)
	selfLink := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/addresses/%s", projectID, name)
	// Location is intentionally empty for global addresses (the
	// "global" path segment is not a GCP location/region). Matches
	// google_compute_firewall and other intrinsically-global types.
	return makeImportedResource(book, computeGlobalAddressTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
		"self_link":  selfLink,
	}, a.Labels)
}

func (computeGlobalAddressDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := computeGlobalAddressNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/global/addresses/%s", projectID, name)
	selfLink := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/addresses/%s", projectID, name)
	assetName := fmt.Sprintf("//%s/projects/%s/global/addresses/%s", computeAssetHost, projectID, name)
	return makeImportedResource(addressBook{}, computeGlobalAddressTFType, name, importID, projectID, "", map[string]string{
		"asset_name": assetName,
		"self_link":  selfLink,
	}, nil), nil
}

// computeGlobalAddressNameFromID parses the global shape only.
// Regional inputs are rejected with ErrNotSupported — they belong to
// google_compute_address, the regional sibling. Symmetric with
// FromAsset's filter: keeps the dep-chase code path from emitting a
// `projects/<p>/global/addresses/<n>` import-id for a row whose real
// type is the regional one.
func computeGlobalAddressNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("compute_global_address: empty id: %w", ErrNotSupported)
	}
	_, after, ok := strings.Cut(id, "/global/addresses/")
	if !ok {
		return "", fmt.Errorf("compute_global_address: unrecognized id %q (expected /global/addresses/ shape): %w", id, ErrNotSupported)
	}
	name, _, _ := strings.Cut(after, "/")
	if name == "" {
		return "", fmt.Errorf("compute_global_address: empty name in id %q: %w", id, ErrNotSupported)
	}
	return name, nil
}
