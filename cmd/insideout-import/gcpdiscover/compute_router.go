package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_router.
//
// Cloud Asset Inventory: compute.googleapis.com/Router
// Asset name shape:      //compute.googleapis.com/projects/<proj>/regions/<r>/routers/<name>
// Terraform import ID:   projects/<proj>/regions/<r>/routers/<name>
//
// Routers are regional (Identity.Location = region) and don't carry
// GCP labels. ScopeStyleNamePrefix per the CLAUDE.md convention.

const (
	computeRouterTFType    = "google_compute_router"
	computeRouterAssetType = "compute.googleapis.com/Router"
)

type computeRouterDiscoverer struct{}

func newComputeRouterDiscoverer() Discoverer { return &computeRouterDiscoverer{} }

func (computeRouterDiscoverer) ResourceType() string   { return computeRouterTFType }
func (computeRouterDiscoverer) AssetType() string      { return computeRouterAssetType }
func (computeRouterDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeRouterDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	region := a.Location
	if region == "" {
		region = regionFromComputeRegionalAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/routers/%s", projectID, region, name)
	return makeImportedResource(book, computeRouterTFType, name, importID, projectID, region, map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/routers/%s", projectID, region, name),
	}, a.Labels)
}

func (computeRouterDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	region, name, err := computeRouterPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/routers/%s", projectID, region, name)
	return makeImportedResource(addressBook{}, computeRouterTFType, name, importID, projectID, region, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/regions/%s/routers/%s", computeAssetHost, projectID, region, name),
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/routers/%s", projectID, region, name),
	}, nil), nil
}

func computeRouterPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("compute_router: empty id: %w", ErrNotSupported)
	}
	region, name := parseRegionAndTrailing(id, "/routers/")
	if region == "" || name == "" {
		return "", "", fmt.Errorf("compute_router: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return region, name, nil
}

// regionFromComputeRegionalAssetName extracts /regions/<r>/ from a
// Compute asset path. Returns "" if missing — caller's responsibility
// to handle that. The path can also carry /zones/<z>/ for zonal
// resources; the caller picks the right marker.
func regionFromComputeRegionalAssetName(assetName string) string {
	return segmentAfter(assetName, "/regions/")
}

// segmentAfter returns the segment immediately following `marker`,
// stopping at the next '/'. Returns "" if marker is absent.
func segmentAfter(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	rest := s[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// parseRegionAndTrailing pulls the region segment and the segment
// after `tail` (e.g. /routers/, /addresses/). Returns ("", "") on
// malformed input. Mirror of parseLocationAndTrailing but keyed on
// /regions/ instead of /locations/.
func parseRegionAndTrailing(s, tail string) (string, string) {
	region := regionFromComputeRegionalAssetName(s)
	if region == "" {
		return "", ""
	}
	idx := strings.Index(s, tail)
	if idx < 0 {
		return "", ""
	}
	rest := s[idx+len(tail):]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", ""
	}
	return region, rest
}
