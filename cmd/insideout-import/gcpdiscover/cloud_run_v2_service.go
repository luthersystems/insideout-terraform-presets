package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_cloud_run_v2_service.
//
// Cloud Asset Inventory: run.googleapis.com/Service
// Asset name shape:      //run.googleapis.com/projects/<proj>/locations/<loc>/services/<name>
// Terraform import ID:   projects/<proj>/locations/<loc>/services/<name>
//
// Cloud Run v2 services carry labels per the provider schema →
// ScopeStyleLabels. The asset surface returns Location as the
// service's region (e.g. us-central1).

const (
	cloudRunV2ServiceTFType    = "google_cloud_run_v2_service"
	cloudRunV2ServiceAssetType = "run.googleapis.com/Service"

	cloudRunAssetHost = "run.googleapis.com"
)

type cloudRunV2ServiceDiscoverer struct{}

func newCloudRunV2ServiceDiscoverer() Discoverer { return &cloudRunV2ServiceDiscoverer{} }

func (cloudRunV2ServiceDiscoverer) ResourceType() string   { return cloudRunV2ServiceTFType }
func (cloudRunV2ServiceDiscoverer) AssetType() string      { return cloudRunV2ServiceAssetType }
func (cloudRunV2ServiceDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (cloudRunV2ServiceDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, loc, name)
	return makeImportedResource(book, cloudRunV2ServiceTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (cloudRunV2ServiceDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := cloudRunV2ServicePartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, loc, name)
	return makeImportedResource(addressBook{}, cloudRunV2ServiceTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/services/%s", cloudRunAssetHost, projectID, loc, name),
	}, nil), nil
}

func cloudRunV2ServicePartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("cloud_run_v2_service: empty id: %w", ErrNotSupported)
	}
	loc, name := parseLocationAndTrailing(id, "/services/")
	if loc == "" || name == "" {
		return "", "", fmt.Errorf("cloud_run_v2_service: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, name, nil
}
