package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_vertex_ai_dataset.
//
// Cloud Asset Inventory: aiplatform.googleapis.com/Dataset
// Asset name shape:      //aiplatform.googleapis.com/projects/<proj>/locations/<r>/datasets/<id>
// Terraform import ID:   projects/<proj>/locations/<r>/datasets/<id>
//
// Vertex AI datasets DO carry labels per the provider schema →
// ScopeStyleLabels. The `id` field is a numeric Vertex-assigned
// identifier; the user-facing display_name is a separate attribute
// that does not appear in the asset short-name.

const (
	vertexAIDatasetTFType    = "google_vertex_ai_dataset"
	vertexAIDatasetAssetType = "aiplatform.googleapis.com/Dataset"

	aiplatformAssetHost = "aiplatform.googleapis.com"
)

type vertexAIDatasetDiscoverer struct{}

func newVertexAIDatasetDiscoverer() Discoverer { return &vertexAIDatasetDiscoverer{} }

func (vertexAIDatasetDiscoverer) ResourceType() string   { return vertexAIDatasetTFType }
func (vertexAIDatasetDiscoverer) AssetType() string      { return vertexAIDatasetAssetType }
func (vertexAIDatasetDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (vertexAIDatasetDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	id := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/datasets/%s", projectID, loc, id)
	return makeImportedResource(book, vertexAIDatasetTFType, id, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (vertexAIDatasetDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, dsID, err := vertexAIDatasetPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/datasets/%s", projectID, loc, dsID)
	assetName := fmt.Sprintf("//%s/projects/%s/locations/%s/datasets/%s", aiplatformAssetHost, projectID, loc, dsID)
	return makeImportedResource(addressBook{}, vertexAIDatasetTFType, dsID, importID, projectID, loc, map[string]string{
		"asset_name": assetName,
	}, nil), nil
}

// vertexAIDatasetPartsFromID extracts (location, dataset_id) from a
// Cloud Asset full name or the projects/<p>/locations/<l>/datasets/<id>
// Terraform import-ID form.
func vertexAIDatasetPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("vertex_ai_dataset: empty id: %w", ErrNotSupported)
	}
	loc, dsID := parseLocationAndTrailing(id, "/datasets/")
	if loc == "" || dsID == "" {
		return "", "", fmt.Errorf("vertex_ai_dataset: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, dsID, nil
}
