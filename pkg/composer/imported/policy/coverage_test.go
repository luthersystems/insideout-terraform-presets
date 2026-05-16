package policy

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// syntheticTypePrefix is the namespace used by registry_test.go for
// synthetic test-only tfTypes. Production tests in this package must
// filter out registrations carrying this prefix so concurrent test
// runs don't observe each other through RegisteredTypes() or LintAll().
const syntheticTypePrefix = "policy_test_"

// coveredTypes pins the exact set of import resource types that must
// have a Layer 2 policy registered. Adding or removing a type requires
// updating this list — the diff makes the surface change explicit.
var coveredTypes = []string{
	"aws_acm_certificate",
	// Bundle 11 (#482) — APIGW v1 deployment + CloudWatch log stream +
	// EKS access entry + EKS Fargate profile + ElastiCache parameter
	// group + EC2 key pair + S3 bucket policy + SNS topic subscription.
	// Pushes AWS DriftDetectable from 85% to ~92%.
	"aws_api_gateway_deployment",
	// AWS drift coverage bundle 4 (#482) — 10 more cloud-control-routed
	// types pushing DriftDetectable from 42% to ~51%.
	"aws_api_gateway_resource",
	// Bundle 7 (#482) — REST API v1 stage + APIGW v2
	// authorizer/integration/route, backup_plan, db_parameter_group,
	// ebs_volume, launch_template. Pushes AWS DriftDetectable past 56%.
	"aws_api_gateway_stage",
	// Bundle 8 (#482) — APIGW v2 parent API + custom domain name,
	// CloudFront OAI, EKS managed node group, inline iam_role_policy,
	// KMS alias, Lambda function URL, SSM Parameter Store entry. Pushes
	// AWS DriftDetectable from 63% to ~70%.
	"aws_apigatewayv2_api",
	// Bundle 9 (#482) — APIGW v2 API mapping (binds custom domain to
	// api+stage), cognito identity provider, lambda event source mapping,
	// network ACL, VPC DHCP options, VPC SG ingress/egress rules,
	// WAFv2 web ACL. Pushes AWS DriftDetectable from 71% to ~78%.
	"aws_apigatewayv2_api_mapping",
	"aws_apigatewayv2_authorizer",
	"aws_apigatewayv2_domain_name",
	"aws_apigatewayv2_integration",
	"aws_apigatewayv2_route",
	"aws_apigatewayv2_stage",
	"aws_appautoscaling_policy",
	"aws_appautoscaling_target",
	"aws_athena_workgroup",
	"aws_backup_plan",
	// Bundle 10 (#482) — Backup selection (plan → resource scoping),
	// CloudFront function, CloudWatch dashboard, Cognito user-pool
	// custom domain, ECS cluster capacity providers, EKS managed
	// add-on, ElastiCache subnet group, IAM service-linked role.
	// Pushes AWS DriftDetectable from 78% to ~85%.
	"aws_backup_selection",
	"aws_backup_vault",
	// Final-2 push (#482) — closes the last hand-rolled enrichers
	// (per-tag-on-ASG and resource-arn × web-acl-arn binding). Both
	// have curated Layer 2 policy.Maps with Exact drift semantics.
	"aws_autoscaling_group",
	"aws_autoscaling_group_tag",
	"aws_bedrock_guardrail",
	"aws_bedrock_model_invocation_logging_configuration",
	// Bundle 8 (cont.) — CloudFront OAI.
	"aws_cloudfront_origin_access_identity",
	// Bundle 4 (cont.) — CloudTrail.
	"aws_cloudtrail",
	// AWS drift coverage bundle 2 (#482) — cloud-control-routed types
	// in the RDS / compute / monitoring / managed-search family.
	"aws_cloudfront_distribution",
	// Bundle 10 (cont.) — CloudFront function.
	"aws_cloudfront_function",
	// Bundle 12 (#482) — CloudFront monitoring subscription +
	// CloudWatch log resource policy + Cognito resource server + EKS
	// pod identity association + OpenSearch Serverless collection.
	// Pushes AWS DriftDetectable from 93% past the ≥95% target.
	"aws_cloudfront_monitoring_subscription",
	// AWS drift coverage bundle 5 (#482) — 10 more cloud-control-routed
	// types pushing DriftDetectable further. Compute/container, RDS,
	// MSK, Glue, Cognito, Lambda alias/permission, IAM user, event bus.
	// Bundle 10 (cont.) — CloudWatch dashboard.
	"aws_cloudwatch_dashboard",
	"aws_cloudwatch_event_bus",
	"aws_cloudwatch_event_rule",
	"aws_cloudwatch_log_group",
	// Bundle 12 (cont.) — CloudWatch log resource policy.
	"aws_cloudwatch_log_resource_policy",
	// Bundle 11 (cont.) — CloudWatch log stream.
	"aws_cloudwatch_log_stream",
	"aws_cloudwatch_metric_alarm",
	"aws_codebuild_project",
	// Bundle 4 (cont.) — CodeDeploy app.
	"aws_codedeploy_app",
	"aws_codepipeline",
	// Bundle 9 (cont.) — Cognito identity provider.
	"aws_cognito_identity_provider",
	// Bundle 12 (cont.) — Cognito resource server.
	"aws_cognito_resource_server",
	// Bundle 5 (cont.) — Cognito user-pool client.
	"aws_cognito_user_pool_client",
	// Bundle 10 (cont.) — Cognito user-pool custom domain.
	"aws_cognito_user_pool_domain",
	"aws_db_instance",
	"aws_db_parameter_group",
	// Bundle 6 (#482) — RDS DB subnet group, EIP, IAM group, IAM
	// instance profile, internet gateway, NAT gateway, network
	// interface, route table.
	"aws_db_subnet_group",
	"aws_dynamodb_contributor_insights",
	// Bundle 4 (cont.) — DynamoDB global table.
	"aws_dynamodb_global_table",
	"aws_dynamodb_table",
	"aws_ebs_volume",
	"aws_ecs_cluster",
	// Bundle 10 (cont.) — ECS cluster capacity providers.
	"aws_ecs_cluster_capacity_providers",
	// Bundle 5 (cont.) — ECS service + task definition.
	"aws_ecs_service",
	"aws_ecs_task_definition",
	// Bundle 4 (cont.) — EFS file system.
	"aws_efs_file_system",
	"aws_eip",
	// Bundle 11 (cont.) — EKS access entry.
	"aws_eks_access_entry",
	// Bundle 10 (cont.) — EKS managed add-on.
	"aws_eks_addon",
	"aws_eks_cluster",
	// Bundle 11 (cont.) — EKS Fargate profile.
	"aws_eks_fargate_profile",
	// Bundle 8 (cont.) — EKS managed node group.
	"aws_eks_node_group",
	// Bundle 12 (cont.) — EKS pod identity association.
	"aws_eks_pod_identity_association",
	// Bundle 11 (cont.) — ElastiCache parameter group.
	"aws_elasticache_parameter_group",
	"aws_elasticache_replication_group",
	// Bundle 10 (cont.) — ElastiCache subnet group.
	"aws_elasticache_subnet_group",
	// Bundle 4 (cont.) — Glue catalog database (substituted in for
	// aws_cognito_user_pool which trips a codegen name collision).
	"aws_glue_catalog_database",
	// Bundle 5 (cont.) — Glue ETL job.
	"aws_glue_job",
	// AWS drift coverage bundle 1 (#482) — high-value cloud-control-routed
	// types that already had Enrichable coverage but lacked a curated
	// Layer 2 policy.Map.
	"aws_iam_group",
	"aws_iam_instance_profile",
	"aws_iam_policy",
	"aws_iam_role",
	// Bundle 8 (cont.) — inline iam_role_policy.
	"aws_iam_role_policy",
	"aws_iam_role_policy_attachment",
	// Bundle 10 (cont.) — IAM service-linked role.
	"aws_iam_service_linked_role",
	// Bundle 5 (cont.) — standalone IAM user.
	"aws_iam_user",
	// `aws_instance` is the canonical TF name for EC2 instances.
	"aws_instance",
	"aws_internet_gateway",
	// Bundle 11 (cont.) — EC2 key pair.
	"aws_key_pair",
	// Bundle 4 (cont.) — Kinesis Data Stream.
	"aws_kinesis_stream",
	// Bundle 8 (cont.) — KMS alias.
	"aws_kms_alias",
	"aws_kms_key",
	// Bundle 5 (cont.) — Lambda alias + permission.
	"aws_lambda_alias",
	// Bundle 9 (cont.) — Lambda event-source mapping.
	"aws_lambda_event_source_mapping",
	"aws_lambda_function",
	// Bundle 8 (cont.) — Lambda function URL.
	"aws_lambda_function_url",
	"aws_lambda_layer_version",
	"aws_lambda_permission",
	"aws_launch_template",
	"aws_lb",
	"aws_lb_listener",
	"aws_lb_target_group",
	// Bundle 2 (cont.) — managed-search / streaming / rotation types.
	"aws_msk_cluster",
	// Bundle 5 (cont.) — MSK broker-configuration revision.
	"aws_msk_configuration",
	"aws_nat_gateway",
	// Bundle 9 (cont.) — VPC network ACL.
	"aws_network_acl",
	"aws_network_interface",
	"aws_opensearch_domain",
	// Bundle 12 (cont.) — OpenSearch Serverless collection.
	"aws_opensearchserverless_collection",
	// Bundle 5 (cont.) — RDS Aurora / multi-AZ cluster.
	"aws_rds_cluster",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_route53_zone",
	"aws_route_table",
	"aws_s3_bucket",
	// S3 bucket sub-resources (#482 enricher push to 95%).
	"aws_s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_ownership_controls",
	// Bundle 11 (cont.) — S3 bucket policy.
	"aws_s3_bucket_policy",
	"aws_s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning",
	"aws_secretsmanager_secret",
	"aws_secretsmanager_secret_rotation",
	"aws_security_group",
	"aws_service_discovery_private_dns_namespace",
	"aws_sfn_state_machine",
	"aws_sns_topic",
	// Bundle 11 (cont.) — SNS topic subscription.
	"aws_sns_topic_subscription",
	"aws_sqs_queue",
	// Bundle 8 (cont.) — SSM Parameter Store entry.
	"aws_ssm_parameter",
	"aws_subnet",
	"aws_vpc",
	// Bundle 9 (cont.) — VPC DHCP option set.
	"aws_vpc_dhcp_options",
	"aws_vpc_endpoint",
	// Bundle 9 (cont.) — Modern SG egress/ingress rule resources + WAFv2 web ACL.
	"aws_vpc_security_group_egress_rule",
	"aws_vpc_security_group_ingress_rule",
	"aws_wafv2_web_acl",
	// Final-2 push (#482), continued — wafv2_web_acl_association in
	// alphabetical position at the end of the AWS block.
	"aws_wafv2_web_acl_association",
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_api_gateway_gateway",
	"google_cloud_run_v2_service",
	"google_cloudbuild_trigger",
	"google_cloudfunctions2_function",
	"google_firestore_database",
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
	"google_identity_platform_config",
	"google_identity_platform_default_supported_idp_config",
	"google_kms_crypto_key",
	"google_kms_key_ring",
	"google_logging_project_sink",
	"google_monitoring_alert_policy",
	"google_monitoring_dashboard",
	"google_monitoring_notification_channel",
	"google_project_service",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_redis_instance",
	"google_secret_manager_secret",
	"google_secret_manager_secret_version",
	"google_service_account",
	"google_service_networking_connection",
	"google_sql_database_instance",
	"google_sql_user",
	"google_storage_bucket",
	"google_storage_bucket_object",
	"google_vertex_ai_dataset",
	"google_vpc_access_connector",
	// IAM-binding types (#482 follow-up). Curated minimally — the (parent
	// × role × member) tuple is identity; `members` lists on _iam_binding
	// rows are the only non-identity field, edited via RequiresApproval.
	"google_cloud_run_v2_service_iam_member",
	"google_cloudfunctions2_function_iam_member",
	"google_kms_crypto_key_iam_binding",
	"google_project_iam_member",
	"google_secret_manager_secret_iam_binding",
	"google_secret_manager_secret_iam_member",
	"google_storage_bucket_iam_member",
}

