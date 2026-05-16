package bindings

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	typeregistry "github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// metricsBindingExempt is the documented "we deliberately do not have a
// MetricsBinding for this type" list. It is the negative side of the
// TestEveryDiscoverableHasBindingOrExempt invariant: every type in
// registry.SupportedDiscoverTypes(...) must EITHER have a binding in
// seededBindings, OR appear here with a rationale comment.
//
// The exempt list captures the current state at the time the invariant
// landed (#482). To remove a type from the exempt list, register a real
// binding in seed.go::seededBindings. Adding to the exempt list is the
// "we know this has no native CW/SD metrics" escape hatch — prefer
// authoring a real binding when one is available.
//
// Major categories represented here today:
//
//   - IAM-shaped types (roles, policies, instance profiles, members):
//     IAM is a control-plane API with no native CloudWatch / Cloud
//     Monitoring metric surface. Activity is captured via CloudTrail /
//     Audit Logs, not native metrics.
//   - VPC topology primitives (vpc, subnet, route_table, security_group,
//     internet_gateway, network_acl, vpc_endpoint, dhcp_options): static
//     network plumbing, no per-resource metrics.
//   - Composite / wiring resources (api_gateway_resource, lb_listener,
//     lb_target_group, cognito_user_pool_*, cognito_resource_server,
//     apigatewayv2_*): metrics live on the parent (apigateway stage, lb,
//     cognito user_pool) rather than the sub-resource.
//   - Discovery-only types (resourceexplorer2_*, opensearchserverless_*
//     access/security policies): admin surfaces with no time-series.
//   - One-shot / configuration objects (kms_alias, key_pair, ssm_parameter,
//     backup_plan/selection/vault, db_parameter_group, db_subnet_group,
//     elasticache_parameter_group, elasticache_subnet_group, launch_template,
//     bedrock_model_invocation_logging_configuration, secretsmanager_secret_rotation,
//     cloudwatch_log_resource_policy, cloudwatch_log_stream): edits don't
//     produce metric streams.
//   - GCP IAM-member-shaped types (*_iam_member, *_iam_binding,
//     project_iam_member, project_service, service_account): same as AWS
//     IAM — no native time-series surface.
//   - GCP wiring / configuration (compute_address, compute_global_address,
//     compute_forwarding_rule, compute_global_forwarding_rule,
//     compute_managed_ssl_certificate, compute_target_http_proxy,
//     compute_target_https_proxy, compute_url_map, compute_resource_policy,
//     compute_health_check, compute_router, compute_firewall, compute_network,
//     compute_backend_service, compute_security_policy, vpc_access_connector,
//     service_networking_connection): metrics surface on the consumer
//     workload (Cloud Run, GKE), not the network primitive.
//   - GCP singleton / metadata (identity_platform_*, firestore_database,
//     api_gateway_*, kms_*, secret_manager_secret*, logging_project_sink,
//     monitoring_*, vertex_ai_dataset, storage_bucket_object,
//     sql_user): no per-resource metrics or metrics aggregate at the
//     project level.
//   - Lambda sub-resources (lambda_alias, lambda_event_source_mapping,
//     lambda_function_url, lambda_permission): metrics aggregate on the
//     parent aws_lambda_function.
//   - Auto-scaling primitives (autoscaling_group, autoscaling_group_tag):
//     ASG metrics route through EC2 / CloudWatch with a different binding
//     shape; deliberately deferred until the per-ASG dimension story is
//     curated.
//   - Container orchestration sub-resources (eks_access_entry, eks_addon,
//     eks_fargate_profile, eks_node_group, eks_pod_identity_association,
//     ecs_cluster, ecs_cluster_capacity_providers): metrics live on the
//     workload (ecs_service, eks_cluster control plane), not the
//     management plane.
//
// Stale-entry guard: TestExemptListNoStaleEntries asserts every key here
// still appears in registry.SupportedDiscoverTypes for some provider.
// Removing a discoverable type elsewhere will fail this test and force
// the curator to clean up the exempt entry.
var metricsBindingExempt = map[string]bool{
	// --- AWS IAM (control plane, no native CloudWatch metrics) ---
	"aws_iam_group":                  true,
	"aws_iam_instance_profile":       true,
	"aws_iam_policy":                 true,
	"aws_iam_role":                   true,
	"aws_iam_role_policy":            true,
	"aws_iam_role_policy_attachment": true,
	"aws_iam_service_linked_role":    true,
	"aws_iam_user":                   true,

	// --- AWS networking primitives (no per-resource metrics) ---
	"aws_eip":                                     true,
	"aws_internet_gateway":                        true,
	"aws_nat_gateway":                             true,
	"aws_network_acl":                             true,
	"aws_network_interface":                       true,
	"aws_route_table":                             true,
	"aws_security_group":                          true,
	"aws_subnet":                                  true,
	"aws_vpc":                                     true,
	"aws_vpc_dhcp_options":                        true,
	"aws_vpc_endpoint":                            true,
	"aws_vpc_security_group_egress_rule":          true,
	"aws_vpc_security_group_ingress_rule":         true,
	"aws_service_discovery_private_dns_namespace": true,
	"aws_route53_zone":                            true,

	// --- AWS ACM / WAF / CloudFront sub-resources ---
	"aws_acm_certificate":                    true,
	"aws_wafv2_web_acl":                      true,
	"aws_wafv2_web_acl_association":          true,
	"aws_cloudfront_distribution":            true,
	"aws_cloudfront_function":                true,
	"aws_cloudfront_monitoring_subscription": true,
	"aws_cloudfront_origin_access_identity":  true,

	// --- AWS API Gateway (metrics live on the stage, not sub-resources) ---
	"aws_api_gateway_deployment":   true,
	"aws_api_gateway_resource":     true,
	"aws_api_gateway_stage":        true,
	"aws_apigatewayv2_api":         true,
	"aws_apigatewayv2_api_mapping": true,
	"aws_apigatewayv2_authorizer":  true,
	"aws_apigatewayv2_domain_name": true,
	"aws_apigatewayv2_integration": true,
	"aws_apigatewayv2_route":       true,
	"aws_apigatewayv2_stage":       true,

	// --- AWS autoscaling (per-ASG binding deferred) ---
	"aws_autoscaling_group":     true,
	"aws_autoscaling_group_tag": true,

	// --- AWS backup (one-shot configuration objects) ---
	"aws_backup_plan":      true,
	"aws_backup_selection": true,
	"aws_backup_vault":     true,

	// --- AWS Bedrock (no per-resource time-series for guardrails / logging config) ---
	"aws_bedrock_guardrail":                              true,
	"aws_bedrock_model_invocation_logging_configuration": true,

	// --- AWS CloudWatch sub-resources (event rules, dashboards, log
	// policies/streams produce no native metric stream of their own) ---
	"aws_cloudwatch_dashboard":           true,
	"aws_cloudwatch_event_rule":          true,
	"aws_cloudwatch_log_resource_policy": true,
	"aws_cloudwatch_log_stream":          true,
	"aws_cloudwatch_metric_alarm":        true,

	// --- AWS Cognito (metrics aggregate on the parent user_pool) ---
	"aws_cognito_identity_provider": true,
	"aws_cognito_resource_server":   true,
	"aws_cognito_user_pool":         true,
	"aws_cognito_user_pool_client":  true,
	"aws_cognito_user_pool_domain":  true,

	// --- AWS DB sub-resources (metrics on parent db_instance / cluster) ---
	"aws_db_parameter_group":            true,
	"aws_db_subnet_group":               true,
	"aws_dynamodb_contributor_insights": true,
	"aws_ebs_volume":                    true,

	// --- AWS ECS (metrics live on aws_ecs_service, not the cluster) ---
	"aws_ecs_cluster":                    true,
	"aws_ecs_cluster_capacity_providers": true,

	// --- AWS EKS (control plane has separate metric path; sub-resources have none) ---
	"aws_eks_access_entry":             true,
	"aws_eks_addon":                    true,
	"aws_eks_cluster":                  true,
	"aws_eks_fargate_profile":          true,
	"aws_eks_node_group":               true,
	"aws_eks_pod_identity_association": true,

	// --- AWS ElastiCache sub-resources (metrics on the replication_group) ---
	"aws_elasticache_parameter_group":   true,
	"aws_elasticache_replication_group": true,
	"aws_elasticache_subnet_group":      true,

	// --- AWS EC2 (instance metrics binding shape differs; deferred) ---
	"aws_instance":        true,
	"aws_key_pair":        true,
	"aws_launch_template": true,

	// --- AWS KMS (key rotation metrics tied to the parent key) ---
	"aws_kms_alias": true,
	"aws_kms_key":   true,

	// --- AWS Lambda sub-resources (metrics on parent function) ---
	"aws_lambda_alias":                true,
	"aws_lambda_event_source_mapping": true,
	"aws_lambda_function_url":         true,
	"aws_lambda_permission":           true,

	// --- AWS ELBv2 sub-resources (metrics on the parent aws_lb) ---
	"aws_lb_listener":     true,
	"aws_lb_target_group": true,

	// --- AWS MSK (cluster metrics deferred) ---
	"aws_msk_cluster":       true,
	"aws_msk_configuration": true,

	// --- AWS OpenSearch (metrics binding shape deferred) ---
	"aws_opensearch_domain":                    true,
	"aws_opensearchserverless_access_policy":   true,
	"aws_opensearchserverless_collection":      true,
	"aws_opensearchserverless_security_policy": true,

	// --- AWS Resource Explorer (admin surface, no time-series) ---
	"aws_resourceexplorer2_index": true,
	"aws_resourceexplorer2_view":  true,

	// --- AWS S3 sub-resources (metrics on parent bucket) ---
	"aws_s3_bucket_lifecycle_configuration":              true,
	"aws_s3_bucket_ownership_controls":                   true,
	"aws_s3_bucket_policy":                               true,
	"aws_s3_bucket_public_access_block":                  true,
	"aws_s3_bucket_server_side_encryption_configuration": true,
	"aws_s3_bucket_versioning":                           true,

	// --- AWS Secrets Manager rotation (metrics on parent secret) ---
	"aws_secretsmanager_secret_rotation": true,

	// --- AWS SNS subscription (metrics on parent topic) ---
	"aws_sns_topic_subscription": true,

	// --- AWS SSM Parameter Store (no per-parameter metric stream) ---
	"aws_ssm_parameter": true,

	// --- GCP API Gateway (no native GCP-Monitoring time-series) ---
	"google_api_gateway_api":        true,
	"google_api_gateway_api_config": true,
	"google_api_gateway_gateway":    true,

	// --- GCP IAM-shaped (control plane, no metrics) ---
	"google_cloud_run_v2_service_iam_member":     true,
	"google_cloudfunctions2_function_iam_member": true,
	"google_kms_crypto_key_iam_binding":          true,
	"google_project_iam_member":                  true,
	"google_secret_manager_secret_iam_binding":   true,
	"google_secret_manager_secret_iam_member":    true,
	"google_storage_bucket_iam_member":           true,

	// --- GCP Cloud Build / Cloud Functions (binding deferred) ---
	"google_cloudbuild_trigger":       true,
	"google_cloudfunctions2_function": true,

	// --- GCP Compute networking primitives (no per-resource metrics) ---
	"google_compute_address":                 true,
	"google_compute_backend_service":         true,
	"google_compute_firewall":                true,
	"google_compute_forwarding_rule":         true,
	"google_compute_global_address":          true,
	"google_compute_global_forwarding_rule":  true,
	"google_compute_health_check":            true,
	"google_compute_managed_ssl_certificate": true,
	"google_compute_network":                 true,
	"google_compute_resource_policy":         true,
	"google_compute_router":                  true,
	"google_compute_security_policy":         true,
	"google_compute_target_http_proxy":       true,
	"google_compute_target_https_proxy":      true,
	"google_compute_url_map":                 true,

	// --- GCP Compute Instance / GKE (binding deferred — different
	// dimension shape than the Cloud Run / Cloud SQL bindings) ---
	"google_compute_instance":    true,
	"google_container_cluster":   true,
	"google_container_node_pool": true,

	// --- GCP singletons / configuration (no per-resource metrics) ---
	"google_firestore_database":                             true,
	"google_identity_platform_config":                       true,
	"google_identity_platform_default_supported_idp_config": true,
	"google_kms_crypto_key":                                 true,
	"google_kms_key_ring":                                   true,
	"google_logging_project_sink":                           true,
	"google_monitoring_alert_policy":                        true,
	"google_monitoring_dashboard":                           true,
	"google_monitoring_notification_channel":                true,
	"google_project_service":                                true,
	"google_secret_manager_secret":                          true,
	"google_secret_manager_secret_version":                  true,
	"google_service_account":                                true,
	"google_service_networking_connection":                  true,
	"google_sql_user":                                       true,
	"google_storage_bucket_object":                          true,
	"google_vertex_ai_dataset":                              true,
	"google_vpc_access_connector":                           true,
}

