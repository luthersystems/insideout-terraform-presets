package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	assetpb "cloud.google.com/go/asset/apiv1/assetpb"
	"google.golang.org/api/iterator"
)

// gcpAssetMap maps GCP Cloud Asset types to Terraform resource types.
var gcpAssetMap = map[string]string{
	"storage.googleapis.com/Bucket":           "google_storage_bucket",
	"compute.googleapis.com/Network":          "google_compute_network",
	"secretmanager.googleapis.com/Secret":      "google_secret_manager_secret",
	"pubsub.googleapis.com/Topic":             "google_pubsub_topic",
	"pubsub.googleapis.com/Subscription":      "google_pubsub_subscription",
}

// gcpAssetResult is a simplified view of a Cloud Asset search result,
// used to decouple from the gRPC iterator for testing.
type gcpAssetResult struct {
	Name      string            // Full resource name (//storage.googleapis.com/projects/_/buckets/name)
	AssetType string            // e.g., "storage.googleapis.com/Bucket"
	Labels    map[string]string // Resource labels
	Project   string            // GCP project ID
}

// gcpAssetSearcher abstracts the Cloud Asset API for testing.
type gcpAssetSearcher interface {
	SearchAll(ctx context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error)
}

// realAssetSearcher wraps the real Cloud Asset client.
type realAssetSearcher struct {
	client *asset.Client
}

func (s *realAssetSearcher) Close() error {
	return s.client.Close()
}

func (s *realAssetSearcher) SearchAll(ctx context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error) {
	req := &assetpb.SearchAllResourcesRequest{
		Scope:      scope,
		AssetTypes: assetTypes,
		Query:      query,
	}

	var results []gcpAssetResult
	it := s.client.SearchAllResources(ctx, req)
	for {
		result, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("search all resources: %w", err)
		}
		results = append(results, gcpAssetResult{
			Name:      result.Name,
			AssetType: result.AssetType,
			Labels:    result.Labels,
			Project:   result.Project,
		})
	}
	return results, nil
}

// GCPDiscoverer discovers GCP resources via the Cloud Asset Inventory API.
type GCPDiscoverer struct {
	searcher gcpAssetSearcher
	project  string
	logger   *slog.Logger
}

// Close releases the underlying gRPC connection. Should be called when
// the discoverer is no longer needed.
func (d *GCPDiscoverer) Close() error {
	if c, ok := d.searcher.(*realAssetSearcher); ok {
		return c.Close()
	}
	return nil
}

// NewGCPDiscoverer creates a discoverer for the given GCP project.
// The caller should call Close() when done to release the gRPC connection.
func NewGCPDiscoverer(ctx context.Context, project string, logger *slog.Logger) (*GCPDiscoverer, error) {
	client, err := asset.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create asset client: %w", err)
	}
	return &GCPDiscoverer{
		searcher: &realAssetSearcher{client: client},
		project:  project,
		logger:   logger,
	}, nil
}

// DiscoverAll discovers all supported GCP resource types.
func (d *GCPDiscoverer) DiscoverAll(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	assetTypes := make([]string, 0, len(gcpAssetMap))
	for at := range gcpAssetMap {
		assetTypes = append(assetTypes, at)
	}
	return d.discover(ctx, filter, assetTypes)
}

// DiscoverTypes discovers only the specified terraform resource types.
func (d *GCPDiscoverer) DiscoverTypes(ctx context.Context, filter Filter, types []string) ([]DiscoveredResource, error) {
	// Reverse-map terraform types to asset types
	tfToAsset := make(map[string]string, len(gcpAssetMap))
	for at, tt := range gcpAssetMap {
		tfToAsset[tt] = at
	}

	var assetTypes []string
	for _, tt := range types {
		if at, ok := tfToAsset[tt]; ok {
			assetTypes = append(assetTypes, at)
		} else {
			d.logger.Warn("unsupported resource type, skipping", "type", tt)
		}
	}
	return d.discover(ctx, filter, assetTypes)
}

func (d *GCPDiscoverer) discover(ctx context.Context, filter Filter, assetTypes []string) ([]DiscoveredResource, error) {
	scope := fmt.Sprintf("projects/%s", d.project)

	// Build query for label filtering
	query := ""
	if len(filter.Tags) > 0 {
		var parts []string
		for k, v := range filter.Tags {
			parts = append(parts, fmt.Sprintf("labels.%s:%s", k, v))
		}
		query = strings.Join(parts, " AND ")
	}

	d.logger.Info("discovering GCP resources", "project", d.project, "asset_types", len(assetTypes))
	results, err := d.searcher.SearchAll(ctx, scope, assetTypes, query)
	if err != nil {
		return nil, fmt.Errorf("gcp asset search: %w", err)
	}

	var resources []DiscoveredResource
	for _, r := range results {
		tfType, ok := gcpAssetMap[r.AssetType]
		if !ok {
			continue
		}

		name := extractGCPResourceName(r.Name)
		importID := gcpImportID(r.AssetType, r.Name, d.project)

		// For GCP, the Cloud Asset API already scopes to the project,
		// so name prefix filtering is only applied when explicitly
		// requested via filter.Tags["name_prefix"].
		if namePrefix, ok := filter.Tags["name_prefix"]; ok {
			if !MatchesPrefix(name, namePrefix) {
				continue
			}
		}

		d.logger.Info("found resource", "type", tfType, "name", name)
		resources = append(resources, DiscoveredResource{
			TerraformType: tfType,
			ImportID:      importID,
			Name:          name,
			Tags:          r.Labels,
			ARN:           r.Name, // Full resource name as canonical ID
		})
	}

	d.logger.Info("GCP discovery complete", "total", len(resources))
	return resources, nil
}

// extractGCPResourceName extracts the short resource name from a full
// Cloud Asset resource name like "//storage.googleapis.com/projects/_/buckets/my-bucket".
func extractGCPResourceName(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return fullName
}

// gcpImportID constructs the terraform import ID for a GCP resource.
// Different resource types use different import ID formats.
func gcpImportID(assetType, fullName, project string) string {
	name := extractGCPResourceName(fullName)

	switch assetType {
	case "storage.googleapis.com/Bucket":
		// GCS buckets import by name only
		return name
	case "compute.googleapis.com/Network":
		return fmt.Sprintf("projects/%s/global/networks/%s", project, name)
	case "secretmanager.googleapis.com/Secret":
		return fmt.Sprintf("projects/%s/secrets/%s", project, name)
	case "pubsub.googleapis.com/Topic":
		return fmt.Sprintf("projects/%s/topics/%s", project, name)
	case "pubsub.googleapis.com/Subscription":
		return fmt.Sprintf("projects/%s/subscriptions/%s", project, name)
	default:
		return name
	}
}
