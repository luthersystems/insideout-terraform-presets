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
// All #622 entries cleared. The two by-design omissions
// (aws_github_actions, gcp_backups) live in metricsNonComponentKeys
// below with rationale.
var metricsDeferredKeys = map[composer.ComponentKey]string{
	// CodeBuild (#619). The issue explicitly defers the discovery
	// inspector — service + actions are not yet registered in
	// AWSServiceActions, so a ComponentMetricsMapping entry pointing at
	// codebuild.list-projects would dispatch to an unregistered service
	// and surface "unsupported service" at runtime instead of the panel.
	// Backfill alongside the inspector landing (mirrors the #622 backfill
	// of apprunner + sagemaker after #618 / #620 added their inspectors).
	composer.KeyAWSCodeBuild: "[#619] discovery inspector deferred; ComponentMetricsMapping entry pending the codebuild.list-projects handler registration alongside the inspector backfill PR",
	// AgentCore Gateway (#763). AgentCore CloudWatch metrics are immature
	// (the preset deliberately wires NO observability alarms and is not in
	// the CloudWatchMonitoring driver list), so there is no (service, action)
	// to bind a panel to yet — a mapping pointing at an AgentCore metrics
	// surface would dispatch to an unregistered service. Backfill once the
	// AgentCore metrics namespace stabilizes and a discovery inspector lands
	// (mirrors the aws_bedrock_agent inspector deferral precedent).
	composer.KeyAWSAgentCoreGateway: "[#763] AgentCore metrics immature; no observability wiring shipped (no CloudWatchMonitoring driver entry / no alarms), so ComponentMetricsMapping entry is deferred until the metrics namespace stabilizes and a discovery inspector lands",
}

// metricsNonComponentKeys are AllComponentKeys entries that genuinely
// do NOT correspond to a panel-renderable resource. Distinct from
// metricsDeferredKeys: these are by-design, not pending work.
var metricsNonComponentKeys = map[composer.ComponentKey]bool{
	// Auto-included node group is covered by the parent KeyAWSEKS row;
	// the dispatcher routes both keys through the same eks panel.
	composer.KeyAWSEKSNodeGroup: true,
	// aws_github_actions (#622): IAM-only component — the preset
	// provisions an IAM OIDC provider + IAM role for GitHub Actions
	// to assume. There is no panel-renderable resource analogous to
	// the GCP WIF pool (gcp_github_actions routes to
	// iam.list-workload-identity-pools because GCP exposes the pool
	// as a first-class listable resource; AWS exposes only the OIDC
	// provider ARN, which is a single static config not worth a
	// panel). IAM identity is observed via the IAM/Role panels for
	// the assumed role, not the OIDC provider list.
	composer.KeyAWSGitHubActions: true,
	// gcp_backups (#622): the preset creates two heterogeneous
	// resources — a google_storage_bucket (covered by the
	// gcp_storage panel via gcs.list-buckets) and a
	// google_compute_resource_policy snapshot schedule (a
	// compute.resource_policy, not a standalone listable service).
	// Neither maps cleanly to a single (service, action) pair on the
	// "backups" surface. Snapshot policies could be surfaced via a
	// compute.list-resource-policies action if customers ask for
	// it, but the current preset's GCS bucket is already observable
	// through the storage panel, so dual-binding to backups would
	// double-count. Marked non-component by design until a customer
	// signal emerges for the snapshot-policy panel.
	composer.KeyGCPBackups: true,
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
