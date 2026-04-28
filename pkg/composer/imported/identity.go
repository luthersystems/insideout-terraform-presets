package imported

// ResourceIdentity separates Terraform's stable address from cloud-side
// correlation identifiers. Address is immutable after import; renaming is a
// future explicit migration operation using `moved {}` blocks, not an ordinary
// edit. See docs/managed-resource-tiers.md lines 456-475 for the canonical
// shape and lines 517-544 for the address generation algorithm.
//
// Use ImportID and ProviderIdentity for Terraform import; use NativeIDs for
// lookup, dedupe, and cross-reference resolution.
type ResourceIdentity struct {
	// Cloud is the platform identifier: "aws" or "gcp".
	Cloud string `json:"cloud,omitempty"`
	// Type is the Terraform resource type, e.g. "aws_sqs_queue".
	Type string `json:"type,omitempty"`
	// Address is the immutable Terraform resource address (e.g.
	// "aws_sqs_queue.dlq"). Generated once via GenerateAddress and frozen.
	Address string `json:"address,omitempty"`
	// NameHint is the original human-readable name source preserved for
	// audit and display.
	NameHint string `json:"name_hint,omitempty"`
	// ProviderConfig identifies the provider alias used by emitted HCL,
	// e.g. "aws.imported" or "google.imported".
	ProviderConfig string `json:"provider_config,omitempty"`
	// ProviderSource is the registry source, e.g.
	// "registry.terraform.io/hashicorp/aws".
	ProviderSource string `json:"provider_source,omitempty"`
	// ProviderVersion is the exact provider version used at import time.
	ProviderVersion string `json:"provider_version,omitempty"`
	// SchemaVersion is the generated-schema / codegen version that produced
	// any associated typed Attrs. Empty for opaque-bag-only carriers.
	SchemaVersion string `json:"schema_version,omitempty"`

	// AccountID is the AWS account ID when applicable.
	AccountID string `json:"account_id,omitempty"`
	// ProjectID is the GCP project ID when applicable.
	ProjectID string `json:"project_id,omitempty"`
	// Region is the AWS region or GCP region when applicable.
	Region string `json:"region,omitempty"`
	// Location is the GCP zone/location when region is not enough.
	Location string `json:"location,omitempty"`
	// ImportID is the provider import ID: URL, ARN, name, self-link, etc.
	ImportID string `json:"import_id,omitempty"`
	// ProviderIdentity is the Terraform identity object when supported.
	ProviderIdentity map[string]string `json:"provider_identity,omitempty"`
	// NativeIDs holds cloud-side identifiers (arn, url, self_link, name).
	NativeIDs map[string]string `json:"native_ids,omitempty"`
}
