package permissions

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// awsTFTypeToServiceSlug mirrors awsdiscover.serviceSlugByTFType for
// use in the manifest-coverage test. Drift between this table and the
// awsdiscover slug map would otherwise surface only via aws.json
// coverage gaps — this map's per-TF-type-slug correspondence is
// independently pinned by TestSlugMap_MatchesAwsdiscover below, which
// uses the exported awsdiscover.ServiceSlug to fence the table off
// against the production source of truth.
var awsTFTypeToServiceSlug = map[string]string{
	"aws_acm_certificate":                                "acm_certificate",
	"aws_api_gateway_deployment":                         "api_gateway_deployment",
	"aws_api_gateway_resource":                           "api_gateway_resource",
	"aws_api_gateway_stage":                              "api_gateway_stage",
	"aws_apigatewayv2_api":                               "apigatewayv2_api",
	"aws_apigatewayv2_api_mapping":                       "apigatewayv2_api_mapping",
	"aws_apigatewayv2_authorizer":                        "apigatewayv2_authorizer",
	"aws_apigatewayv2_domain_name":                       "apigatewayv2_domain_name",
	"aws_apigatewayv2_integration":                       "apigatewayv2_integration",
	"aws_apigatewayv2_route":                             "apigatewayv2_route",
	"aws_apigatewayv2_stage":                             "apigatewayv2_stage",
	"aws_autoscaling_group":                              "autoscaling_group",
	"aws_autoscaling_group_tag":                          "autoscaling_group_tag",
	"aws_backup_plan":                                    "backup_plan",
	"aws_backup_selection":                               "backup_selection",
	"aws_backup_vault":                                   "backup_vault",
	"aws_bedrock_guardrail":                              "bedrock_guardrail",
	"aws_bedrock_model_invocation_logging_configuration": "bedrock_model_invocation_logging",
	"aws_cloudfront_distribution":                        "cloudfront_distribution",
	"aws_cloudfront_function":                            "cloudfront_function",
	"aws_cloudfront_monitoring_subscription":             "cloudfront_monitoring_subscription",
	"aws_cloudfront_origin_access_identity":              "cloudfront_origin_access_identity",
	"aws_cloudwatch_dashboard":                           "cloudwatch_dashboard",
	"aws_cloudwatch_event_rule":                          "cloudwatch_event_rule",
	"aws_cloudwatch_log_group":                           "cloudwatchlogs",
	"aws_cloudwatch_log_resource_policy":                 "cloudwatch_log_resource_policy",
	"aws_cloudwatch_log_stream":                          "cloudwatch_log_stream",
	"aws_cloudwatch_metric_alarm":                        "cloudwatch_metric_alarm",
	"aws_cognito_identity_provider":                      "cognito_identity_provider",
	"aws_cognito_resource_server":                        "cognito_resource_server",
	"aws_cognito_user_pool":                              "cognito_user_pool",
	"aws_cognito_user_pool_client":                       "cognito_user_pool_client",
	"aws_cognito_user_pool_domain":                       "cognito_user_pool_domain",
	"aws_db_instance":                                    "db_instance",
	"aws_db_parameter_group":                             "db_parameter_group",
	"aws_db_subnet_group":                                "db_subnet_group",
	"aws_dynamodb_contributor_insights":                  "dynamodb_contributor_insights",
	"aws_dynamodb_table":                                 "dynamodb",
	"aws_ebs_volume":                                     "ebs_volume",
	"aws_ecs_cluster":                                    "ecs_cluster",
	"aws_ecs_cluster_capacity_providers":                 "ecs_cluster_capacity_providers",
	"aws_eip":                                            "eip",
	"aws_eks_access_entry":                               "eks_access_entry",
	"aws_eks_addon":                                      "eks_addon",
	"aws_eks_cluster":                                    "eks_cluster",
	"aws_eks_fargate_profile":                            "eks_fargate_profile",
	"aws_eks_node_group":                                 "eks_node_group",
	"aws_eks_pod_identity_association":                   "eks_pod_identity",
	"aws_elasticache_parameter_group":                    "elasticache_parameter_group",
	"aws_elasticache_replication_group":                  "elasticache_replication_group",
	"aws_elasticache_subnet_group":                       "elasticache_subnet_group",
	"aws_iam_group":                                      "iam_group",
	"aws_iam_instance_profile":                           "iam_instance_profile",
	"aws_iam_policy":                                     "iam_policy",
	"aws_iam_role":                                       "iam_role",
	"aws_iam_role_policy":                                "iam_role_policy",
	"aws_iam_role_policy_attachment":                     "iam_role_policy_attachment",
	"aws_iam_service_linked_role":                        "iam_service_linked_role",
	"aws_iam_user":                                       "iam_user",
	"aws_instance":                                       "instance",
	"aws_internet_gateway":                               "internet_gateway",
	"aws_key_pair":                                       "key_pair",
	"aws_kms_alias":                                      "kms_alias",
	"aws_kms_key":                                        "kms",
	"aws_lambda_alias":                                   "lambda_alias",
	"aws_lambda_event_source_mapping":                    "lambda_event_source_mapping",
	"aws_lambda_function":                                "lambda",
	"aws_lambda_function_url":                            "lambda_function_url",
	"aws_lambda_permission":                              "lambda_permission",
	"aws_launch_template":                                "launch_template",
	"aws_lb":                                             "lb",
	"aws_lb_listener":                                    "lb_listener",
	"aws_lb_target_group":                                "lb_target_group",
	"aws_msk_cluster":                                    "msk_cluster",
	"aws_msk_configuration":                              "msk_configuration",
	"aws_nat_gateway":                                    "nat_gateway",
	"aws_network_acl":                                    "network_acl",
	"aws_network_interface":                              "network_interface",
	"aws_opensearch_domain":                              "opensearch_domain",
	"aws_opensearchserverless_access_policy":             "opensearchserverless_access_policy",
	"aws_opensearchserverless_collection":                "opensearchserverless_collection",
	"aws_opensearchserverless_security_policy":           "opensearchserverless_security_policy",
	"aws_resourceexplorer2_index":                        "resourceexplorer2_index",
	"aws_resourceexplorer2_view":                         "resourceexplorer2_view",
	"aws_route53_zone":                                   "route53_zone",
	"aws_route_table":                                    "route_table",
	"aws_s3_bucket":                                      "s3",
	"aws_s3_bucket_lifecycle_configuration":              "s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_ownership_controls":                   "s3_bucket_ownership_controls",
	"aws_s3_bucket_policy":                               "s3_bucket_policy",
	"aws_s3_bucket_public_access_block":                  "s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration": "s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning":                           "s3_bucket_versioning",
	"aws_secretsmanager_secret":                          "secretsmanager",
	"aws_secretsmanager_secret_rotation":                 "secretsmanager_secret_rotation",
	"aws_security_group":                                 "security_group",
	"aws_service_discovery_private_dns_namespace":        "service_discovery_private_dns_namespace",
	"aws_sns_topic":                                      "sns_topic",
	"aws_sns_topic_subscription":                         "sns_topic_subscription",
	"aws_sqs_queue":                                      "sqs",
	"aws_ssm_parameter":                                  "ssm_parameter",
	"aws_subnet":                                         "subnet",
	"aws_vpc":                                            "vpc",
	"aws_vpc_dhcp_options":                               "vpc_dhcp_options",
	"aws_vpc_endpoint":                                   "vpc_endpoint",
	"aws_wafv2_web_acl":                                  "wafv2_web_acl",
	"aws_wafv2_web_acl_association":                      "wafv2_web_acl_association",
}