func TestCoveredTypesHavePolicies(t *testing.T) {
	t.Parallel()
	for _, tfType := range coveredTypes {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			m, ok := Lookup(tfType)
			require.True(t, ok, "no policy registered for %q", tfType)
			require.NotEmpty(t, m, "policy for %q is empty", tfType)
		})
	}
}

// TestRegisteredTypes_CoveredSetExact filters out synthetic test
// registrations (the "policy_test_" prefix used by registry_test.go
// and lint_test.go helpers) and asserts the remaining production
// registrations match the covered set exactly. Adding or removing a
// production tfType requires a deliberate edit to coveredTypes.
func TestRegisteredTypes_CoveredSetExact(t *testing.T) {
	t.Parallel()
	got := RegisteredTypes()
	production := got[:0:0]
	for _, tfType := range got {
		if !strings.HasPrefix(tfType, syntheticTypePrefix) {
			production = append(production, tfType)
		}
	}
	want := append([]string(nil), coveredTypes...)
	sort.Strings(want)
	assert.Equal(t, want, production,
		"production policy registrations must equal coveredTypes exactly")
}

// TestPolicyRegistry_CoversGeneratedRegistry pins the invariant that
// every type registered in the Layer 1 typed-Attrs `generated` registry
// also has a Layer 2 policy registered. The two registries are
// independently populated (via WantedGoogle/WantedAWS driving codegen
// vs. hand-authored *.policy.go files), so a curator adding to
// WantedGoogle but forgetting the policy file would silently leave the
// new type with no axes — the wizard / interactive agent would fall back to default
// behavior, defeating the bundle's purpose.
//
// Symmetric to TestRegisteredTypes_PhaseSetExact, which guards the
// other direction (no orphan policies without a generated struct).
func TestPolicyRegistry_CoversGeneratedRegistry(t *testing.T) {
	t.Parallel()
	gen := generated.RegisteredTypes()
	pol := RegisteredTypes()
	production := pol[:0:0]
	for _, tfType := range pol {
		if !strings.HasPrefix(tfType, syntheticTypePrefix) {
			production = append(production, tfType)
		}
	}
	sort.Strings(gen)
	sort.Strings(production)
	assert.Equal(t, gen, production,
		"every generated.RegisteredTypes() entry must have a Layer 2 policy "+
			"registered (and vice versa). If you added a type to WantedGoogle "+
			"or WantedAWS, also author a corresponding *.policy.go file and "+
			"extend coveredTypes.")
}

