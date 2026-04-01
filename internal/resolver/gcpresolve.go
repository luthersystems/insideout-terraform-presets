package resolver

import (
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

// gcpResourceMap maps GCP resource path patterns to terraform types.
// Key format: service/collection (e.g., "compute/networks").
var gcpResourceMap = map[string]string{
	"storage/buckets":             "google_storage_bucket",
	"compute/networks":            "google_compute_network",
	"secretmanager/secrets":       "google_secret_manager_secret",
	"pubsub/topics":               "google_pubsub_topic",
	"pubsub/subscriptions":        "google_pubsub_subscription",
	"compute/subnetworks":         "google_compute_subnetwork",
	"compute/firewalls":           "google_compute_firewall",
	"container/clusters":          "google_container_cluster",
	"sqladmin/instances":          "google_sql_database_instance",
	"redis/instances":             "google_redis_instance",
	"run/services":                "google_cloud_run_v2_service",
}

// GCPResourceToTerraform maps a GCP resource reference to a terraform type
// and import ID. Handles both full resource names
// (//storage.googleapis.com/projects/_/buckets/name) and project paths
// (projects/p/topics/name).
func GCPResourceToTerraform(ref string) (terraformType, importID string, ok bool) {
	// Handle full resource names: //service.googleapis.com/projects/...
	if strings.HasPrefix(ref, "//") {
		return parseFullResourceName(ref)
	}

	// Handle project paths: projects/p/...
	if strings.HasPrefix(ref, "projects/") {
		return parseProjectPath(ref)
	}

	// Handle self-links: https://www.googleapis.com/compute/v1/projects/...
	if strings.HasPrefix(ref, "https://www.googleapis.com/") {
		return parseSelfLink(ref)
	}

	return "", "", false
}

// parseFullResourceName handles //service.googleapis.com/projects/p/collection/name
func parseFullResourceName(fullName string) (string, string, bool) {
	// Strip the // prefix and service domain
	// Format: //storage.googleapis.com/projects/_/buckets/my-bucket
	withoutPrefix := strings.TrimPrefix(fullName, "//")
	slashIdx := strings.Index(withoutPrefix, "/")
	if slashIdx < 0 {
		return "", "", false
	}
	path := withoutPrefix[slashIdx+1:] // "projects/_/buckets/my-bucket"
	return parseProjectPath(path)
}

// parseProjectPath handles projects/p/.../collection/name
func parseProjectPath(path string) (string, string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) < 4 || parts[0] != "projects" {
		return "", "", false
	}

	// Find the collection and name. The path structure varies:
	// projects/p/buckets/name (3 segments after "projects/p")
	// projects/p/global/networks/name
	// projects/p/topics/name
	// projects/p/locations/region/services/name

	// Walk backwards to find the last collection/name pair
	collection := parts[len(parts)-2]

	// Try to determine the service from the collection
	for key, tfType := range gcpResourceMap {
		keyParts := strings.Split(key, "/")
		if keyParts[len(keyParts)-1] == collection {
			// Use the full path as import ID (terraform needs it)
			return tfType, path, true
		}
	}

	return "", "", false
}

// parseSelfLink handles https://www.googleapis.com/compute/v1/projects/p/...
func parseSelfLink(link string) (string, string, bool) {
	// Strip the API prefix to get a project path
	// https://www.googleapis.com/compute/v1/projects/p/global/networks/name
	idx := strings.Index(link, "/projects/")
	if idx < 0 {
		return "", "", false
	}
	path := link[idx+1:] // "projects/p/global/networks/name"
	return parseProjectPath(path)
}

// ResolveGCPReference attempts to resolve a GCP resource reference to a
// DiscoveredResource for dependency chasing.
func ResolveGCPReference(ref string) *discovery.DiscoveredResource {
	tfType, importID, ok := GCPResourceToTerraform(ref)
	if !ok {
		return nil
	}

	// Extract short name from the import ID
	parts := strings.Split(importID, "/")
	name := parts[len(parts)-1]

	return &discovery.DiscoveredResource{
		TerraformType: tfType,
		ImportID:      importID,
		Name:          name,
		ARN:           ref, // Store the original reference as canonical ID
	}
}
