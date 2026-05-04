package observability

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// ComponentMetricsBinding pairs a component key with the (service, action)
// the inspector dispatcher should call to produce the panel-default
// resources view for that component. Mirrors the value type of the InsideOut backend's
// componentMetricsMapping (internal/agentapi/component_metrics.go:96).
type ComponentMetricsBinding struct {
	Service string
	Action  string
}

// ComponentMetricsMapping maps composer.ComponentKey to the (service,
// action) the inspector dispatcher invokes for that component's panel.
// Source of truth ported from the InsideOut backend's componentMetricsMapping.
//
// Keys not present here have no panel-default discovery and the UI shows
// "no observable resources" — typically third-party toggles or
// conceptual classifiers (KeySplunk, KeyDatadog, the polymorphic EKS
// keys, KeyAWSGitHubActions, etc.).
//
// Cross-references:
//   - Service strings join the AWS/GCP service-actions registries
//     (AWSServiceActions / GCPServiceActions) for action validation.
//   - Service strings also join the per-service metric definitions
//     (Observability[k].AWS / .GCP) for the chart fall-through.
var ComponentMetricsMapping = map[composer.ComponentKey]ComponentMetricsBinding{
	// AWS
	composer.KeyAWSEC2: {Service: "ec2", Action: "describe-instances"},
	// aws_ecs → list-clusters: cluster is the billable + metric-scoped unit.
	// Service-level metrics need a ClusterName+ServiceName dimension join,
	// which discovery does not resolve. Use list-services via the MCP
	// inspector directly for a service inventory.
	composer.KeyAWSECS: {Service: "ecs", Action: "list-clusters"},
	// EKS panel queries ContainerInsights metrics with ClusterName as
	// the dimension (#233 Option B-1). The aws/eks_nodegroup preset
	// installs the amazon-cloudwatch-observability addon by default
	// so the namespace is populated on fresh deployments.
	// `list-nodes` remains available via the dispatcher for callers
	// that explicitly want instance-level data (the previous #231
	// Option A path).
	composer.KeyAWSEKS:                  {Service: "eks", Action: "list-clusters"},
	composer.KeyAWSRDS:                  {Service: "rds", Action: "describe-db-instances"},
	composer.KeyAWSElastiCache:          {Service: "elasticache", Action: "describe-cache-clusters"},
	composer.KeyAWSS3:                   {Service: "s3", Action: "list-buckets"},
	composer.KeyAWSDynamoDB:             {Service: "dynamodb", Action: "list-tables"},
	composer.KeyAWSSQS:                  {Service: "sqs", Action: "list-queues"},
	composer.KeyAWSMSK:                  {Service: "msk", Action: "list-clusters"},
	composer.KeyAWSCloudfront:           {Service: "cloudfront", Action: "list-distributions"},
	composer.KeyAWSCloudWatchLogs:       {Service: "cloudwatchlogs", Action: "describe-log-groups"},
	composer.KeyAWSCloudWatchMonitoring: {Service: "cloudwatchlogs", Action: "describe-log-groups"},
	composer.KeyAWSKMS:                  {Service: "kms", Action: "list-keys"},
	composer.KeyAWSSecretsManager:       {Service: "secretsmanager", Action: "list-secrets"},
	composer.KeyAWSCognito:              {Service: "cognito", Action: "list-user-pools"},
	composer.KeyAWSLambda:               {Service: "lambda", Action: "list-functions"},
	composer.KeyAWSALB:                  {Service: "alb", Action: "describe-load-balancers"},
	composer.KeyAWSWAF:                  {Service: "waf", Action: "list-web-acls"},
	composer.KeyAWSAPIGateway:           {Service: "apigateway", Action: "get-apis"},
	composer.KeyAWSOpenSearch:           {Service: "opensearch", Action: "describe-domains"},
	composer.KeyAWSBedrock:              {Service: "bedrock", Action: "list-knowledge-bases"},
	composer.KeyAWSVPC:                  {Service: "vpc", Action: "describe-vpcs"},
	composer.KeyAWSBastion:              {Service: "ec2", Action: "describe-instances"},
	composer.KeyAWSGrafana:              {Service: "ec2", Action: "describe-instances"},
	composer.KeyAWSCodePipeline:         {Service: "ec2", Action: "describe-instances"},
	// GCP
	composer.KeyGCPCompute:          {Service: "compute", Action: "list-instances"},
	composer.KeyGCPGKE:              {Service: "gke", Action: "list-clusters"},
	composer.KeyGCPCloudSQL:         {Service: "cloudsql", Action: "list-instances"},
	composer.KeyGCPGCS:              {Service: "gcs", Action: "list-buckets"},
	composer.KeyGCPCloudRun:         {Service: "cloudrun", Action: "list-services"},
	composer.KeyGCPSecretManager:    {Service: "secretmanager", Action: "list-secrets"},
	composer.KeyGCPCloudKMS:         {Service: "cloudkms", Action: "list-keyrings"},
	composer.KeyGCPPubSub:           {Service: "pubsub", Action: "list-topics"},
	composer.KeyGCPFirestore:        {Service: "firestore", Action: "list-collections"},
	composer.KeyGCPVPC:              {Service: "vpc", Action: "list-networks"},
	composer.KeyGCPLoadbalancer:     {Service: "loadbalancer", Action: "list-url-maps"},
	composer.KeyGCPMemorystore:      {Service: "memorystore", Action: "list-instances"},
	composer.KeyGCPCloudArmor:       {Service: "cloudarmor", Action: "list-policies"},
	composer.KeyGCPCloudBuild:       {Service: "cloudbuild", Action: "list-triggers"},
	composer.KeyGCPCloudFunctions:   {Service: "cloudfunctions", Action: "list-functions"},
	composer.KeyGCPIdentityPlatform: {Service: "identityplatform", Action: "list-tenants"},
	// list-datasets matches what gcp/vertex_ai/main.tf actually creates
	// (google_vertex_ai_dataset). The previous list-endpoints binding
	// surfaced "no metrics available" because the preset declares no
	// endpoints (#253).
	composer.KeyGCPVertexAI:     {Service: "vertexai", Action: "list-datasets"},
	composer.KeyGCPBastion:      {Service: "bastion", Action: "list-bastion-instances"},
	composer.KeyGCPAPIGateway:   {Service: "apigateway", Action: "list-apis"},
	composer.KeyGCPCloudLogging: {Service: "cloudlogging", Action: "list-logs"},
	// Cloud Monitoring routed to its own list-alert-policies discovery so the
	// panel surfaces the user's actual monitoring posture (alert policies)
	// instead of compute/get-metrics, which returned an empty payload on any
	// session without GCE instances and rendered as "No live observable
	// resources were found" — misleading in the InsideOut backend#1234.
	composer.KeyGCPCloudMonitoring: {Service: "cloudmonitoring", Action: "list-alert-policies"},
}

// EmptyDiscoveryAllowlist contains component keys where empty inspector
// results should NOT mark the session as drifted. Restricted to
// lazy-creation services only:
//
//   - aws_cloudwatch_logs / aws_cloudwatch_monitoring / gcp_cloud_logging:
//     infrastructure is configured by Terraform (retention policies, log
//     sinks) but discoverable resources (log groups) are created on first
//     use by other services — empty immediately after deploy is expected.
//
// Source of truth ported from the InsideOut backend's emptyDiscoveryAllowlist
// (internal/agentapi/component_metrics.go:209).
var EmptyDiscoveryAllowlist = map[composer.ComponentKey]bool{
	composer.KeyAWSCloudWatchLogs:       true,
	composer.KeyAWSCloudWatchMonitoring: true,
	composer.KeyGCPCloudLogging:         true,
	// Firestore on day 0 = a database with zero collections. Empty
	// list-collections is the steady state until the application writes
	// its first document; not drift (#253).
	composer.KeyGCPFirestore: true,
}
