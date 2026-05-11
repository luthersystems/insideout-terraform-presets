package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_address.
//
// Cloud Asset Inventory: compute.googleapis.com/Address
// Asset name shapes:
//   //compute.googleapis.com/projects/<proj>/regions/<r>/addresses/<name>  (regional)
//   //compute.googleapis.com/projects/<proj>/global/addresses/<name>       (global)
// Terraform import IDs:
//   projects/<proj>/regions/<r>/addresses/<name>                           (regional)
//   projects/<proj>/global/addresses/<name>                                (global)
//
// google_compute_address resources DO carry labels (the provider's
// schema exposes `labels` as a top-level attribute), so this
// discoverer uses ScopeStyleLabels. Per the CLAUDE.md GCP-labels rule,
// composed addresses set `labels = merge({ project = var.project },
// var.labels)`, which means the server-side labels.project clause
// reliably attributes them.
//
// Global vs regional split: CAI returns global addresses under the
// same compute.googleapis.com/Address slug with Location="global", but
// Terraform's `google_compute_address` type is regional-only —
// `google_compute_global_address` is a separate type, not part of
// Bundle 8. This discoverer's FromAsset filters out the global rows
// so they don't get an invalid `projects/<p>/regions/global/...`
// ImportID; they're skipped silently and surface in the unsupported
// stream when --include-unsupported is set.

const (
	computeAddressTFType    = "google_compute_address"
	computeAddressAssetType = "compute.googleapis.com/Address"
)

type computeAddressDiscoverer struct{}

func newComputeAddressDiscoverer() Discoverer { return &computeAddressDiscoverer{} }

func (computeAddressDiscoverer) ResourceType() string   { return computeAddressTFType }
func (computeAddressDiscoverer) AssetType() string      { return computeAddressAssetType }
func (computeAddressDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (computeAddressDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	// Global rows must be skipped — they belong to google_compute_global_address,
	// a separate TF type not in Bundle 8. Returning a zero
	// ImportedResource signals the orchestrator to drop the row (it
	// filters on empty Identity.Type before emitting).
	if isGlobalComputeAsset(a) {
		return imported.ImportedResource{}
	}
	name := shortName(a.Name)
	region := a.Location
	if region == "" {
		region = regionFromComputeRegionalAssetName(a.Name)
	}
	importID := computeAddressImportID(projectID, region, name)
	selfLink := computeAddressSelfLink(projectID, region, name)
	return makeImportedResource(book, computeAddressTFType, name, importID, projectID, region, map[string]string{
		"asset_name": a.Name,
		"self_link":  selfLink,
	}, a.Labels)
}

// isGlobalComputeAsset reports whether a CAI compute asset is a global
// resource. The path's `/global/` marker is the source of truth — the
// Location field is also "global" for these but checking the path
// matches what the asset name encodes.
func isGlobalComputeAsset(a gcpAssetResult) bool {
	return strings.Contains(a.Name, "/global/") || a.Location == "global"
}

func (computeAddressDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	region, name, err := computeAddressPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := computeAddressImportID(projectID, region, name)
	selfLink := computeAddressSelfLink(projectID, region, name)
	assetName := fmt.Sprintf("//%s/projects/%s/global/addresses/%s", computeAssetHost, projectID, name)
	if region != "" {
		assetName = fmt.Sprintf("//%s/projects/%s/regions/%s/addresses/%s", computeAssetHost, projectID, region, name)
	}
	return makeImportedResource(addressBook{}, computeAddressTFType, name, importID, projectID, region, map[string]string{
		"asset_name": assetName,
		"self_link":  selfLink,
	}, nil), nil
}

// computeAddressImportID picks the regional vs global form based on
// whether region is non-empty. Global addresses have no region
// qualifier; the trailing `/regions//` shape produced by an empty
// region would be invalid.
func computeAddressImportID(projectID, region, name string) string {
	if region == "" {
		return fmt.Sprintf("projects/%s/global/addresses/%s", projectID, name)
	}
	return fmt.Sprintf("projects/%s/regions/%s/addresses/%s", projectID, region, name)
}

func computeAddressSelfLink(projectID, region, name string) string {
	if region == "" {
		return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/addresses/%s", projectID, name)
	}
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/addresses/%s", projectID, region, name)
}

// computeAddressPartsFromID accepts regional and global shapes. The
// /global/ marker disambiguates the two; we check it first so a
// global address whose region happens to be "" is parsed correctly.
func computeAddressPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("compute_address: empty id: %w", ErrNotSupported)
	}
	if strings.Contains(id, "/global/addresses/") {
		_, name := parseLocationOrGlobalTrailing(id, "/global/addresses/")
		if name == "" {
			return "", "", fmt.Errorf("compute_address: malformed global id %q: %w", id, ErrNotSupported)
		}
		return "", name, nil
	}
	region, name := parseRegionAndTrailing(id, "/addresses/")
	if region == "" || name == "" {
		return "", "", fmt.Errorf("compute_address: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return region, name, nil
}

// parseLocationOrGlobalTrailing pulls the segment after `tail` (a
// /global/... marker). The location half is always "" by definition
// — global resources have no region/zone. Symmetric helper to
// parseRegionAndTrailing for the global case.
func parseLocationOrGlobalTrailing(s, tail string) (string, string) {
	idx := strings.Index(s, tail)
	if idx < 0 {
		return "", ""
	}
	rest := s[idx+len(tail):]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return "", rest
}
