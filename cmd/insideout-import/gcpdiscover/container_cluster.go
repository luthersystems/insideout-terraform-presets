package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_container_cluster.
//
// Cloud Asset Inventory: container.googleapis.com/Cluster
// Asset name shape:      //container.googleapis.com/projects/<proj>/locations/<loc>/clusters/<name>
// Terraform import ID:   projects/<proj>/locations/<loc>/clusters/<name>
//
// GKE clusters expose `resource_labels` per the provider schema and
// land in the labels-bucket. The asset-side `location` is either a
// zone (zonal cluster) or a region (regional cluster) — Identity
// .Location carries it verbatim and the provider import resolves
// either form.

const (
	containerClusterTFType    = "google_container_cluster"
	containerClusterAssetType = "container.googleapis.com/Cluster"

	containerAssetHost = "container.googleapis.com"
)

type containerClusterDiscoverer struct{}

func newContainerClusterDiscoverer() Discoverer { return &containerClusterDiscoverer{} }

func (containerClusterDiscoverer) ResourceType() string   { return containerClusterTFType }
func (containerClusterDiscoverer) AssetType() string      { return containerClusterAssetType }
func (containerClusterDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (containerClusterDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name) // /locations/<loc>/ same shape
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, loc, name)
	return makeImportedResource(book, containerClusterTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://container.googleapis.com/v1/projects/%s/locations/%s/clusters/%s", projectID, loc, name),
	}, a.Labels)
}

func (containerClusterDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := containerClusterPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, loc, name)
	return makeImportedResource(addressBook{}, containerClusterTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/clusters/%s", containerAssetHost, projectID, loc, name),
		"self_link":  fmt.Sprintf("https://container.googleapis.com/v1/projects/%s/locations/%s/clusters/%s", projectID, loc, name),
	}, nil), nil
}

func containerClusterPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("container_cluster: empty id: %w", ErrNotSupported)
	}
	loc, name := parseLocationAndTrailing(id, "/clusters/")
	if loc == "" || name == "" {
		return "", "", fmt.Errorf("container_cluster: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, name, nil
}