// TestSlugMap_MatchesAwsdiscover pins the test-local slug table
// against the production awsdiscover.ServiceSlug map. Any TF type the
// registry exposes must round-trip identically via both. A drift
// (e.g. someone renames "iam_role" to "iam-role" in awsdiscover
// without updating this test or aws.json) surfaces here as a
// per-type per-table failure pair.
func TestSlugMap_MatchesAwsdiscover(t *testing.T) {
	t.Parallel()
	for _, tfType := range registry.SupportedDiscoverTypes(registry.ProviderAWS) {
		want := awsdiscover.ServiceSlug(tfType)
		got, ok := awsTFTypeToServiceSlug[tfType]
		if !ok {
			t.Errorf("awsTFTypeToServiceSlug missing entry for registry type %q (production says slug=%q); add it",
				tfType, want)
			continue
		}
		if got != want {
			t.Errorf("slug drift for %q: test-local map says %q, awsdiscover.ServiceSlug says %q", tfType, got, want)
		}
	}
}

func TestLoadAWSManifest_ParsesAndIsNonEmpty(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("Version: got %d, want 1", m.Version)
	}
	if m.Provider != "aws" {
		t.Errorf("Provider: got %q, want %q", m.Provider, "aws")
	}
	if len(m.Permissions) == 0 {
		t.Fatal("Permissions: empty slice; aws.json must declare at least one entry per discoverer")
	}
}

