// The literal expected slices in TestSupportedDiscoverTypes_AWS_ReturnsCanonicalSortedList
// and TestSupportedDiscoverTypes_GCP_ReturnsCanonicalSortedList are the
// authoritative pin for the public-API contract: any change to the supported
// type set must be reflected here. The parity tests in
// cmd/insideout-import/awsdiscover and cmd/insideout-import/gcpdiscover only
// guard drift between the two sources of truth — they do not pin literal
// values, by design (we don't want the awsdiscover/gcpdiscover packages to
// reach across the import boundary to assert what reliable should expect).
package registry

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestSupportedDiscoverTypes_AWS_ReturnsCanonicalSortedList(t *testing.T) {
	t.Parallel()
	want := []string{
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
	got := SupportedDiscoverTypes(ProviderAWS)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedDiscoverTypes(%q) = %v, want %v", ProviderAWS, got, want)
	}
}

func TestSupportedDiscoverTypes_GCP_ReturnsCanonicalSortedList(t *testing.T) {
	t.Parallel()
	want := []string{
		"google_api_gateway_api",
		"google_api_gateway_api_config",
		"google_api_gateway_gateway",
		"google_cloud_run_v2_service",
		"google_cloud_run_v2_service_iam_member",
		"google_cloudbuild_trigger",
		"google_cloudfunctions2_function",
		"google_cloudfunctions2_function_iam_member",
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
		"google_kms_crypto_key_iam_binding",
		"google_kms_key_ring",
		"google_logging_project_sink",
		"google_monitoring_alert_policy",
		"google_monitoring_dashboard",
		"google_monitoring_notification_channel",
		"google_project_iam_member",
		"google_pubsub_subscription",
		"google_pubsub_topic",
		"google_redis_instance",
		"google_secret_manager_secret",
		"google_secret_manager_secret_iam_binding",
		"google_secret_manager_secret_iam_member",
		"google_service_account",
		"google_sql_database_instance",
		"google_sql_user",
		"google_storage_bucket",
		"google_storage_bucket_iam_member",
		"google_vertex_ai_dataset",
	}
	got := SupportedDiscoverTypes(ProviderGCP)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedDiscoverTypes(%q) = %v, want %v", ProviderGCP, got, want)
	}
}

// TestSupportedDiscoverTypes_UnknownProvider_ReturnsNil pins the nil (vs
// empty-slice) contract documented on SupportedDiscoverTypes. JSON consumers
// rely on this — `null` and `[]` are not interchangeable in the reliable
// wizard's payloads.
func TestSupportedDiscoverTypes_UnknownProvider_ReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider string
	}{
		{name: "empty", provider: ""},
		{name: "azure", provider: "azure"},
		{name: "AWS_uppercase", provider: "AWS"},
		{name: "whitespace", provider: " aws "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SupportedDiscoverTypes(tc.provider)
			if got != nil {
				t.Errorf("SupportedDiscoverTypes(%q) = %v, want nil (not empty slice)", tc.provider, got)
			}
		})
	}
}

// TestSupportedDiscoverTypes_ReturnsCopy_PackageStateUnchanged proves the
// stronger invariant that callers cannot mutate the package's internal slice
// through the returned value. Asserting against literal first elements + a
// pointer-identity check is mutation-resistant against subtle implementation
// regressions like `return s[:len(s):len(s)]` (which would share the backing
// array even though the slice headers differ).
func TestSupportedDiscoverTypes_ReturnsCopy_PackageStateUnchanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		// firstLiteral is the canonical first element of the sorted list.
		// If a mutation leaks back into the package, a subsequent call's
		// [0] no longer matches.
		firstLiteral string
	}{
		{provider: ProviderAWS, firstLiteral: "aws_acm_certificate"},
		{provider: ProviderGCP, firstLiteral: "google_api_gateway_api"}, // first entry after Bundle 10
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			first := SupportedDiscoverTypes(tc.provider)
			if len(first) == 0 {
				t.Fatalf("expected non-empty list for provider %q", tc.provider)
			}
			if first[0] != tc.firstLiteral {
				t.Fatalf("first element drift: got %q, want %q (test data needs updating)", first[0], tc.firstLiteral)
			}
			first[0] = "MUTATED"

			second := SupportedDiscoverTypes(tc.provider)
			if second[0] != tc.firstLiteral {
				t.Errorf("mutation leaked into package state: second call [0] = %q, want %q", second[0], tc.firstLiteral)
			}
			if &first[0] == &second[0] {
				t.Error("returned slices share backing array; not a real copy")
			}
		})
	}
}

func TestSupportedDiscoverTypes_AllProviders_AreSorted(t *testing.T) {
	t.Parallel()
	for _, provider := range SupportedProviders() {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			got := SupportedDiscoverTypes(provider)
			if !sort.StringsAreSorted(got) {
				t.Errorf("SupportedDiscoverTypes(%q) not sorted: %v", provider, got)
			}
		})
	}
}

// TestSupportedProviders_RoundTripsThroughSupportedDiscoverTypes guards the
// invariant promised in SupportedProviders' doc comment: every provider key
// it advertises must map to a non-empty SupportedDiscoverTypes result. A new
// provider added to the SupportedDiscoverTypes switch but missed in
// SupportedProviders (or vice versa) fails here instead of silently breaking
// downstream UIs that enumerate providers via SupportedProviders().
func TestSupportedProviders_RoundTripsThroughSupportedDiscoverTypes(t *testing.T) {
	t.Parallel()
	providers := SupportedProviders()
	if len(providers) == 0 {
		t.Fatal("SupportedProviders returned empty list — registry would be unusable")
	}
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			got := SupportedDiscoverTypes(p)
			if len(got) == 0 {
				t.Errorf("SupportedProviders advertises %q but SupportedDiscoverTypes(%q) returned no types", p, p)
			}
		})
	}
}

func TestSupportedProviders_ReturnsBothCloudKeysSorted(t *testing.T) {
	t.Parallel()
	want := []string{ProviderAWS, ProviderGCP}
	got := SupportedProviders()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedProviders() = %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("SupportedProviders() not sorted: %v", got)
	}
}

// TestSupportedDiscoverTypes_ConcurrentAccess_IsRaceFree pins the documented
// goroutine-safety contract by running concurrent callers under -race. The
// package is safe by construction today (only stateless reads + per-call
// allocation), but a future "optimization" that caches the slice and returns
// the cached copy directly would silently break this — and surface as a race
// here. Each goroutine mutates its own returned slice to maximize the chance
// that a buggy shared-state implementation trips the race detector.
func TestSupportedDiscoverTypes_ConcurrentAccess_IsRaceFree(t *testing.T) {
	t.Parallel()
	const goroutines = 64
	providers := SupportedProviders()
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for _, p := range providers {
				got := SupportedDiscoverTypes(p)
				if len(got) == 0 {
					t.Errorf("concurrent caller got empty list for %q", p)
					return
				}
				got[0] = "MUTATED-BY-GOROUTINE"
			}
		}()
	}
	wg.Wait()

	for _, p := range providers {
		got := SupportedDiscoverTypes(p)
		if len(got) > 0 && got[0] == "MUTATED-BY-GOROUTINE" {
			t.Errorf("concurrent mutation leaked into package state for provider %q", p)
		}
	}
}
