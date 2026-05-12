package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_container_node_pool.
//
// Cloud Asset Inventory: container.googleapis.com/NodePool
// Asset name shape:      //container.googleapis.com/projects/<proj>/locations/<loc>/clusters/<c>/nodePools/<name>
// Terraform import ID:   projects/<proj>/locations/<loc>/clusters/<c>/nodePools/<name>
//
// Node pools have no labels of their own — labels live on the parent
// cluster's resource_labels. Node-pool names are conventionally short
// (e.g. "default-pool"/"system-pool") with the stack project embedded
// in the parent cluster name, so this discoverer is scoped via
// ScopeStyleParentNamePrefix on the "/clusters/" segment (#381), not
// ScopeStyleNamePrefix on the short pool name.

const (
	containerNodePoolTFType    = "google_container_node_pool"
	containerNodePoolAssetType = "container.googleapis.com/NodePool"
)

type containerNodePoolDiscoverer struct{}

func newContainerNodePoolDiscoverer() Discoverer { return &containerNodePoolDiscoverer{} }

func (containerNodePoolDiscoverer) ResourceType() string   { return containerNodePoolTFType }
func (containerNodePoolDiscoverer) AssetType() string      { return containerNodePoolAssetType }
func (containerNodePoolDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleParentNamePrefix }
func (containerNodePoolDiscoverer) ParentMarker() string   { return "/clusters/" }

func (containerNodePoolDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	loc, cluster, name := containerNodePoolAssetParts(a.Name)
	if loc == "" && a.Location != "" {
		loc = a.Location
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", projectID, loc, cluster, name)
	return makeImportedResource(book, containerNodePoolTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
		"cluster":    cluster,
	}, a.Labels)
}

func (containerNodePoolDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, cluster, name, err := containerNodePoolPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", projectID, loc, cluster, name)
	return makeImportedResource(addressBook{}, containerNodePoolTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/clusters/%s/nodePools/%s", containerAssetHost, projectID, loc, cluster, name),
		"cluster":    cluster,
	}, nil), nil
}

// containerNodePoolAssetParts extracts (location, cluster, name) from
// a Cloud Asset NodePool resource name. Returns ("", "", "") on
// malformed input.
func containerNodePoolAssetParts(assetName string) (string, string, string) {
	loc := locationFromKMSAssetName(assetName) // /locations/<l>/ — shared shape
	const clusterMarker = "/clusters/"
	const poolMarker = "/nodePools/"
	cIdx := strings.Index(assetName, clusterMarker)
	pIdx := strings.Index(assetName, poolMarker)
	if cIdx < 0 || pIdx < 0 || pIdx < cIdx {
		return loc, "", ""
	}
	cluster := assetName[cIdx+len(clusterMarker) : pIdx]
	name := assetName[pIdx+len(poolMarker):]
	return loc, cluster, name
}

func containerNodePoolPartsFromID(id string) (string, string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", "", fmt.Errorf("container_node_pool: empty id: %w", ErrNotSupported)
	}
	loc, cluster, name := containerNodePoolAssetParts(id)
	if loc == "" || cluster == "" || name == "" {
		return "", "", "", fmt.Errorf("container_node_pool: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, cluster, name, nil
}
