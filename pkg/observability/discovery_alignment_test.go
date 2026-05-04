package observability

// TestComponentMetricsMapping_PresetResourcesAlignWithAction is the
// deepest of the four #253 mitigations: it asserts the resources a
// preset actually declares can be reached by the discovery action the
// panel is wired to.
//
// The Vertex AI bug in #253 is the canonical case — preset declares
// google_vertex_ai_dataset, ComponentMetricsMapping pointed at
// list-endpoints, and TestComponentMetricsMapping_ActionRegistered
// would NOT have caught it (list-endpoints IS a registered action,
// just for the wrong resource type). Only a check that crosses preset
// HCL with the discovery handler's actual SDK call surface flags
// "preset and discovery disagree about what resource type to look for."
//
// The (service, action) → expected resource types table below was
// hand-built from the per-action handlers in
// pkg/observability/discovery/{aws,gcp}/*.go. Adding a new action
// requires adding a row here too — any new entry without a row hits
// the "no expected types declared for this action" failure with a
// pointer to add coverage.

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
)

// expectedResourceTypesByAction maps a (service, action) pair to the
// Terraform resource types the discovery action is shaped to query.
// At least ONE of the listed types must appear as a `resource "<type>"`
// declaration in the preset's HCL — otherwise the panel will return
// empty even on a successful apply (the #253 Vertex AI shape).
var expectedResourceTypesByAction = map[string][]string{
	// AWS
	"aws/ec2/describe-instances":              {"aws_instance"},
	"aws/ecs/list-clusters":                   {"aws_ecs_cluster"},
	"aws/eks/list-clusters":                   {"aws_eks_cluster"},
	"aws/rds/describe-db-instances":           {"aws_db_instance"},
	"aws/elasticache/describe-cache-clusters": {"aws_elasticache_cluster", "aws_elasticache_replication_group"},
	"aws/s3/list-buckets":                     {"aws_s3_bucket"},
	"aws/dynamodb/list-tables":                {"aws_dynamodb_table"},
	"aws/sqs/list-queues":                     {"aws_sqs_queue"},
	"aws/msk/list-clusters":                   {"aws_msk_cluster"},
	"aws/cloudfront/list-distributions":       {"aws_cloudfront_distribution"},
	"aws/cloudwatchlogs/describe-log-groups":  {"aws_cloudwatch_log_group"},
	"aws/kms/list-keys":                       {"aws_kms_key"},
	"aws/secretsmanager/list-secrets":         {"aws_secretsmanager_secret"},
	"aws/cognito/list-user-pools":             {"aws_cognito_user_pool"},
	"aws/lambda/list-functions":               {"aws_lambda_function"},
	"aws/alb/describe-load-balancers":         {"aws_lb", "aws_alb"},
	"aws/waf/list-web-acls":                   {"aws_wafv2_web_acl", "aws_waf_web_acl"},
	"aws/apigateway/get-apis":                 {"aws_apigatewayv2_api", "aws_api_gateway_rest_api"},
	"aws/opensearch/describe-domains":         {"aws_opensearch_domain"},
	"aws/bedrock/list-knowledge-bases":        {"aws_bedrockagent_knowledge_base"},
	"aws/vpc/describe-vpcs":                   {"aws_vpc"},

	// GCP
	"gcp/compute/list-instances":              {"google_compute_instance", "google_compute_instance_template", "google_compute_instance_group_manager"},
	"gcp/gke/list-clusters":                   {"google_container_cluster"},
	"gcp/cloudsql/list-instances":             {"google_sql_database_instance"},
	"gcp/gcs/list-buckets":                    {"google_storage_bucket"},
	"gcp/cloudrun/list-services":              {"google_cloud_run_v2_service", "google_cloud_run_service"},
	"gcp/secretmanager/list-secrets":          {"google_secret_manager_secret"},
	"gcp/cloudkms/list-keyrings":              {"google_kms_key_ring"},
	"gcp/pubsub/list-topics":                  {"google_pubsub_topic"},
	"gcp/firestore/list-collections":          {"google_firestore_database"},
	"gcp/vpc/list-networks":                   {"google_compute_network"},
	"gcp/loadbalancer/list-url-maps":          {"google_compute_url_map"},
	"gcp/memorystore/list-instances":          {"google_redis_instance", "google_memcache_instance"},
	"gcp/cloudarmor/list-policies":            {"google_compute_security_policy"},
	"gcp/cloudbuild/list-triggers":            {"google_cloudbuild_trigger"},
	"gcp/cloudfunctions/list-functions":       {"google_cloudfunctions_function", "google_cloudfunctions2_function"},
	"gcp/identityplatform/list-tenants":       {"google_identity_platform_config", "google_identity_platform_tenant"},
	"gcp/vertexai/list-datasets":              {"google_vertex_ai_dataset"},
	"gcp/bastion/list-bastion-instances":      {"google_compute_instance"},
	"gcp/apigateway/list-apis":                {"google_api_gateway_api"},
	"gcp/cloudlogging/list-logs":              {"google_logging_project_sink", "google_logging_project_bucket_config"},
	"gcp/cloudmonitoring/list-alert-policies": {"google_monitoring_alert_policy"},
}

