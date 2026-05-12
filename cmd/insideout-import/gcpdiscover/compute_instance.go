package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_instance.
//
// Cloud Asset Inventory: compute.googleapis.com/Instance
// Asset name shape:      //compute.googleapis.com/projects/<proj>/zones/<z>/instances/<name>
// Terraform import ID:   projects/<proj>/zones/<z>/instances/<name>
//
// Instances are zonal (Identity.Location = zone) and DO carry labels
// per the provider schema, so ScopeStyleLabels. The diagramtest stack
// has zero VMs as of the 2026-05 survey — unit tests pin behavior;
// live smoke is deferred until a richer test fixture stack exists.

const (
	computeInstanceTFType    = "google_compute_instance"
	computeInstanceAssetType = "compute.googleapis.com/Instance"
)

type computeInstanceDiscoverer struct{}

func newComputeInstanceDiscoverer() Discoverer { return &computeInstanceDiscoverer{} }

func (computeInstanceDiscoverer) ResourceType() string   { return computeInstanceTFType }
func (computeInstanceDiscoverer) AssetType() string      { return computeInstanceAssetType }
func (computeInstanceDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (computeInstanceDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	zone := a.Location
	if zone == "" {
		zone = zoneFromComputeZonalAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/zones/%s/instances/%s", projectID, zone, name)
	return makeImportedResource(book, computeInstanceTFType, name, importID, projectID, zone, map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s", projectID, zone, name),
	}, a.Labels)
}

func (computeInstanceDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	zone, name, err := computeInstancePartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/zones/%s/instances/%s", projectID, zone, name)
	return makeImportedResource(addressBook{}, computeInstanceTFType, name, importID, projectID, zone, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/zones/%s/instances/%s", computeAssetHost, projectID, zone, name),
		"self_link":  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s", projectID, zone, name),
	}, nil), nil
}

// computeInstancePartsFromID accepts a Cloud Asset full resource name
// or the projects/<p>/zones/<z>/instances/<n> Terraform import-ID
// form. Bare names are NOT accepted — the import requires the zone.
func computeInstancePartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("compute_instance: empty id: %w", ErrNotSupported)
	}
	zone, name := parseZoneAndTrailing(id, "/instances/")
	if zone == "" || name == "" {
		return "", "", fmt.Errorf("compute_instance: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return zone, name, nil
}

// zoneFromComputeZonalAssetName extracts /zones/<z>/ from a Compute
// asset path. Mirrors regionFromComputeRegionalAssetName but for the
// zonal-resource path shape (instances, disks, persistent IPs, etc.).
func zoneFromComputeZonalAssetName(assetName string) string {
	return segmentAfter(assetName, "/zones/")
}

// parseZoneAndTrailing is the zonal sibling of parseRegionAndTrailing
// — same shape, swapping /regions/ for /zones/.
func parseZoneAndTrailing(s, tail string) (string, string) {
	zone := zoneFromComputeZonalAssetName(s)
	if zone == "" {
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
	return zone, rest
}
