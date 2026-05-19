package observability

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// metric_display_labels.json is the single source of truth for metric
// name -> display label overrides, shared with the TS client (the InsideOut backend's
// lib/stack/component-detail-utils.ts imports the same file via a path
// alias). Drift between server and client labels is prevented at the
// source: adding or changing an entry updates both surfaces in lockstep.
//
//go:embed metric_display_labels.json
var metricDisplayLabelsJSON []byte

// metricDisplayLabels is the in-memory map parsed from
// metric_display_labels.json at package init. A malformed embedded file
// is a build-time bug — we panic at init rather than silently ship
// broken labels.
var metricDisplayLabels = func() map[string]string {
	m := make(map[string]string)
	if err := json.Unmarshal(metricDisplayLabelsJSON, &m); err != nil {
		panic(fmt.Sprintf("failed to parse metric_display_labels.json: %v", err))
	}
	return m
}()

// MetricDisplayLabels returns a copy of the embedded display-label map,
// so callers can iterate without aliasing the package-private map.
func MetricDisplayLabels() map[string]string {
	out := make(map[string]string, len(metricDisplayLabels))
	maps.Copy(out, metricDisplayLabels)
	return out
}

// MetricDisplayLabel returns the user-facing label for a CloudWatch /
// Cloud Monitoring metric name. Falls back to a CamelCase-split of the
// metric name when the JSON has no override. Mirrors the InsideOut backend's
// metricDisplayLabel (internal/agentapi/component_metrics.go:353).
//
// Names with consecutive capitals or underscores that need smarter
// handling (e.g. "HTTPCode_ELB_5XX_Count" -> "ALB 5XX Errors") must be
// added to metric_display_labels.json — the fallback only handles
// CamelCase boundaries.
func MetricDisplayLabel(name string) string {
	if label, ok := metricDisplayLabels[name]; ok {
		return label
	}
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// ComponentDisplayName returns the user-facing display name for a
// composer.ComponentKey. Mirrors the InsideOut backend's componentDisplayName
// (internal/agentapi/component_metrics.go:244) one-for-one for keys
// The InsideOut backend knows about; unknown keys fall back to the
// CamelCase-from-snake_case convention.
//
// The fallback path is a defense-in-depth safety net: every key in
// composer.AllComponentKeys is enumerated explicitly in the switch
// (drift-tested by TestComponentDisplayName_CoversEveryComponentKey).
func ComponentDisplayName(key composer.ComponentKey) string {
	switch key {
	case composer.KeyAWSVPC:
		return "AWS VPC"
	case composer.KeyAWSEC2:
		return "AWS EC2"
	case composer.KeyAWSECS:
		return "AWS ECS"
	case composer.KeyAWSEKS:
		return "AWS EKS"
	case composer.KeyAWSEKSNodeGroup:
		return "AWS EKS Node Group"
	case composer.KeyAWSRDS:
		return "AWS RDS"
	case composer.KeyAWSElastiCache:
		return "AWS ElastiCache"
	case composer.KeyAWSS3:
		return "AWS S3"
	case composer.KeyAWSDynamoDB:
		return "AWS DynamoDB"
	case composer.KeyAWSSQS:
		return "AWS SQS"
	case composer.KeyAWSMSK:
		return "AWS MSK"
	case composer.KeyAWSCloudfront:
		return "AWS CloudFront"
	case composer.KeyAWSCloudWatchLogs:
		return "AWS CloudWatch Logs"
	case composer.KeyAWSCloudWatchMonitoring:
		return "AWS CloudWatch Monitoring"
	case composer.KeyAWSKMS:
		return "AWS KMS"
	case composer.KeyAWSSecretsManager:
		return "AWS Secrets Manager"
	case composer.KeyAWSCognito:
		return "AWS Cognito"
	case composer.KeyAWSLambda:
		return "AWS Lambda"
	case composer.KeyAWSAppRunner:
		return "AWS App Runner"
	case composer.KeyAWSSageMaker:
		return "AWS SageMaker"
	case composer.KeyAWSALB:
		return "AWS Application Load Balancer"
	case composer.KeyAWSWAF:
		return "AWS WAF"
	case composer.KeyAWSAPIGateway:
		return "AWS API Gateway"
	case composer.KeyAWSOpenSearch:
		return "AWS OpenSearch"
	case composer.KeyAWSBedrock:
		return "AWS Bedrock"
	case composer.KeyAWSBastion:
		return "AWS Bastion"
	case composer.KeyAWSGrafana:
		return "AWS Grafana"
	case composer.KeyAWSCodeBuild:
		return "AWS CodeBuild"
	case composer.KeyAWSCodePipeline:
		return "AWS CodePipeline"
	case composer.KeyAWSBackups:
		return "AWS Backups"
	case composer.KeyAWSGitHubActions:
		return "AWS GitHub Actions OIDC"
	case composer.KeyAWSRoute53:
		return "AWS Route 53"
	case composer.KeyAWSACM:
		return "AWS Certificate Manager"
	case composer.KeyGCPCompute:
		return "GCP Compute Engine"
	case composer.KeyGCPGKE:
		return "GCP GKE"
	case composer.KeyGCPCloudSQL:
		return "GCP Cloud SQL"
	case composer.KeyGCPGCS:
		return "GCP Cloud Storage"
	case composer.KeyGCPCloudRun:
		return "GCP Cloud Run"
	case composer.KeyGCPSecretManager:
		return "GCP Secret Manager"
	case composer.KeyGCPCloudKMS:
		return "GCP Cloud KMS"
	case composer.KeyGCPPubSub:
		return "GCP Pub/Sub"
	case composer.KeyGCPFirestore:
		return "GCP Firestore"
	case composer.KeyGCPVPC:
		return "GCP VPC"
	case composer.KeyGCPLoadbalancer:
		return "GCP Load Balancer"
	case composer.KeyGCPMemorystore:
		return "GCP Memorystore"
	case composer.KeyGCPCloudArmor:
		return "GCP Cloud Armor"
	case composer.KeyGCPCloudBuild:
		return "GCP Cloud Build"
	case composer.KeyGCPCloudDeploy:
		return "GCP Cloud Deploy"
	case composer.KeyGCPCloudFunctions:
		return "GCP Cloud Functions"
	case composer.KeyGCPIdentityPlatform:
		return "GCP Identity Platform"
	case composer.KeyGCPVertexAI:
		return "GCP Vertex AI"
	case composer.KeyGCPBastion:
		return "GCP Bastion"
	case composer.KeyGCPAPIGateway:
		return "GCP API Gateway"
	case composer.KeyGCPCloudLogging:
		return "GCP Cloud Logging"
	case composer.KeyGCPCloudMonitoring:
		return "GCP Cloud Monitoring"
	case composer.KeyGCPCloudDNS:
		return "GCP Cloud DNS"
	case composer.KeyGCPGitHubActions:
		return "GCP GitHub Actions WIF"
	case composer.KeyGCPBackups:
		return "GCP Backups"
	default:
		// Fallback for unknown keys (forward-compat: a future composer
		// release introducing a new ComponentKey shouldn't crash here).
		raw := string(key)
		trimmed := strings.TrimPrefix(strings.TrimPrefix(raw, "aws_"), "gcp_")
		words := strings.Split(trimmed, "_")
		for i, word := range words {
			if word == "" {
				continue
			}
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
		return strings.Join(words, " ")
	}
}
