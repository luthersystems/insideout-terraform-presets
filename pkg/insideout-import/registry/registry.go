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
//
// # Single source of truth for type lists (#482 / #494)
//
// This file is the canonical home for every list of Terraform resource
// types referenced by the InsideOut pipeline. Three concerns share it:
//
//  1. Discoverable types (per-provider) — what the live discoverer can
//     list from a real cloud account. Surfaced via SupportedDiscoverTypes;
//     downstream wizard contract.
//  2. Layer-1 codegen targets — what cmd/imported-codegen emits typed
//     structs + zod schemas for. Historically lived in
//     cmd/imported-codegen/config.go::WantedAWS / WantedGoogle /
//     WantedGoogleBeta as a parallel hand-maintained list. That parallel
//     split caused bundle 12 to silently miscount drift coverage when a
//     curator added a type to one list but not the other.
//  3. Beta-provider overlay — the small set of GCP types whose schema
//     lives in hashicorp/google-beta (currently the api_gateway family).
//
// To kill the parallel lists, WantedAWSCodegen / WantedGoogleCodegen /
// WantedGoogleBetaCodegen are now defined here and re-exported by
// cmd/imported-codegen/config.go as the old names. Curators only ever
// edit the lists in this file; the codegen pipeline reads them through
// the re-export, and the wizard reads them through SupportedDiscoverTypes.
package registry

import "slices"

const (
	ProviderAWS = "aws"
	ProviderGCP = "gcp"
)

// awsDiscoverTypes is the canonical, sorted list of AWS Terraform resource
// types the discover pipeline emits clean HCL for. Keep sorted lexicographi-
// cally; the awsdiscover parity test will fail if this drifts from the live
// constructor.
//
// Every entry here also drives Layer-1 codegen (the imported-codegen WantedAWS
// re-export concatenates this with awsCodegenOnlyTypes).
var awsDiscoverTypes = []string{
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
	"aws_autoscaling_group_tag",
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
	"aws_dynamodb_contributor_insights",
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
	"aws_iam_role_policy_attachment",
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
	"aws_vpc_security_group_egress_rule",
	"aws_vpc_security_group_ingress_rule",
	"aws_wafv2_web_acl",
	"aws_wafv2_web_acl_association",
}

// awsCodegenOnlyTypes is the sorted list of AWS Terraform resource types
// that have Layer-1 typed structs + curated Layer-2 policy maps but are
// NOT yet wired to a live discoverer. They were authored as part of the
// #482 drift-coverage push but their discoverer registration is pending
// (the underlying AWS APIs are mostly not cloud-control-routable, so each
// needs a hand-rolled SDKLister in cmd/insideout-import/awsdiscover).
//
// Until a live discoverer is added per type, they appear in the codegen
// output (so the structs + policy maps stay in lockstep with the rest of
// the registry) but NOT in SupportedDiscoverTypes (so the reliable wizard
// doesn't advertise picker rows it cannot actually fetch).
//
// The SUPPORTED_RESOURCES.md capabilities matrix iterates the union via
// KnownTypes — these rows show Discoverable=✗ + Enrichable=✗ but
// DriftDetectable=✓ + AgentEditable=✓, correctly surfacing the gap.
//
// To promote an entry from this list to awsDiscoverTypes:
//  1. Author a discoverer in cmd/insideout-import/awsdiscover (either a
//     cloudControlTypeConfig row if the type is cloud-control-routable,
//     or a hand-rolled SDKLister).
//  2. Add the IAM permissions row to pkg/insideout-import/permissions/aws.json
//     and the slug entry to permissions_test.go::awsTFTypeToServiceSlug.
//  3. Move the entry from this list to awsDiscoverTypes (alphabetical).
var awsCodegenOnlyTypes = []string{
	"aws_appautoscaling_policy",
	"aws_appautoscaling_target",
	"aws_athena_workgroup",
	"aws_cloudtrail",
	"aws_cloudwatch_event_bus",
	"aws_codebuild_project",
	"aws_codedeploy_app",
	"aws_codepipeline",
	"aws_dynamodb_global_table",
	"aws_ecs_service",
	"aws_ecs_task_definition",
	"aws_efs_file_system",
	"aws_glue_catalog_database",
	"aws_glue_job",
	"aws_kinesis_stream",
	"aws_lambda_layer_version",
	"aws_rds_cluster",
	"aws_sfn_state_machine",
}