// TestEveryDiscoverableHasBindingOrExempt asserts that every type in
// registry.SupportedDiscoverTypes(...) is either backed by a real
// metrics binding (seededBindings) or explicitly listed in
// metricsBindingExempt with a rationale comment.
//
// Rationale: when curators add a type to the live discoverer, the
// downstream UI surfaces a "Metrics" panel for that type. Without a
// MetricsBinding, the panel renders empty — but there's no test today
// to catch the gap before release. This invariant turns the gap into a
// compile-time-style error: either author a binding, or document why a
// binding is impossible by adding to the exempt list.
//
// The exempt list IS the documented "we deliberately don't have a
// binding" decision log — see metricsBindingExempt's header for the
// rationale per category.
func TestEveryDiscoverableHasBindingOrExempt(t *testing.T) {
	// Reseed the registry so this test doesn't depend on test ordering
	// — bindings_test.go's resetForTest tests run in the same package
	// and may have wiped registrations.
	reseed(t)

	discoverable := map[string]struct{}{}
	for _, p := range typeregistry.SupportedProviders() {
		for _, tfType := range typeregistry.SupportedDiscoverTypes(p) {
			discoverable[tfType] = struct{}{}
		}
	}

	missing := []string{}
	for tfType := range discoverable {
		if metricsBindingExempt[tfType] {
			continue
		}
		if _, ok := Binding(tfType); !ok {
			missing = append(missing, tfType)
		}
	}
	sort.Strings(missing)

	require.Empty(t, missing,
		"%d discoverable types have no MetricsBinding and aren't on metricsBindingExempt:\n  %s\n\n"+
			"Either add the type to seededBindings, or add it to metricsBindingExempt "+
			"with a rationale comment explaining why no binding is possible (e.g., "+
			"IAM-style control-plane, no native CloudWatch / Cloud Monitoring metrics).",
		len(missing), strings.Join(missing, "\n  "))
}

// TestExemptListNoStaleEntries guards against bit-rot in
// metricsBindingExempt. Every key must still appear in
// registry.SupportedDiscoverTypes(...) for SOME provider — removing a
// type from the live discoverer must also remove its exempt entry, so
// the list stays an accurate decision log instead of accumulating dead
// references.
func TestExemptListNoStaleEntries(t *testing.T) {
	t.Parallel()

	known := map[string]struct{}{}
	for _, p := range typeregistry.SupportedProviders() {
		for _, tfType := range typeregistry.SupportedDiscoverTypes(p) {
			known[tfType] = struct{}{}
		}
	}

	stale := []string{}
	for tfType := range metricsBindingExempt {
		if _, ok := known[tfType]; !ok {
			stale = append(stale, tfType)
		}
	}
	sort.Strings(stale)

	require.Empty(t, stale,
		"%d entries in metricsBindingExempt are not in registry.SupportedDiscoverTypes "+
			"for any provider — remove them from the exempt list:\n  %s",
		len(stale), strings.Join(stale, "\n  "))
}