func TestLoadGCPManifest_ParsesAndIsNonEmpty(t *testing.T) {
	t.Parallel()

	m, err := LoadGCPManifest()
	if err != nil {
		t.Fatalf("LoadGCPManifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("Version: got %d, want 1", m.Version)
	}
	if m.Provider != "gcp" {
		t.Errorf("Provider: got %q, want %q", m.Provider, "gcp")
	}
	if len(m.Permissions) == 0 {
		t.Fatal("Permissions: empty slice; gcp.json must declare at least one entry")
	}
}

// TestAWSManifest_CoversAllAWSDiscovererServices asserts every service
// slug derived from registry.SupportedDiscoverTypes("aws") appears at
// least once in the AWS manifest. Adding a new TF type to the registry
// (and therefore a new per-service discoverer) without updating aws.json
// fails this test — the credential probe in reliable would silently
// under-test the new service otherwise.
func TestAWSManifest_CoversAllAWSDiscovererServices(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}

	slugInManifest := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		slugInManifest[p.Service] = true
	}

	tfTypes := registry.SupportedDiscoverTypes(registry.ProviderAWS)
	if len(tfTypes) == 0 {
		t.Fatal("registry.SupportedDiscoverTypes(aws) returned empty; manifest coverage cannot be checked")
	}
	for _, tfType := range tfTypes {
		slug, ok := awsTFTypeToServiceSlug[tfType]
		if !ok {
			t.Errorf("awsTFTypeToServiceSlug missing entry for %q (registry exposes it but the test slug map does not); update awsTFTypeToServiceSlug AND aws.json", tfType)
			continue
		}
		if !slugInManifest[slug] {
			t.Errorf("aws.json: missing permission entry for service slug %q (TF type %q); add at least one IAM action row", slug, tfType)
		}
	}

	// sts is a one-call-per-run dependency that isn't tied to a TF
	// type via the registry, but the credential probe still needs it
	// — gate explicitly so the manifest can't drop sts:GetCallerIdentity.
	if !slugInManifest["sts"] {
		t.Error("aws.json: missing sts entry; sts:GetCallerIdentity is called once per discover run to stamp AccountID")
	}
}