// gcpDiscoverTypes is the canonical, sorted list of GCP Terraform resource
// types the discover pipeline emits clean HCL for. Keep sorted lexicographi-
// cally; the gcpdiscover parity test will fail if this drifts from the live
// constructor.
//
// This is the union of gcpGoogleCodegenTypes and gcpGoogleBetaCodegenTypes
// (the underlying schema source differs but discovery treats them identical-
// ly). Unlike AWS, every GCP discoverer entry already has a Layer-1 struct,
// so there's no GCP equivalent of awsCodegenOnlyTypes.
var gcpDiscoverTypes = []string{
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_api_gateway_gateway",
	"google_cloud_run_v2_service",
	"google_cloud_run_v2_service_iam_member",
	"google_cloudbuild_trigger",
	"google_cloudfunctions2_function",
	"google_cloudfunctions2_function_iam_member",
	"google_compute_address",
	"google_compute_backend_service",
	"google_compute_firewall",
	"google_compute_forwarding_rule",
	"google_compute_global_address",
	"google_compute_global_forwarding_rule",
	"google_compute_health_check",
	"google_compute_instance",
	"google_compute_managed_ssl_certificate",
	"google_compute_network",
	"google_compute_resource_policy",
	"google_compute_router",
	"google_compute_security_policy",
	"google_compute_target_http_proxy",
	"google_compute_target_https_proxy",
	"google_compute_url_map",
	"google_container_cluster",
	"google_container_node_pool",
	"google_firestore_database",
	"google_identity_platform_config",
	"google_identity_platform_default_supported_idp_config",
	"google_kms_crypto_key",
	"google_kms_crypto_key_iam_binding",
	"google_kms_key_ring",
	"google_logging_project_sink",
	"google_monitoring_alert_policy",
	"google_monitoring_dashboard",
	"google_monitoring_notification_channel",
	"google_project_iam_member",
	"google_project_service",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_redis_instance",
	"google_secret_manager_secret",
	"google_secret_manager_secret_iam_binding",
	"google_secret_manager_secret_iam_member",
	"google_secret_manager_secret_version",
	"google_service_account",
	"google_service_networking_connection",
	"google_sql_database_instance",
	"google_sql_user",
	"google_storage_bucket",
	"google_storage_bucket_iam_member",
	"google_storage_bucket_object",
	"google_vertex_ai_dataset",
	"google_vpc_access_connector",
}

// googleBetaCodegenTypes is the subset of gcpDiscoverTypes whose schema
// lives in the hashicorp/google-beta provider rather than hashicorp/google.
// The codegen pipeline routes these through the beta schema dump and emits
// GoogleBetaProviderSource markers so the composer's imported-resource path
// uses the `google-beta.imported` provider alias.
//
// At discover time the live discoverer treats beta-only types identically
// to GA types — Google's discovery APIs don't distinguish — so they appear
// in gcpDiscoverTypes alongside everything else.
var googleBetaCodegenTypes = []string{
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_api_gateway_gateway",
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
		return slices.Clone(awsDiscoverTypes)
	case ProviderGCP:
		return slices.Clone(gcpDiscoverTypes)
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

// AWSCodegenTypes returns the sorted union of awsDiscoverTypes and
// awsCodegenOnlyTypes — every AWS Terraform resource type that
// cmd/imported-codegen emits a Layer-1 typed struct for.
//
// This is the canonical input to the imported-codegen pipeline. The cmd
// package re-exports it as WantedAWS for backwards compatibility with the
// pre-#482 naming. Curators editing the AWS type set must edit
// awsDiscoverTypes / awsCodegenOnlyTypes in this file — there is no longer
// a parallel hand-maintained list in cmd/imported-codegen/config.go.
//
// The returned slice is a fresh copy; callers may mutate it freely.
func AWSCodegenTypes() []string {
	out := make([]string, 0, len(awsDiscoverTypes)+len(awsCodegenOnlyTypes))
	out = append(out, awsDiscoverTypes...)
	out = append(out, awsCodegenOnlyTypes...)
	slices.Sort(out)
	return out
}

// GoogleCodegenTypes returns the sorted list of GCP Terraform resource
// types that cmd/imported-codegen emits Layer-1 typed structs for from
// the hashicorp/google provider (i.e. excluding google-beta-only types).
//
// The returned slice is a fresh copy; callers may mutate it freely.
func GoogleCodegenTypes() []string {
	beta := make(map[string]struct{}, len(googleBetaCodegenTypes))
	for _, t := range googleBetaCodegenTypes {
		beta[t] = struct{}{}
	}
	out := make([]string, 0, len(gcpDiscoverTypes))
	for _, t := range gcpDiscoverTypes {
		if _, isBeta := beta[t]; !isBeta {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}

// GoogleBetaCodegenTypes returns the sorted list of GCP Terraform resource
// types whose schema lives in hashicorp/google-beta rather than the GA
// provider. cmd/imported-codegen routes these against the beta schema dump
// and stamps GoogleBetaProviderSource on the registrations so the composer
// emits them under the `google-beta.imported` provider alias.
//
// The returned slice is a fresh copy; callers may mutate it freely.
func GoogleBetaCodegenTypes() []string {
	return slices.Clone(googleBetaCodegenTypes)
}

// KnownTypes returns the sorted union of every Terraform resource type
// known to the InsideOut pipeline — discoverable + codegen-only — across
// all providers. SUPPORTED_RESOURCES.md iterates this so codegen-only
// types still appear in the capabilities matrix (with Discoverable=✗) and
// drift-coverage % isn't silently undercounted (the bundle-12 regression
// guard).
//
// The returned slice is a fresh copy; callers may mutate it freely.
func KnownTypes() []string {
	aws := AWSCodegenTypes()
	gcp := gcpDiscoverTypes
	out := make([]string, 0, len(aws)+len(gcp))
	out = append(out, aws...)
	out = append(out, gcp...)
	slices.Sort(out)
	return out
}
