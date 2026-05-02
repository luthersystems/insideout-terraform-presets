package observability

import (
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// ComponentObservability is the per-component authority record that drives
// both the alarm-author surface (per-component observability.tf in this
// repo) and the metric-watch surface (CloudWatch GetMetricData and Cloud
// Monitoring timeSeries.list calls served by reliable today, ported into
// pkg/observability/metrics later in this PR series).
//
// Every entry in composer.AllComponentKeys must have a record here; the
// drift-guard tests TestObservabilityCoversEveryAWSKey /
// TestObservabilityCoversEveryGCPKey fail the package build otherwise.
// Records may be empty (zero-value AWS/GCP fields) during the migration —
// every empty record must also appear in observabilityDeferred with an
// issue ref so reviewers can tell "deliberately deferred" apart from
// "forgot to seed."
type ComponentObservability struct {
	// Service is the inspector-side join key (e.g. "rds", "compute") that
	// reliable's per-service discoverer dispatches on. Mirrors the value
	// in reliable's componentMetricsMapping[k].Service. Empty string means
	// "no inspector dispatch wired yet" — entry must be in
	// observabilityDeferred.
	Service string

	// AWS is populated for AWS-backed components and carries the
	// CloudWatch namespace / dimension / metric specs that drive both the
	// metric-fetch wrapper and the per-component alarm authoring.
	// nil for GCP components.
	AWS *AWSObs

	// GCP is populated for GCP-backed components and carries the Cloud
	// Monitoring metric-type / resource-type / aligner specs.
	// nil for AWS components.
	GCP *GCPObs
}

// AWSObs is the AWS half of ComponentObservability. Mirrors
// reliable's serviceMetricDef one-for-one; the AWSMetricSpec.Alarmed /
// AlarmIssue fields are net-new, used by TestObservabilitySpecMatchesEmittedAlarms
// to enforce that every Alarmed=true spec has a matching
// aws_cloudwatch_metric_alarm in <module>/observability.tf.
type AWSObs struct {
	Namespace     string // CloudWatch namespace, e.g. "AWS/RDS"
	DimensionName string // CloudWatch dimension, e.g. "DBInstanceIdentifier"
	Metrics       []AWSMetricSpec
}

// AWSMetricSpec describes one CloudWatch metric the metric-fetch wrapper
// queries via GetMetricData and (when Alarmed=true) one alarm authored
// in the corresponding per-component observability.tf.
type AWSMetricSpec struct {
	Name  string // raw CloudWatch metric name (UI-side join key)
	Stat  string // "Average" | "Sum" | "Maximum"
	Label string // friendly display label; empty => fall back to metric_display_labels.json

	// Alarmed=true asserts a matching aws_cloudwatch_metric_alarm exists
	// in the module's observability.tf. Defaults false.
	Alarmed bool

	// AlarmIssue references the GitHub issue that justifies the deferred
	// alarm authoring when Alarmed=false. Empty means the spec is
	// metric-fetch-only and never expected to alarm.
	AlarmIssue string
}

// GCPObs is the GCP half. Mirrors reliable's gcpServiceDef.
type GCPObs struct {
	Metrics []GCPMetricSpec
}

// GCPMetricSpec describes one Cloud Monitoring metric.
type GCPMetricSpec struct {
	DisplayName   string   // UI-side join key on GCP
	MetricType    string   // e.g. "compute.googleapis.com/instance/cpu/utilization"
	ResourceType  string   // e.g. "gce_instance"
	LabelKey      string   // resource label to group by, e.g. "instance_id"
	Aligner       string   // "ALIGN_MEAN" | "ALIGN_RATE" | "ALIGN_PERCENTILE_99"
	GroupByLabels []string // metric labels to group by for breakdowns

	Alarmed    bool
	AlarmIssue string
}

// Observability is the canonical authority for per-component metric
// definitions and (eventually) per-component alarm authoring.
//
// During the migration window every entry exists but most are zero-valued
// stubs paired with an observabilityDeferred row — the corresponding data
// tables are still authoritative in reliable's internal/agentapi/
// {aws_metrics,gcp_metrics,component_metrics}.go. C2 of the migration
// fills in the Service / AWS / GCP fields by porting those tables here.
//
// Every Alarmed=true spec is enforced by
// TestObservabilitySpecMatchesEmittedAlarms (lands in C9) to have a
// matching alarm resource in the module's observability.tf.
//
// nil values are NOT permitted — use a zero-value ComponentObservability{}
// instead so the field-shape is uniform and the drift-test
// TestObservabilityCoversEveryComponentKey can rely on _, ok := Observability[k].
var Observability = map[composer.ComponentKey]ComponentObservability{
	// AWS — alphabetical, matches AllComponentKeys ordering.
	composer.KeyAWSALB:                  {},
	composer.KeyAWSAPIGateway:           {},
	composer.KeyAWSBackups:              {},
	composer.KeyAWSBastion:              {},
	composer.KeyAWSBedrock:              {},
	composer.KeyAWSCloudWatchLogs:       {},
	composer.KeyAWSCloudWatchMonitoring: {},
	composer.KeyAWSCloudfront:           {},
	composer.KeyAWSCodePipeline:         {},
	composer.KeyAWSCognito:              {},
	composer.KeyAWSDynamoDB:             {},
	composer.KeyAWSEC2:                  {},
	composer.KeyAWSECS:                  {},
	composer.KeyAWSEKS:                  {},
	composer.KeyAWSEKSControlPlane:      {},
	composer.KeyAWSEKSNodeGroup:         {},
	composer.KeyAWSElastiCache:          {},
	composer.KeyAWSGitHubActions:        {},
	composer.KeyAWSGrafana:              {},
	composer.KeyAWSKMS:                  {},
	composer.KeyAWSLambda:               {},
	composer.KeyAWSMSK:                  {},
	composer.KeyAWSOpenSearch:           {},
	composer.KeyAWSRDS:                  {},
	composer.KeyAWSS3:                   {},
	composer.KeyAWSSQS:                  {},
	composer.KeyAWSSecretsManager:       {},
	composer.KeyAWSVPC:                  {},
	composer.KeyAWSWAF:                  {},
	// GCP
	composer.KeyGCPAPIGateway:       {},
	composer.KeyGCPBackups:          {},
	composer.KeyGCPBastion:          {},
	composer.KeyGCPCloudArmor:       {},
	composer.KeyGCPCloudBuild:       {},
	composer.KeyGCPCloudCDN:         {},
	composer.KeyGCPCloudFunctions:   {},
	composer.KeyGCPCloudKMS:         {},
	composer.KeyGCPCloudLogging:     {},
	composer.KeyGCPCloudMonitoring:  {},
	composer.KeyGCPCloudRun:         {},
	composer.KeyGCPCloudSQL:         {},
	composer.KeyGCPCompute:          {},
	composer.KeyGCPFirestore:        {},
	composer.KeyGCPGCS:              {},
	composer.KeyGCPGKE:              {},
	composer.KeyGCPIdentityPlatform: {},
	composer.KeyGCPLoadbalancer:     {},
	composer.KeyGCPMemorystore:      {},
	composer.KeyGCPPubSub:           {},
	composer.KeyGCPSecretManager:    {},
	composer.KeyGCPVPC:              {},
	composer.KeyGCPVertexAI:         {},
}

// observabilityDeferred carries components whose Observability entry is
// deliberately incomplete during the migration. Each value MUST be a
// non-empty issue ref so reviewers can tell "deliberately deferred" apart
// from "forgot to seed."
//
// During the migration window most components are deferred. Subsequent
// commits in this PR series fill in real data tables (C2), per-component
// observability.tf (C7/C8), and flip Alarmed=true (C9) — corresponding
// rows here are removed as that lands.
//
// The TestObservabilityDeferred_AllHaveIssueRef gate fails the build if a
// row's value is empty.
var observabilityDeferred = map[composer.ComponentKey]string{
	composer.KeyAWSALB:                  "#204",
	composer.KeyAWSAPIGateway:           "#204",
	composer.KeyAWSBackups:              "#204",
	composer.KeyAWSBastion:              "#204",
	composer.KeyAWSBedrock:              "#204",
	composer.KeyAWSCloudWatchLogs:       "#204",
	composer.KeyAWSCloudWatchMonitoring: "#204",
	composer.KeyAWSCloudfront:           "#204",
	composer.KeyAWSCodePipeline:         "#204",
	composer.KeyAWSCognito:              "#204",
	composer.KeyAWSDynamoDB:             "#204",
	composer.KeyAWSEC2:                  "#204",
	composer.KeyAWSECS:                  "#204",
	composer.KeyAWSEKS:                  "#204",
	composer.KeyAWSEKSControlPlane:      "#204",
	composer.KeyAWSEKSNodeGroup:         "#204",
	composer.KeyAWSElastiCache:          "#204",
	composer.KeyAWSGitHubActions:        "#204",
	composer.KeyAWSGrafana:              "#204",
	composer.KeyAWSKMS:                  "#204",
	composer.KeyAWSLambda:               "#204",
	composer.KeyAWSMSK:                  "#204",
	composer.KeyAWSOpenSearch:           "#204",
	composer.KeyAWSRDS:                  "#204",
	composer.KeyAWSS3:                   "#204",
	composer.KeyAWSSQS:                  "#204",
	composer.KeyAWSSecretsManager:       "#204",
	composer.KeyAWSVPC:                  "#204",
	composer.KeyAWSWAF:                  "#204",
	composer.KeyGCPAPIGateway:           "#204",
	composer.KeyGCPBackups:              "#204",
	composer.KeyGCPBastion:              "#204",
	composer.KeyGCPCloudArmor:           "#204",
	composer.KeyGCPCloudBuild:           "#204",
	composer.KeyGCPCloudCDN:             "#204",
	composer.KeyGCPCloudFunctions:       "#204",
	composer.KeyGCPCloudKMS:             "#204",
	composer.KeyGCPCloudLogging:         "#204",
	composer.KeyGCPCloudMonitoring:      "#204",
	composer.KeyGCPCloudRun:             "#204",
	composer.KeyGCPCloudSQL:             "#204",
	composer.KeyGCPCompute:              "#204",
	composer.KeyGCPFirestore:            "#204",
	composer.KeyGCPGCS:                  "#204",
	composer.KeyGCPGKE:                  "#204",
	composer.KeyGCPIdentityPlatform:     "#204",
	composer.KeyGCPLoadbalancer:         "#204",
	composer.KeyGCPMemorystore:          "#204",
	composer.KeyGCPPubSub:               "#204",
	composer.KeyGCPSecretManager:        "#204",
	composer.KeyGCPVPC:                  "#204",
	composer.KeyGCPVertexAI:             "#204",
}

// Lookup returns the ComponentObservability record for a key. Unknown
// keys return a zero-value record and false, mirroring the
// _, ok := AWSIAMActions[k] convention from pkg/composer/iam_actions.go.
func Lookup(k composer.ComponentKey) (ComponentObservability, bool) {
	o, ok := Observability[k]
	return o, ok
}

// ServicesForKeys returns the deduplicated, sorted list of inspector
// service tags reachable from the given component keys. Used by tests
// asserting that every (service, action) pair the inspector dispatcher
// can produce traces back to a component declared in Observability.
//
// Stable order keeps test snapshots clean. Unknown component keys are
// silently ignored (forward-compat: a future composer release introducing
// a new ComponentKey shouldn't break consumers passing it here).
func ServicesForKeys(keys []composer.ComponentKey) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		o, ok := Observability[k]
		if !ok || o.Service == "" {
			continue
		}
		if seen[o.Service] {
			continue
		}
		seen[o.Service] = true
		out = append(out, o.Service)
	}
	sort.Strings(out)
	return out
}