// TestTagsIntentionallyUncurated pins the deliberate gap documented in
// google_compute_instance.policy.go and google_cloudbuild_trigger.policy.go.
// Neither type's `tags` attribute is a label:
//
//   - compute_instance.tags drives firewall source_tags / target_tags
//     (operationally meaningful network selectors)
//   - cloudbuild_trigger.tags is a free-text set of operator annotations
//
// But lint.go's `tagAttrSuffixes` hardcodes `"tags"` as label-shaped, so
// any non-SystemOnly curation trips CodeTagFieldNotSystemOnly while
// tagPolicy() (SystemOnly+Hidden+Redacted) is semantically wrong for both.
// Until the lint exemption lands, the attrs stay uncurated.
//
// This test fires if a well-meaning curator adds the entry back.
func TestTagsIntentionallyUncurated(t *testing.T) {
	t.Parallel()
	cases := []string{
		"google_compute_instance",
		"google_cloudbuild_trigger",
	}
	for _, tfType := range cases {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			m, ok := Lookup(tfType)
			require.True(t, ok, "%s policy must be registered", tfType)
			_, present := m["tags"]
			assert.False(t, present,
				"%s.tags must remain uncurated — see policy file header for the lint.go::tagAttrSuffixes follow-up", tfType)
		})
	}
}

