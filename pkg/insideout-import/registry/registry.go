// Package registry exposes the public, dependency-free list of Terraform
// resource types that `insideout-import discover` supports for each cloud
// provider.
//
// The reliable repo's importer wizard imports this package to render picker
// and address-mapping rows without referencing CLI source lines. Consumers
// must treat the returned slices as opaque, sorted lists keyed by provider.
//
// Drift between this registry and the live discoverer constructor maps in
// cmd/insideout-import/awsdiscover and cmd/insideout-import/gcpdiscover is
// guarded by parity tests in those packages — see TestRegistryParity_AWS /
// TestRegistryParity_GCP.
package registry

import "slices"

const (
	ProviderAWS = "aws"
	ProviderGCP = "gcp"
)

// awsTypes is the canonical, sorted list of AWS Terraform resource types the
// discover pipeline emits clean HCL for. Keep sorted lexicographically; the
// awsdiscover parity test will fail if this drifts from the live constructor.
var awsTypes = []string{
	"aws_apigatewayv2_api",
	"aws_apigatewayv2_stage",
	"aws_bedrock_guardrail",
	"aws_cloudfront_distribution",
	"aws_cloudwatch_event_rule",
	"aws_cloudwatch_log_group",
	"aws_db_instance",
	"aws_db_parameter_group",
	"aws_db_subnet_group",
	"aws_dynamodb_table",
	"aws_eip",
	"aws_eks_pod_identity_association",
	"aws_iam_policy",
	"aws_iam_role",
	"aws_internet_gateway",
	"aws_kms_key",
	"aws_lambda_function",
	"aws_lb",
	"aws_lb_listener",
	"aws_lb_target_group",
	"aws_nat_gateway",
	"aws_network_acl",
	"aws_network_interface",
	"aws_opensearchserverless_collection",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_route53_zone",
	"aws_route_table",
	"aws_s3_bucket",
	"aws_secretsmanager_secret",
	"aws_security_group",
	"aws_sqs_queue",
	"aws_subnet",
	"aws_vpc",
	"aws_vpc_dhcp_options",
	"aws_vpc_endpoint",
}

// gcpTypes is the canonical, sorted list of GCP Terraform resource types the
// discover pipeline emits clean HCL for. Keep sorted lexicographically; the
// gcpdiscover parity test will fail if this drifts from the live constructor.
var gcpTypes = []string{
	"google_compute_address",
	"google_compute_firewall",
	"google_compute_instance",
	"google_compute_network",
	"google_compute_router",
	"google_container_cluster",
	"google_container_node_pool",
	"google_kms_crypto_key",
	"google_kms_key_ring",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_secret_manager_secret",
	"google_service_account",
	"google_storage_bucket",
}

// SupportedDiscoverTypes returns the sorted, deterministic list of Terraform
// resource types that the discover pipeline can emit clean HCL for, for the
// given provider. Returns nil (not an empty slice) for unrecognized provider
// strings — downstream consumers may distinguish "unknown provider" from
// "known provider with zero supported types" via that nil-vs-non-nil signal,
// and JSON marshaling renders them differently (`null` vs `[]`).
//
// The returned slice is a fresh copy; callers may mutate it freely without
// affecting subsequent calls or the package's internal state.
func SupportedDiscoverTypes(provider string) []string {
	switch provider {
	case ProviderAWS:
		return slices.Clone(awsTypes)
	case ProviderGCP:
		return slices.Clone(gcpTypes)
	default:
		return nil
	}
}

// SupportedProviders returns the sorted list of provider keys recognized by
// SupportedDiscoverTypes. Useful for UIs enumerating providers without
// hardcoding the set. Every entry returned here is guaranteed to map to a
// non-empty SupportedDiscoverTypes result; the round-trip invariant is
// pinned by TestSupportedProviders_RoundTripsThroughSupportedDiscoverTypes.
func SupportedProviders() []string {
	return []string{ProviderAWS, ProviderGCP}
}
