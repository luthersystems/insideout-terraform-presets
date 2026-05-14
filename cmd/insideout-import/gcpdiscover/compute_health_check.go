package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_health_check.
//
// Cloud Asset Inventory: compute.googleapis.com/HealthCheck
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/healthChecks/<name>
// Terraform import ID:   projects/<proj>/global/healthChecks/<name>
//
// Health checks are global (project/global/...) and don't carry labels
// per the provider schema → ScopeStyleNamePrefix.

const (
	computeHealthCheckTFType    = "google_compute_health_check"
	computeHealthCheckAssetType = "compute.googleapis.com/HealthCheck"
)

type computeHealthCheckDiscoverer struct{}

func newComputeHealthCheckDiscoverer() Discoverer { return &computeHealthCheckDiscoverer{} }

func (computeHealthCheckDiscoverer) ResourceType() string   { return computeHealthCheckTFType }
func (computeHealthCheckDiscoverer) AssetType() string      { return computeHealthCheckAssetType }
func (computeHealthCheckDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeHealthCheckDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/healthChecks/%s", projectID, name)
	return makeImportedResource(book, computeHealthCheckTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeHealthCheckDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_health_check: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/healthChecks/"); idx >= 0 {
		rest := id[idx+len("/healthChecks/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		importID := fmt.Sprintf("projects/%s/global/healthChecks/%s", projectID, rest)
		return makeImportedResource(addressBook{}, computeHealthCheckTFType, rest, importID, projectID, "", map[string]string{
			"asset_name": fmt.Sprintf("//%s/projects/%s/global/healthChecks/%s", computeAssetHost, projectID, rest),
		}, nil), nil
	}
	if strings.ContainsAny(id, " /:") {
		return imported.ImportedResource{}, fmt.Errorf("compute_health_check: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/global/healthChecks/%s", projectID, id)
	return makeImportedResource(addressBook{}, computeHealthCheckTFType, id, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/healthChecks/%s", computeAssetHost, projectID, id),
	}, nil), nil
}