// presetMatchesActionAllowlist holds (component, reason) entries for
// components that legitimately don't declare a resource the action
// would query. Use sparingly — if you allowlist a real preset, you
// silence the check that catches the #253 Vertex AI shape.
//
// Two flavors of legitimate exemption:
//   - "wraps community module" — the preset uses `module "x" { source =
//     "terraform-google-modules/..." }` which creates the discovered
//     resource via the upstream module, not a top-level resource block.
//     The static check can't see through module sources.
//   - "panel binding is intentional surface, not the preset's primary
//     resource" — e.g. cloud_monitoring's panel queries alert policies
//     because that's what the user cares about; the preset itself owns
//     dashboards + notification channels and the alert policies live on
//     the per-component observability.tf files.
var presetMatchesActionAllowlist = map[composer.ComponentKey]string{
	// AWS placeholders that bind to ec2.describe-instances solely so
	// the panel's "Compute" view doesn't 404. The presets themselves
	// declare no resources (covered by emptyPresetAllowlist in the
	// composer package).
	composer.KeyAWSCodePipeline: "AWS placeholder preset; no resources, panel binding is conventional",
	composer.KeyAWSGrafana:      "AWS placeholder preset; no resources, panel binding is conventional",
	// AWS bastion delegates to a community module; the EC2 instance
	// it stands up is not declared as `resource "aws_instance"` here.
	composer.KeyAWSBastion: "bastion EC2 instance is created via the wrapped module, not a top-level resource block",
	// CloudWatch monitoring binds to log-groups for the panel default
	// but the preset's primary resources are CloudWatch alarms.
	composer.KeyAWSCloudWatchMonitoring: "panel binds to log-groups for the empty-state allowlist; preset declares CW alarm policies, not log groups",
	// Wrapped-module presets — the discovered resource is created by
	// the upstream community module, not a top-level resource block.
	composer.KeyAWSEKS:      "wraps terraform-aws-modules/eks/aws which creates aws_eks_cluster",
	composer.KeyAWSVPC:      "wraps terraform-aws-modules/vpc/aws which creates aws_vpc",
	composer.KeyAWSBedrock:  "knowledge base is provisioned via the wrapped module path; preset declares the surrounding IAM/log/guardrail surface",
	composer.KeyGCPGKE:      "wraps terraform-google-modules/kubernetes-engine/google which creates google_container_cluster",
	composer.KeyGCPCloudSQL: "wraps terraform-google-modules/sql-db/google which creates google_sql_database_instance",
	composer.KeyGCPVPC:      "wraps terraform-google-modules/network/google which creates google_compute_network",
	// Cloud Monitoring's panel queries alert policies (which live on
	// per-component observability.tf files), not the dashboards /
	// notification channels the preset itself owns.
	composer.KeyGCPCloudMonitoring: "panel binding intentionally points at alert policies emitted by per-component observability.tf, not the dashboard/channel resources the preset itself declares",
}

func TestComponentMetricsMapping_PresetResourcesAlignWithAction(t *testing.T) {
	t.Parallel()
	resourceRe := regexp.MustCompile(`(?m)^resource\s+"([^"]+)"\s+"[^"]+"\s*\{`)

	for k, binding := range ComponentMetricsMapping {
		t.Run(string(k), func(t *testing.T) {
			t.Parallel()
			if reason, exempt := presetMatchesActionAllowlist[k]; exempt {
				t.Logf("allowlisted: %s (%s)", k, reason)
				return
			}
			cloud := string(composer.CloudFor(k))
			tableKey := cloud + "/" + binding.Service + "/" + binding.Action
			expected, ok := expectedResourceTypesByAction[tableKey]
			require.True(t, ok,
				"expectedResourceTypesByAction has no entry for %q — every (service, action) bound from ComponentMetricsMapping needs a row so this gate has coverage. Add the row or, if the binding is an aggregator, add the component to presetMatchesActionAllowlist.",
				tableKey)
			if len(expected) == 0 {
				return
			}

			presetPath := composer.GetPresetPath(cloud, k, &composer.Components{})
			declared := map[string]bool{}
			entries, err := fs.ReadDir(terraformpresets.FS, presetPath)
			require.NoError(t, err, "ReadDir(%s)", presetPath)
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
					continue
				}
				body, err := fs.ReadFile(terraformpresets.FS, presetPath+"/"+e.Name())
				require.NoError(t, err)
				for _, m := range resourceRe.FindAllStringSubmatch(string(body), -1) {
					declared[m[1]] = true
				}
			}

			matched := false
			for _, want := range expected {
				if declared[want] {
					matched = true
					break
				}
			}
			assert.True(t, matched,
				"discovery binding %s.%s for %s expects one of %v, but %s declares no matching resource block (declared: %v). Either fix ComponentMetricsMapping[%s] to point at an action that matches what the preset creates (the #253 Vertex AI fix), update the preset to declare an expected resource type, or add %s to presetMatchesActionAllowlist with a justification.",
				binding.Service, binding.Action, k, expected, presetPath, sortedSetKeys(declared), k, k)
		})
	}
}

// TestPresetMatchesActionAllowlist_NotStale guards against allowlist
// entries that no longer correspond to a ComponentMetricsMapping
// binding.
func TestPresetMatchesActionAllowlist_NotStale(t *testing.T) {
	t.Parallel()
	for k := range presetMatchesActionAllowlist {
		_, ok := ComponentMetricsMapping[k]
		assert.True(t, ok,
			"presetMatchesActionAllowlist entry %q has no ComponentMetricsMapping binding — drop it",
			k)
	}
}

// sortedSetKeys returns the map's keys in sorted order for deterministic
// error-message output.
func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
