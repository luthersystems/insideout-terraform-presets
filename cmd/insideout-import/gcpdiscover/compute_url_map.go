package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_url_map.
//
// Cloud Asset Inventory: compute.googleapis.com/UrlMap
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/urlMaps/<name>
// Terraform import ID:   projects/<proj>/global/urlMaps/<name>
//
// URL maps are global and don't carry labels per the provider
// schema → ScopeStyleNamePrefix.

const (
	computeURLMapTFType    = "google_compute_url_map"
	computeURLMapAssetType = "compute.googleapis.com/UrlMap"
)

type computeURLMapDiscoverer struct{}

func newComputeURLMapDiscoverer() Discoverer { return &computeURLMapDiscoverer{} }

func (computeURLMapDiscoverer) ResourceType() string   { return computeURLMapTFType }
func (computeURLMapDiscoverer) AssetType() string      { return computeURLMapAssetType }
func (computeURLMapDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeURLMapDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/urlMaps/%s", projectID, name)
	return makeImportedResource(book, computeURLMapTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeURLMapDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_url_map: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/urlMaps/"); idx >= 0 {
		rest := id[idx+len("/urlMaps/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		importID := fmt.Sprintf("projects/%s/global/urlMaps/%s", projectID, rest)
		return makeImportedResource(addressBook{}, computeURLMapTFType, rest, importID, projectID, "", map[string]string{
			"asset_name": fmt.Sprintf("//%s/projects/%s/global/urlMaps/%s", computeAssetHost, projectID, rest),
		}, nil), nil
	}
	if strings.ContainsAny(id, " /:") {
		return imported.ImportedResource{}, fmt.Errorf("compute_url_map: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/global/urlMaps/%s", projectID, id)
	return makeImportedResource(addressBook{}, computeURLMapTFType, id, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/urlMaps/%s", computeAssetHost, projectID, id),
	}, nil), nil
}
