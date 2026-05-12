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
	return parseGlobalNameFromID(id, "/global/addresses/", "compute_global_address", "google_compute_address")
}

// parseGlobalNameFromID is the shared parser for the two global
// compute discoverers (#384). The shape is:
//
//   - input may be a Cloud Asset full resource name or a Terraform
//     import-id; either way the global path marker `/global/<collection>/`
//     appears once.
//   - `tfPrefix` (e.g. "compute_global_address") prefixes the error
//     message so the test/log surface names the offending discoverer.
//   - `siblingType` (e.g. "google_compute_address") is named in the
//     unrecognized-id error so an operator who passed a regional
//     shape sees the actionable cross-reference.
//
// Regional inputs surface as "unrecognized id" since the marker is
// absent; the explicit "belongs to siblingType" hint surfaces the
// migration path.
func parseGlobalNameFromID(id, marker, tfPrefix, siblingType string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%s: empty id: %w", tfPrefix, ErrNotSupported)
	}
	_, after, ok := strings.Cut(id, marker)
	if !ok {
		return "", fmt.Errorf("%s: unrecognized id %q (regional inputs belong to %s): %w", tfPrefix, id, siblingType, ErrNotSupported)
	}
	name, _, _ := strings.Cut(after, "/")
	if name == "" {
		return "", fmt.Errorf("%s: empty name in id %q: %w", tfPrefix, id, ErrNotSupported)
	}
	return name, nil
}