// TestGCPManifest_CoversCloudAssetInventory asserts the single
// cloud_asset_inventory row that powers every GCP discoverer is present.
// If a future PR breaks GCP into per-API discoverers, this test should
// be updated alongside that change rather than removed wholesale.
func TestGCPManifest_CoversCloudAssetInventory(t *testing.T) {
	t.Parallel()

	m, err := LoadGCPManifest()
	if err != nil {
		t.Fatalf("LoadGCPManifest: %v", err)
	}
	for _, p := range m.Permissions {
		if p.Service == "cloud_asset_inventory" {
			if p.GCPRole != "roles/cloudasset.viewer" {
				t.Errorf("cloud_asset_inventory: GCPRole=%q, want roles/cloudasset.viewer", p.GCPRole)
			}
			if p.IAMPermission != "cloudasset.assets.searchAllResources" {
				t.Errorf("cloud_asset_inventory: IAMPermission=%q, want cloudasset.assets.searchAllResources", p.IAMPermission)
			}
			return
		}
	}
	t.Error("gcp.json: missing cloud_asset_inventory entry; SearchAllResources is the only API every GCP discoverer calls")
}

// TestAWSManifest_DeterministicSortedByServiceAndAction protects the
// byte-stability invariant: the on-disk JSON must be sorted by
// (service, iam_action) so a downstream byte-comparison probe (e.g.
// reliable's CI gate that checksums the embedded blob) doesn't churn
// when an unrelated PR touches the file. A regression that re-orders
// entries without re-sorting fails this test.
func TestAWSManifest_DeterministicSortedByServiceAndAction(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}

	want := make([]Permission, len(m.Permissions))
	copy(want, m.Permissions)
	sort.SliceStable(want, func(i, j int) bool {
		if want[i].Service != want[j].Service {
			return want[i].Service < want[j].Service
		}
		return want[i].IAMAction < want[j].IAMAction
	})
	for i := range m.Permissions {
		if m.Permissions[i] != want[i] {
			t.Fatalf("aws.json entry %d unsorted: got (service=%q, action=%q), want (service=%q, action=%q); re-sort the file by (service, iam_action)",
				i, m.Permissions[i].Service, m.Permissions[i].IAMAction, want[i].Service, want[i].IAMAction)
		}
	}
}

// TestAWSManifest_NoUnknownPurposeStrings asserts every entry carries a
// non-empty Purpose. Reliable's wizard surfaces Purpose verbatim when
// explaining a missing-permission failure to the operator; an empty
// string would render a blank explanation row.
func TestAWSManifest_NoUnknownPurposeStrings(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}
	for i, p := range m.Permissions {
		if p.Purpose == "" {
			t.Errorf("aws.json entry %d (service=%q, action=%q): empty Purpose", i, p.Service, p.IAMAction)
		}
	}
}

// TestGCPManifest_NoUnknownPurposeStrings is the GCP-side mirror of the
// purpose-non-empty contract.
func TestGCPManifest_NoUnknownPurposeStrings(t *testing.T) {
	t.Parallel()

	m, err := LoadGCPManifest()
	if err != nil {
		t.Fatalf("LoadGCPManifest: %v", err)
	}
	for i, p := range m.Permissions {
		if p.Purpose == "" {
			t.Errorf("gcp.json entry %d (service=%q, role=%q): empty Purpose", i, p.Service, p.GCPRole)
		}
	}
}

// TestManifests_RoundTripJSON sanity-checks that loaded manifests
// re-marshal to valid JSON without losing fields. Catches struct-tag
// typos that would silently drop a field on either the read or write
// path.
func TestManifests_RoundTripJSON(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		load func() (Manifest, error)
	}{
		{"aws", LoadAWSManifest},
		{"gcp", LoadGCPManifest},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := tc.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var round Manifest
			if err := json.Unmarshal(b, &round); err != nil {
				t.Fatalf("unmarshal round-trip: %v", err)
			}
			if round.Version != m.Version || round.Provider != m.Provider {
				t.Errorf("round-trip drift: got %+v, want %+v", round, m)
			}
			if len(round.Permissions) != len(m.Permissions) {
				t.Errorf("round-trip permission count: got %d, want %d", len(round.Permissions), len(m.Permissions))
			}
		})
	}
}
