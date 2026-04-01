package discovery

import (
	"context"
	"strings"
)

// DiscoveredResource represents a single AWS resource found during discovery.
type DiscoveredResource struct {
	// TerraformType is the Terraform resource type (e.g., "aws_sqs_queue").
	TerraformType string

	// ImportID is the identifier used by terraform import for this resource type.
	// Format varies by type: queue URL for SQS, table name for DynamoDB, ARN for secrets, etc.
	ImportID string

	// Name is a human-readable identifier used to construct the HCL resource address.
	// Will be sanitized to a valid HCL identifier before use.
	Name string

	// Tags are the AWS tags on the resource.
	Tags map[string]string

	// ARN is the full ARN of the resource (when available).
	ARN string
}

// Filter controls which resources are discovered.
type Filter struct {
	// Project is the InsideOut project ID used as a name prefix filter.
	Project string

	// Region is the AWS region to scan.
	Region string

	// Tags is an optional map of additional tag key-value pairs that must all match.
	Tags map[string]string
}

// Discoverer discovers AWS resources of a specific type.
type Discoverer interface {
	// Discover returns all matching resources for this service.
	Discover(ctx context.Context, filter Filter) ([]DiscoveredResource, error)

	// ResourceType returns the Terraform resource type this discoverer handles
	// (e.g., "aws_sqs_queue").
	ResourceType() string
}

// MatchesPrefix returns true if name starts with the project prefix.
func MatchesPrefix(name, project string) bool {
	return strings.HasPrefix(name, project)
}

// MatchesTags returns true if the resource tags contain all required tag key-value pairs.
func MatchesTags(resourceTags, requiredTags map[string]string) bool {
	for k, v := range requiredTags {
		if resourceTags[k] != v {
			return false
		}
	}
	return true
}
