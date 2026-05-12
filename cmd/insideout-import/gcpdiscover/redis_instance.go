package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_redis_instance (Memorystore).
//
// Cloud Asset Inventory: redis.googleapis.com/Instance
// Asset name shape:      //redis.googleapis.com/projects/<proj>/locations/<r>/instances/<name>
// Terraform import ID:   projects/<proj>/locations/<r>/instances/<name>
//
// Redis instances DO carry labels per the provider schema, so the
// label-bucket dispatch from #366 attributes them by the project
// label. The gcp/memorystore preset emits
// `labels = merge({ project = var.project }, var.labels)`.

const (
	redisInstanceTFType    = "google_redis_instance"
	redisInstanceAssetType = "redis.googleapis.com/Instance"

	redisAssetHost = "redis.googleapis.com"
)

type redisInstanceDiscoverer struct{}

func newRedisInstanceDiscoverer() Discoverer { return &redisInstanceDiscoverer{} }

func (redisInstanceDiscoverer) ResourceType() string   { return redisInstanceTFType }
func (redisInstanceDiscoverer) AssetType() string      { return redisInstanceAssetType }
func (redisInstanceDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (redisInstanceDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, loc, name)
	return makeImportedResource(book, redisInstanceTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (redisInstanceDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := redisInstancePartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, loc, name)
	assetName := fmt.Sprintf("//%s/projects/%s/locations/%s/instances/%s", redisAssetHost, projectID, loc, name)
	return makeImportedResource(addressBook{}, redisInstanceTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": assetName,
	}, nil), nil
}

// redisInstancePartsFromID extracts (location, name) from a Cloud
// Asset full name or the projects/<p>/locations/<l>/instances/<n>
// Terraform import-ID form.
func redisInstancePartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("redis_instance: empty id: %w", ErrNotSupported)
	}
	loc, name := parseLocationAndTrailing(id, "/instances/")
	if loc == "" || name == "" {
		return "", "", fmt.Errorf("redis_instance: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, name, nil
}
