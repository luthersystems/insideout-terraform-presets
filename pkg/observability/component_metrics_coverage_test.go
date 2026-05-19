package observability

// component_metrics_coverage_test.go gates the silent gap discovered
// during the #598 / #614 / #618 / #620 audit: ComponentMetricsMapping
// is the source of truth for which (service, action) the inspector
// dispatcher invokes to populate a component's panel. Three back-to-
// back parity PRs landed without adding entries here, and there was
// no test forcing the omission to be acknowledged — the affected
// components silently rendered "no observable resources."
//
// This test fails when an AWS/GCP ComponentKey in
// composer.AllComponentKeys is missing from ComponentMetricsMapping,
// unless the key is in metricsDeferredKeys (with a tracked-issue
// rationale) or in metricsNonComponentKeys (genuinely not panel-
// renderable — IAM-only components, sub-keys priced under a parent,
// etc.). Adding to either allowlist requires a code change reviewers
// will see.

import (
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/require"
)

// metricsDeferredKeys are AWS/GCP ComponentKeys whose
// ComponentMetricsMapping entry is intentionally absent today. Adding
// or extending an entry here MUST cite the tracking issue.
//
// Two cohorts:
//  1. Pre-existing historical drift — keys that have lacked a panel
//     mapping since before #598/#614/#618/#620. Documented here so
//     the gap is now at least visible. Backfilled in a separate PR.
//  2. New parity-roll-up keys — #614 / #618 / #620 landed without a
//     mapping AND without a corresponding `<service>` entry in
//     AWSServiceActions / GCPServiceActions. Adding a mapping
//     requires registering the service + its actions first.
//
// Trim back as backfills land.
var metricsDeferredKeys = map[composer.ComponentKey]string{
	// Cohort 1: pre-existing historical drift. All tracked in #622.
	composer.KeyAWSACM:           "Pre-existing (tracked in #622): ACM has no panel mapping. Listing certificates is the natural surface but it was not wired when the ACM preset (#280) landed.",
	composer.KeyAWSBackups:       "Pre-existing (tracked in #622): aws_backups panel surface unclear (vault list vs plan list vs selection list). The [no-inspector] allowlist in extractors_drift_test.go acknowledges the parallel discovery gap.",
	composer.KeyAWSGitHubActions: "Pre-existing (tracked in #622): IAM-only component, no panel resource. Either belongs in metricsNonComponentKeys permanently OR needs an IAM-list mapping for the OIDC provider.",
	composer.KeyAWSRoute53:       "Pre-existing (tracked in #622): Route 53 has a discovery dispatcher (#596 — route53.go) but no panel mapping. list-hosted-zones is the natural surface.",
	composer.KeyGCPBackups:       "Pre-existing (tracked in #622): gcp_backups panel surface unclear, parallel to aws_backups deferral above.",
	composer.KeyGCPCloudDNS:      "Pre-existing (tracked in #622): Cloud DNS has a discovery dispatcher (#596 — gcp/dns.go) but no panel mapping. list-managed-zones is the natural surface.",

	// Cohort 2: parity-roll-up — needs both ComponentMetricsMapping
	// entry AND a matching service registered in service_actions.go.
	// All tracked in #622.
	composer.KeyGCPCloudDeploy: "Backfill ComponentMetricsMapping + service_actions.go for gcp_cloud_deploy (#614 / tracked in #622). Natural surface: list-delivery-pipelines.",
	composer.KeyAWSSageMaker:   "Backfill ComponentMetricsMapping + service_actions.go for aws_sagemaker (#615 / #618 / tracked in #622). Natural surface: list-domains.",
	composer.KeyAWSAppRunner:   "Backfill ComponentMetricsMapping + service_actions.go for aws_apprunner (#598 / #620 / tracked in #622). Natural surface: list-services.",
}

// metricsNonComponentKeys are AllComponentKeys entries that genuinely
// do NOT correspond to a panel-renderable resource. Distinct from
// metricsDeferredKeys: these are by-design, not pending work.
var metricsNonComponentKeys = map[composer.ComponentKey]bool{
	// Auto-included node group is covered by the parent KeyAWSEKS row;
	// the dispatcher routes both keys through the same eks panel.
	composer.KeyAWSEKSNodeGroup: true,
}

// TestComponentMetricsMappingCoversAllComponentKeys fails when an
// AWS/GCP ComponentKey in composer.AllComponentKeys lacks an entry
// in ComponentMetricsMapping.
//
// The dispatcher returns "unsupportedServiceError" for unmapped keys;
// the panel falls through to "no observable resources" with no
// indication of whether the gap is a deploy failure or a missing
// mapping. Customers see a healthy deploy report no observability —
// hard to attribute without a coverage test that fails CI.
func TestComponentMetricsMappingCoversAllComponentKeys(t *testing.T) {
	t.Parallel()

	var missing []string
	for _, k := range composer.AllComponentKeys {
		s := string(k)
		if !strings.HasPrefix(s, "aws_") && !strings.HasPrefix(s, "gcp_") {
			continue
		}
		if metricsNonComponentKeys[k] {
			continue
		}
		if _, ok := metricsDeferredKeys[k]; ok {
			require.NotEmptyf(t, metricsDeferredKeys[k],
				"metricsDeferredKeys[%q] must carry a non-empty issue-tracked rationale", k)
			continue
		}
		if _, ok := ComponentMetricsMapping[k]; !ok {
			missing = append(missing, s)
		}
	}
	require.Empty(t, missing,
		"ComponentMetricsMapping is missing entries for these AWS/GCP ComponentKeys: %v\n"+
			"Fix: add an entry in component_metrics.go,\n"+
			"OR add the key to metricsDeferredKeys with a tracked-issue rationale.\n"+
			"Note: adding a mapping may also require registering the service in pkg/observability/service_actions.go.",
		missing)

	// Sanity: an entry in metricsDeferredKeys must NOT also have a
	// ComponentMetricsMapping entry (would mean the backfill landed
	// without clearing the allowlist).
	for k := range metricsDeferredKeys {
		if _, ok := ComponentMetricsMapping[k]; ok {
			require.Failf(t,
				"deferred key has a mapping",
				"%q appears in metricsDeferredKeys AND in ComponentMetricsMapping — delete the allowlist entry; the backfill has landed",
				k)
		}
	}
}

// TestComponentMetricsMappingDeferralReferencesIssues guards against
// allowlist entries with no tracked-issue reference, matching the
// pricing coverage test pattern.
func TestComponentMetricsMappingDeferralReferencesIssues(t *testing.T) {
	t.Parallel()
	for k, reason := range metricsDeferredKeys {
		require.Truef(t, strings.Contains(reason, "#"),
			"metricsDeferredKeys[%q] must cite a tracking issue (e.g. \"#1234\") so the deferral is auditable",
			k)
	}
}
