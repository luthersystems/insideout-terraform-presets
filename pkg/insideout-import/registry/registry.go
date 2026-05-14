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
	"aws_acm_certificate",
	"aws_api_gateway_deployment",
	"aws_api_gateway_resource",
	"aws_api_gateway_stage",
	"aws_apigatewayv2_api",
	"aws_apigatewayv2_api_mapping",
	"aws_apigatewayv2_authorizer",
	"aws_apigatewayv2_domain_name",
	"aws_apigatewayv2_integration",
	"aws_apigatewayv2_route",
	"aws_apigatewayv2_stage",
	"aws_autoscaling_group",
	"aws_backup_plan",
	"aws_backup_selection",
	"aws_backup_vault",
	"aws_bedrock_guardrail",
	"aws_bedrock_model_invocation_logging_configuration",
	"aws_cloudfront_distribution",
	"aws_cloudfront_function",
	"aws_cloudfront_monitoring_subscription",
	"aws_cloudfront_origin_access_identity",
	"aws_cloudwatch_dashboard",
	"aws_cloudwatch_event_rule",
	"aws_cloudwatch_log_group",
	"aws_cloudwatch_log_resource_policy",
	"aws_cloudwatch_log_stream",
	"aws_cloudwatch_metric_alarm",
	"aws_cognito_identity_provider",
	"aws_cognito_resource_server",
	"aws_cognito_user_pool",
	"aws_cognito_user_pool_client",
	"aws_cognito_user_pool_domain",
	"aws_db_instance",
	"aws_db_parameter_group",
	"aws_db_subnet_group",
	"aws_dynamodb_table",
	"aws_ebs_volume",
	"aws_ecs_cluster",
	"aws_ecs_cluster_capacity_providers",
	"aws_eip",
	"aws_eks_access_entry",
	"aws_eks_addon",
	"aws_eks_cluster",
	"aws_eks_fargate_profile",
	"aws_eks_node_group",
	"aws_eks_pod_identity_association",
	"aws_elasticache_parameter_group",
	"aws_elasticache_replication_group",
	"aws_elasticache_subnet_group",
	"aws_iam_group",
	"aws_iam_instance_profile",
	"aws_iam_policy",
	"aws_iam_role",
	"aws_iam_role_policy",
	"aws_iam_service_linked_role",
	"aws_iam_user",
	"aws_instance",
	"aws_internet_gateway",
	"aws_key_pair",
	"aws_kms_alias",
	"aws_kms_key",
	"aws_lambda_alias",
	"aws_lambda_event_source_mapping",
	"aws_lambda_function",
	"aws_lambda_function_url",
	"aws_lambda_permission",
	"aws_launch_template",
	"aws_lb",
	"aws_lb_listener",
	"aws_lb_target_group",
	"aws_msk_cluster",
	"aws_msk_configuration",
	"aws_nat_gateway",
	"aws_network_acl",
	"aws_network_interface",
	"aws_opensearch_domain",
	"aws_opensearchserverless_access_policy",
	"aws_opensearchserverless_collection",
	"aws_opensearchserverless_security_policy",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_route53_zone",
	"aws_route_table",
	"aws_s3_bucket",
	"aws_s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_ownership_controls",
	"aws_s3_bucket_policy",
	"aws_s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning",
	"aws_secretsmanager_secret",
	"aws_secretsmanager_secret_rotation",
	"aws_security_group",
	"aws_service_discovery_private_dns_namespace",
	"aws_sns_topic",
	"aws_sns_topic_subscription",
	"aws_sqs_queue",
	"aws_ssm_parameter",
	"aws_subnet",
	"aws_vpc",
	"aws_vpc_dhcp_options",
	"aws_vpc_endpoint",
	"aws_wafv2_web_acl",
}

// gcpTypes is the canonical, sorted list of GCP Terraform resource types the
// discover pipeline emits clean HCL for. Keep sorted lexicographically; the
// gcpdiscover parity test will fail if this drifts from the live constructor.
var gcpTypes = []string{
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_api_gateway_gateway",
	"google_cloud_run_v2_service",
	"google_cloudbuild_trigger",
	"google_cloudfunctions2_function",
	"google_compute_address",
	"google_compute_firewall",
	"google_compute_forwarding_rule",
	"google_compute_global_address",
	"google_compute_global_forwarding_rule",
	"google_compute_instance",
	"google_compute_network",
	"google_compute_router",
	"google_compute_security_policy",
	"google_compute_target_https_proxy",
	"google_compute_url_map",
	"google_container_cluster",
	"google_container_node_pool",
	"google_firestore_database",
	"google_identity_platform_config",
	"google_kms_crypto_key",
	"google_kms_key_ring",
	"google_logging_project_sink",
	"google_monitoring_alert_policy",
	"google_monitoring_dashboard",
	"google_monitoring_notification_channel",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_redis_instance",
	"google_secret_manager_secret",
	"google_service_account",
	"google_sql_database_instance",
	"google_sql_user",
	"google_storage_bucket",
	"google_vertex_ai_dataset",
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
