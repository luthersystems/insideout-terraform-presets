package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_backend_service.
//
// Cloud Asset Inventory: compute.googleapis.com/BackendService
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/backendServices/<name>
// Terraform import ID:   projects/<proj>/global/backendServices/<name>
//
// Backend services are global (project/global/...) and don't carry
// labels per the provider schema → ScopeStyleNamePrefix.

const (
	computeBackendServiceTFType    = "google_compute_backend_service"
	computeBackendServiceAssetType = "compute.googleapis.com/BackendService"
)

type computeBackendServiceDiscoverer struct{}

func newComputeBackendServiceDiscoverer() Discoverer { return &computeBackendServiceDiscoverer{} }

func (computeBackendServiceDiscoverer) ResourceType() string   { return computeBackendServiceTFType }
func (computeBackendServiceDiscoverer) AssetType() string      { return computeBackendServiceAssetType }
func (computeBackendServiceDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeBackendServiceDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/backendServices/%s", projectID, name)
	return makeImportedResource(book, computeBackendServiceTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeBackendServiceDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_backend_service: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/backendServices/"); idx >= 0 {
		rest := id[idx+len("/backendServices/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		importID := fmt.Sprintf("projects/%s/global/backendServices/%s", projectID, rest)
		return makeImportedResource(addressBook{}, computeBackendServiceTFType, rest, importID, projectID, "", map[string]string{
			"asset_name": fmt.Sprintf("//%s/projects/%s/global/backendServices/%s", computeAssetHost, projectID, rest),
		}, nil), nil
	}
	if strings.ContainsAny(id, " /:") {
		return imported.ImportedResource{}, fmt.Errorf("compute_backend_service: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/global/backendServices/%s", projectID, id)
	return makeImportedResource(addressBook{}, computeBackendServiceTFType, id, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/backendServices/%s", computeAssetHost, projectID, id),
	}, nil), nil
}
