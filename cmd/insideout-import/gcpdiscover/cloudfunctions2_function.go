package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_cloudfunctions2_function.
//
// Cloud Asset Inventory: cloudfunctions.googleapis.com/Function
// Asset name shape:      //cloudfunctions.googleapis.com/projects/<proj>/locations/<loc>/functions/<name>
// Terraform import ID:   projects/<proj>/locations/<loc>/functions/<name>
//
// Gen-2 Cloud Functions carry labels per the provider schema →
// ScopeStyleLabels. Cloud Asset uses the same asset-type slug
// (cloudfunctions.googleapis.com/Function) for Gen-1 and Gen-2;
// the discoverer maps to google_cloudfunctions2_function — operators
// who still run Gen-1 stacks must use the unsupported.json surface
// (the Gen-1 type isn't a Bundle 8 target).

const (
	cloudFunctions2FunctionTFType    = "google_cloudfunctions2_function"
	cloudFunctions2FunctionAssetType = "cloudfunctions.googleapis.com/Function"

	cloudFunctionsAssetHost = "cloudfunctions.googleapis.com"
)

type cloudFunctions2FunctionDiscoverer struct{}

func newCloudFunctions2FunctionDiscoverer() Discoverer { return &cloudFunctions2FunctionDiscoverer{} }

func (cloudFunctions2FunctionDiscoverer) ResourceType() string   { return cloudFunctions2FunctionTFType }
func (cloudFunctions2FunctionDiscoverer) AssetType() string      { return cloudFunctions2FunctionAssetType }
func (cloudFunctions2FunctionDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (cloudFunctions2FunctionDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/functions/%s", projectID, loc, name)
	return makeImportedResource(book, cloudFunctions2FunctionTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (cloudFunctions2FunctionDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := cloudFunctions2FunctionPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/functions/%s", projectID, loc, name)
	return makeImportedResource(addressBook{}, cloudFunctions2FunctionTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/functions/%s", cloudFunctionsAssetHost, projectID, loc, name),
	}, nil), nil
}

func cloudFunctions2FunctionPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("cloudfunctions2_function: empty id: %w", ErrNotSupported)
	}
	loc, name := parseLocationAndTrailing(id, "/functions/")
	if loc == "" || name == "" {
		return "", "", fmt.Errorf("cloudfunctions2_function: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, name, nil
}
