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
// `google_compute_global_address` is a separate type (#384). This
// discoverer's FromAsset filters out the global rows; the companion
// google_compute_global_address discoverer in compute_global_address.go
// processes them via the inverse filter against the same shared CAI
// bucket. The two discoverers always co-register (assetTypesOf dedups
// the shared slug at search time).

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
	// Global rows are processed by computeGlobalAddressDiscoverer
	// (#384) via the same shared compute.googleapis.com/Address asset
	// bucket. Returning a zero ImportedResource signals the
	// orchestrator to drop the row from THIS discoverer's emit set —
	// the global discoverer's inverse filter then keeps it.
	if isGlobalAddressOrForwardingRule(a) {
		return imported.ImportedResource{}
	}
	name := shortName(a.Name)
	region := a.Location
	if region == "" {
		region = regionFromComputeRegionalAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/addresses/%s", projectID, region, name)
	selfLink := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/addresses/%s", projectID, region, name)
	return makeImportedResource(book, computeAddressTFType, name, importID, projectID, region, map[string]string{
		"asset_name": a.Name,
		"self_link":  selfLink,
	}, a.Labels)
}

// isGlobalAddressOrForwardingRule reports whether a CAI asset row is
// a global Address/ForwardingRule — the two compute asset slugs whose
// regional and global TF types share the same Cloud Asset type slug
// and so must be split on the discover side.
//
// NOT a general "is this a global compute asset" predicate: many
// compute types (firewalls, URL maps, target proxies) are
// intrinsically global. Their asset paths also contain "/global/",
// but the right behavior for those is to discover, not skip. This
// helper is narrowed to the address+forwarding-rule callers — see
// compute_address.go and compute_forwarding_rule.go.
//
// The asset path's `/global/` marker is authoritative; Cloud Asset's
// Location field is `"global"` for the same rows but it's a
// derivative signal (live-smoke confirmed). Checking the path keeps
// the predicate consistent if a future CAI version stops surfacing
// Location for global rows.
func isGlobalAddressOrForwardingRule(a gcpAssetResult) bool {
	return strings.Contains(a.Name, "/global/")
}

func (computeAddressDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	region, name, err := computeAddressPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/regions/%s/addresses/%s", projectID, region, name)
	selfLink := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/addresses/%s", projectID, region, name)
	assetName := fmt.Sprintf("//%s/projects/%s/regions/%s/addresses/%s", computeAssetHost, projectID, region, name)
	return makeImportedResource(addressBook{}, computeAddressTFType, name, importID, projectID, region, map[string]string{
		"asset_name": assetName,
		"self_link":  selfLink,
	}, nil), nil
}

// computeAddressPartsFromID parses the regional shape only. Globals
// are rejected with ErrNotSupported — they belong to
// google_compute_global_address (#384). Symmetric with FromAsset's
// filter; keeps the dep-chase code path from emitting a malformed
// `projects/<p>/regions/global/...` import-id.
func computeAddressPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("compute_address: empty id: %w", ErrNotSupported)
	}
	if strings.Contains(id, "/global/addresses/") {
		return "", "", fmt.Errorf("compute_address: global address %q belongs to google_compute_global_address: %w", id, ErrNotSupported)
	}
	region, name := parseRegionAndTrailing(id, "/addresses/")
	if region == "" || name == "" {
		return "", "", fmt.Errorf("compute_address: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return region, name, nil
}
