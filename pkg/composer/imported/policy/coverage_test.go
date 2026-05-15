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
	"aws_apigatewayv2_stage",
	"aws_bedrock_guardrail",
	"aws_bedrock_model_invocation_logging_configuration",
	"aws_cloudwatch_log_group",
	"aws_dynamodb_contributor_insights",
	"aws_dynamodb_table",
	// AWS drift coverage bundle 1 (#482) — high-value cloud-control-routed
	// types that already had Enrichable coverage but lacked a curated
	// Layer 2 policy.Map.
	"aws_iam_policy",
	"aws_iam_role",
	"aws_iam_role_policy_attachment",
	"aws_kms_key",
	"aws_lambda_function",
	"aws_lb",
	"aws_lb_listener",
	"aws_lb_target_group",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_route53_zone",
	"aws_s3_bucket",
	// S3 bucket sub-resources (#482 enricher push to 95%).
	"aws_s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_ownership_controls",
	"aws_s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning",
	"aws_secretsmanager_secret",
	"aws_security_group",
	"aws_service_discovery_private_dns_namespace",
	"aws_sqs_queue",
	"aws_subnet",
	"aws_vpc",
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