func TestLintAll_Clean(t *testing.T) {
	t.Parallel()
	for _, tfType := range coveredTypes {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			issues := Lint(tfType)
			if len(issues) == 0 {
				return
			}
			lines := make([]string, 0, len(issues))
			for _, i := range issues {
				lines = append(lines, i.String())
			}
			t.Fatalf("policy for %q lints with %d issue(s):\n%s",
				tfType, len(issues), strings.Join(lines, "\n"))
		})
	}
}

// TestKnownPathsNoShrink locks the curated Layer 2 surface against
// silent erosion. Every PR that adds, removes, or renames a policy
// path must also bump testdata/known_paths.golden so the diff is
// explicit. Set UPDATE_GOLDEN=1 to seed.
func TestKnownPathsNoShrink(t *testing.T) {
	t.Parallel()

	goldenPath := filepath.Join("testdata", "known_paths.golden")
	current := snapshot()

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(current), 0o644))
		t.Logf("wrote golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err,
		"golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/composer/imported/policy/ -run TestKnownPathsNoShrink`")
	require.Equal(t, string(want), current,
		"policy surface drifted from %s. If this is intentional, re-seed via UPDATE_GOLDEN=1.",
		goldenPath)
}

// snapshot emits a sorted, stable text representation of the entire
// curated policy surface for diffing purposes. Format:
//
//	<tfType>\t<path>\t<Role>\t<Pillar>\t<Visibility>\t<Edit>\t<Sensitivity>\t<ChangeRisk>\n
//
// Rationale is intentionally NOT included — it is freeform prose that
// would dirty the diff for non-surface changes.
func snapshot() string {
	tfTypes := RegisteredTypes()
	var b strings.Builder
	for _, t := range tfTypes {
		// Skip synthetic types that may have leaked from earlier tests.
		if !isCovered(t) {
			continue
		}
		m, _ := Lookup(t)
		paths := make([]string, 0, len(m))
		for p := range m {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fp := m[p]
			b.WriteString(t)
			b.WriteByte('\t')
			b.WriteString(p)
			b.WriteByte('\t')
			b.WriteString(string(fp.Role))
			b.WriteByte('\t')
			b.WriteString(string(fp.Pillar))
			b.WriteByte('\t')
			b.WriteString(string(fp.Visibility))
			b.WriteByte('\t')
			b.WriteString(string(fp.Edit))
			b.WriteByte('\t')
			b.WriteString(string(fp.Sensitivity))
			b.WriteByte('\t')
			b.WriteString(string(fp.ChangeRisk))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func isCovered(tfType string) bool {
	return slices.Contains(coveredTypes, tfType)
}
