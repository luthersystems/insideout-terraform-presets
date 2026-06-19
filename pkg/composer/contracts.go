package composer

import (
	"slices"
	"strings"
)

// vpcSubnetSelfLinkExpr is the wiring expression for any module that consumes
// gcp_vpc's subnets_self_links output as a single value. It uses try() so
// terraform plan succeeds against an empty state on the first run (issue
// #178). On steady state subnet_self_links has length 1 and the [0] index
// resolves; on first plan when the upstream VPC module hasn't yet
// materialized the subnet list, the fallback null lets terraform plan
// progress without producing the "Invalid index ... empty tuple" stage_error
// that the custom-stack-provision pipeline previously surfaced.
//
// Built from WireRef so the prefix is guaranteed to match the rendered
// `module "gcp_vpc" {}` block label (#283).
var vpcSubnetSelfLinkExpr = "try(" + WireRef(KeyGCPVPC, "subnet_self_links") + "[0], null)"

type ComponentKey string

const (
	KeyComposer ComponentKey = "composer"
	KeyArch     ComponentKey = "architecture"
	KeyCloud    ComponentKey = "cloud"

	KeySplunk  ComponentKey = "splunk"
	KeyDatadog ComponentKey = "datadog"

	// AWS components (cloud-prefixed canonical vocabulary)
	KeyAWSVPC ComponentKey = "aws_vpc"
	// KeyAWSEKSNodeGroup is the canonical EKS managed node-group key. The
	// preset directory is aws/eks_nodegroup and the composed module label
	// is `module "aws_eks_nodegroup"` (issue #224 — the legacy
	// polymorphic "ec2" string identity was removed when its sibling
	// polymorphic KeyAWSEKSControlPlane was collapsed into KeyAWSEKS).
	KeyAWSEKSNodeGroup         ComponentKey = "aws_eks_nodegroup"
	KeyAWSBastion              ComponentKey = "aws_bastion"
	KeyAWSEC2                  ComponentKey = "aws_ec2"
	KeyAWSEKS                  ComponentKey = "aws_eks"
	KeyAWSECS                  ComponentKey = "aws_ecs"
	KeyAWSLambda               ComponentKey = "aws_lambda"
	KeyAWSAppRunner            ComponentKey = "aws_apprunner"
	KeyAWSSageMaker            ComponentKey = "aws_sagemaker"
	KeyAWSALB                  ComponentKey = "aws_alb"
	KeyAWSCloudfront           ComponentKey = "aws_cloudfront"
	KeyAWSWAF                  ComponentKey = "aws_waf"
	KeyAWSAPIGateway           ComponentKey = "aws_apigateway"
	KeyAWSRDS                  ComponentKey = "aws_rds"
	KeyAWSElastiCache          ComponentKey = "aws_elasticache"
	KeyAWSDynamoDB             ComponentKey = "aws_dynamodb"
	KeyAWSS3                   ComponentKey = "aws_s3"
	KeyAWSKMS                  ComponentKey = "aws_kms"
	KeyAWSSecretsManager       ComponentKey = "aws_secretsmanager"
	KeyAWSOpenSearch           ComponentKey = "aws_opensearch"
	KeyAWSBedrock              ComponentKey = "aws_bedrock"
	KeyAWSBedrockAgent         ComponentKey = "aws_bedrock_agent"
	KeyAWSAgentCoreGateway     ComponentKey = "aws_agentcore_gateway"
	KeyAWSKendra               ComponentKey = "aws_kendra"
	KeyAWSSQS                  ComponentKey = "aws_sqs"
	KeyAWSMSK                  ComponentKey = "aws_msk"
	KeyAWSCloudWatchLogs       ComponentKey = "aws_cloudwatch_logs"
	KeyAWSCloudWatchMonitoring ComponentKey = "aws_cloudwatch_monitoring"
	KeyAWSGrafana              ComponentKey = "aws_grafana"
	KeyAWSCognito              ComponentKey = "aws_cognito"
	KeyAWSBackups              ComponentKey = "aws_backups"
	KeyAWSGitHubActions        ComponentKey = "aws_github_actions"
	KeyAWSCodeBuild            ComponentKey = "aws_codebuild"
	KeyAWSCodePipeline         ComponentKey = "aws_codepipeline"
	KeyAWSRoute53              ComponentKey = "aws_route53"
	KeyAWSACM                  ComponentKey = "aws_acm"

	// GCP components
	KeyGCPVPC              ComponentKey = "gcp_vpc"
	KeyGCPBastion          ComponentKey = "gcp_bastion"
	KeyGCPCompute          ComponentKey = "gcp_compute"
	KeyGCPGKE              ComponentKey = "gcp_gke"
	KeyGCPCloudRun         ComponentKey = "gcp_cloud_run"
	KeyGCPCloudFunctions   ComponentKey = "gcp_cloud_functions"
	KeyGCPLoadbalancer     ComponentKey = "gcp_loadbalancer"
	KeyGCPCloudSQL         ComponentKey = "gcp_cloudsql"
	KeyGCPMemorystore      ComponentKey = "gcp_memorystore"
	KeyGCPGCS              ComponentKey = "gcp_gcs"
	KeyGCPPubSub           ComponentKey = "gcp_pubsub"
	KeyGCPCloudLogging     ComponentKey = "gcp_cloud_logging"
	KeyGCPSecretManager    ComponentKey = "gcp_secret_manager" // #nosec G101
	KeyGCPCloudKMS         ComponentKey = "gcp_cloud_kms"
	KeyGCPCloudMonitoring  ComponentKey = "gcp_cloud_monitoring"
	KeyGCPIdentityPlatform ComponentKey = "gcp_identity_platform"
	KeyGCPCloudBuild       ComponentKey = "gcp_cloud_build"
	KeyGCPCloudDeploy      ComponentKey = "gcp_cloud_deploy"
	KeyGCPFirestore        ComponentKey = "gcp_firestore"
	KeyGCPVertexAI         ComponentKey = "gcp_vertex_ai"
	KeyGCPAgentEngine      ComponentKey = "gcp_agent_engine"
	KeyGCPCloudArmor       ComponentKey = "gcp_cloud_armor"
	KeyGCPAPIGateway       ComponentKey = "gcp_api_gateway"
	KeyGCPBackups          ComponentKey = "gcp_backups"
	KeyGCPCloudDNS         ComponentKey = "gcp_cloud_dns"
	KeyGCPGitHubActions    ComponentKey = "gcp_github_actions"
)

var ComposeOrder = []ComponentKey{
	// Deps first, then consumers.
	KeyAWSVPC,
	KeyGCPVPC,
	KeyAWSEKS,
	KeyAWSECS,
	KeyGCPGKE,
	KeyGCPCompute,
	KeyGCPBastion,
	KeyGCPCloudRun,
	KeyGCPCloudFunctions,
	KeyAWSLambda,
	KeyAWSEKSNodeGroup, // node group after cluster
	KeyAWSEC2,
	KeyAWSBastion,
	KeyAWSALB,
	KeyGCPLoadbalancer,
	KeyAWSRDS,
	KeyGCPCloudSQL,
	KeyAWSElastiCache,
	KeyGCPMemorystore,
	KeyGCPFirestore,
	KeyAWSMSK,
	KeyAWSS3,
	KeyGCPGCS,
	KeyAWSDynamoDB,
	KeyAWSCloudfront,
	KeyAWSWAF,
	KeyGCPCloudArmor,
	KeyAWSBackups,
	KeyGCPBackups,
	KeyAWSCloudWatchLogs,
	KeyGCPCloudLogging,
	KeyAWSCloudWatchMonitoring,
	KeyGCPCloudMonitoring,
	KeySplunk,
	KeyDatadog,
	KeyAWSGrafana,
	KeyAWSCognito,
	KeyGCPIdentityPlatform,
	KeyAWSAPIGateway,
	KeyGCPAPIGateway,
	// ACM before Route 53 so DefaultWiring for KeyAWSRoute53 can read ACM's
	// validation_records output and inject it into route53.records (#593).
	KeyAWSACM,
	// Cloud DNS — GCP analog of Route 53. No back-edges from other GCP
	// presets today (no Cloud Armor / load-balancer alias auto-wiring), so
	// position is not load-bearing relative to other GCP keys (#593).
	KeyGCPCloudDNS,
	// Route 53 last so DefaultWiring can read ALB / CloudFront / API Gateway /
	// Cognito siblings and auto-derive the matching alias records (#584),
	// AND read ACM's validation_records output to wire DNS-01 challenges
	// without manual caller plumbing (#593).
	KeyAWSRoute53,
	KeyAWSKMS,
	KeyGCPCloudKMS,
	KeyAWSSecretsManager,
	KeyGCPSecretManager,
	KeyAWSOpenSearch,
	KeyAWSBedrock,
	// Bedrock Agent (#762) composes after KeyAWSBedrock so its DefaultWiring
	// case can read the KB's knowledge_base_id output when aws_bedrock is also
	// selected. Its hard ImplicitDependency on KeyAWSLambda (the action-group
	// executor) is positioned far earlier (Lambda :~110), so both producers
	// precede this consumer — TestImplicitDependencies_ComposeOrderRespected
	// enforces that.
	KeyAWSBedrockAgent,
	// AgentCore Gateway (#763) composes after KeyAWSBedrockAgent — it is the
	// next AI-stack layer (L7, MCP/tool gateway). Its hard ImplicitDependency
	// on KeyAWSLambda (the gateway's Lambda target) is positioned far earlier
	// (Lambda :~110), so the producer precedes this consumer —
	// TestImplicitDependencies_ComposeOrderRespected enforces that.
	KeyAWSAgentCoreGateway,
	// Kendra (#760) is a standalone enterprise-search / RAG-retrieval index.
	// It has NO hard ImplicitDependency — a bare index is valid; the optional
	// S3 data source is wired conditionally by DefaultWiring only when aws_s3
	// is also selected. aws_s3 (the only producer it can read) is positioned
	// far earlier in this order, so the conditional wire's producer precedes
	// this consumer.
	KeyAWSKendra,
	KeyAWSSageMaker,
	KeyGCPVertexAI,
	// Agent Engine (#769) composes after KeyGCPVertexAI and KeyGCPGCS (GCS is
	// far earlier, ~:124) so its DefaultWiring case can read gcp/gcs's
	// bucket_url output to wire the staging bucket when GCS is also selected.
	// No hard ImplicitDependency: a bare engine composes standalone with a
	// caller-supplied artifact URI (public by default; PSC-private networking
	// is out of scope until gcp/vpc provisions a network attachment).
	KeyGCPAgentEngine,
	// App Runner (#598 row 2). Independent of EKS/ECS/Lambda compute
	// keys — App Runner is a peer managed-container service (Cloud Run
	// analog), not a downstream consumer of them. Position adjacent to
	// other managed-compute pairs for reviewability; ordering is not
	// load-bearing because no other key wires off of it.
	KeyAWSAppRunner,
	KeyAWSSQS,
	KeyGCPPubSub,
	KeyGCPCloudBuild,
	// CodeBuild standalone preset (#619, deferred row 3 of #598). Peer
	// of gcp/cloud_build; no other key wires off of it today (CodePipeline
	// integration is a future PR). Placed alongside the GCP analog +
	// CodePipeline sibling for reviewability; ordering is not load-bearing.
	KeyAWSCodeBuild,
	KeyAWSGitHubActions,
	KeyAWSCodePipeline,
	// GCP GitHub Actions WIF preset (#597 row 1). Independent of any
	// upstream GCP preset so position is not load-bearing — placed
	// alongside the AWS sibling for reviewability.
	KeyGCPGitHubActions,
	// GCP Cloud Deploy delivery-pipeline preset (#613). The pipeline is
	// independent of any upstream GCP preset in this repo (its targets
	// reference Cloud Run regions / GKE cluster IDs the caller supplies
	// out-of-stack), so position is not load-bearing — placed alongside
	// the AWS CodePipeline sibling for reviewability.
	KeyGCPCloudDeploy,
	KeyArch,
	KeyCloud,
	KeyComposer,
}

// ModulePath defines the base directory for each component's preset.
var ModulePath = map[ComponentKey]string{
	// Third-party toggles
	KeySplunk:  "modules/splunk",
	KeyDatadog: "modules/datadog",

	// AWS (cloud-prefixed canonical vocabulary)
	KeyAWSVPC:                  "modules/vpc",
	KeyAWSEC2:                  "modules/ec2",
	KeyAWSEKS:                  "modules/eks",
	KeyAWSEKSNodeGroup:         "modules/eks_nodegroup",
	KeyAWSECS:                  "modules/ecs",
	KeyAWSLambda:               "modules/lambda",
	KeyAWSAppRunner:            "modules/apprunner",
	KeyAWSSageMaker:            "modules/sagemaker",
	KeyAWSALB:                  "modules/alb",
	KeyAWSCloudfront:           "modules/cloudfront",
	KeyAWSWAF:                  "modules/waf",
	KeyAWSAPIGateway:           "modules/apigateway",
	KeyAWSRDS:                  "modules/rds",
	KeyAWSElastiCache:          "modules/elasticache",
	KeyAWSDynamoDB:             "modules/dynamodb",
	KeyAWSOpenSearch:           "modules/opensearch",
	KeyAWSS3:                   "modules/s3",
	KeyAWSKMS:                  "modules/kms",
	KeyAWSSecretsManager:       "modules/secretsmanager",
	KeyAWSBedrock:              "modules/bedrock",
	KeyAWSBedrockAgent:         "modules/bedrock_agent",
	KeyAWSAgentCoreGateway:     "modules/agentcore_gateway",
	KeyAWSKendra:               "modules/kendra",
	KeyAWSSQS:                  "modules/sqs",
	KeyAWSMSK:                  "modules/msk",
	KeyAWSCloudWatchLogs:       "modules/cloudwatchlogs",
	KeyAWSCloudWatchMonitoring: "modules/cloudwatchmonitoring",
	KeyAWSGrafana:              "modules/grafana",
	KeyAWSCognito:              "modules/cognito",
	KeyAWSBackups:              "modules/backups",
	KeyAWSBastion:              "modules/bastion",
	KeyAWSGitHubActions:        "modules/githubactions",
	KeyAWSCodeBuild:            "modules/codebuild",
	KeyAWSCodePipeline:         "modules/codepipeline",
	KeyAWSRoute53:              "modules/route53",
	KeyAWSACM:                  "modules/acm",

	// GCP
	KeyGCPVPC:              "gcp/vpc",
	KeyGCPCompute:          "gcp/compute",
	KeyGCPBastion:          "gcp/bastion",
	KeyGCPGKE:              "gcp/gke",
	KeyGCPCloudRun:         "gcp/cloud_run",
	KeyGCPCloudFunctions:   "gcp/cloud_functions",
	KeyGCPLoadbalancer:     "gcp/loadbalancer",
	KeyGCPCloudArmor:       "gcp/cloud_armor",
	KeyGCPAPIGateway:       "gcp/api_gateway",
	KeyGCPCloudSQL:         "gcp/cloudsql",
	KeyGCPMemorystore:      "gcp/memorystore",
	KeyGCPFirestore:        "gcp/firestore",
	KeyGCPGCS:              "gcp/gcs",
	KeyGCPCloudKMS:         "gcp/kms",
	KeyGCPSecretManager:    "gcp/secret_manager",
	KeyGCPVertexAI:         "gcp/vertex_ai",
	KeyGCPAgentEngine:      "gcp/agent_engine",
	KeyGCPPubSub:           "gcp/pubsub",
	KeyGCPCloudLogging:     "gcp/cloud_logging",
	KeyGCPCloudMonitoring:  "gcp/cloud_monitoring",
	KeyGCPIdentityPlatform: "gcp/identity_platform",
	KeyGCPCloudBuild:       "gcp/cloud_build",
	KeyGCPCloudDeploy:      "gcp/cloud_deploy",
	KeyGCPBackups:          "gcp/backups",
	KeyGCPCloudDNS:         "gcp/cloud_dns",
	KeyGCPGitHubActions:    "gcp/github_actions",
}

// ImplicitDependencies defines components that must be automatically added
// if a certain component is selected.
//
// Entries here are HARD dependencies only: the selected component's preset
// literally will not compose without the dependency (a required input with
// no source, a module reference that resolves to nothing). "Commonly used
// together" is NOT a dependency — that is a recommendation and belongs in
// the conversational layer, not here. Registering a soft pairing as a hard
// dep silently re-selects a component the user explicitly removed (see the
// Bedrock→OpenSearch removal below).
//
// Bedrock is deliberately absent: the aws/bedrock preset's s3_bucket_arn
// and opensearch_collection_arn inputs are optional (default null). A
// Bedrock-only stack composes to a model-invocation role + guardrails;
// S3 and OpenSearch join only when the user selects them for the
// Knowledge Base use case, and DefaultWiring already wires them
// conditionally on that selection.
var ImplicitDependencies = map[ComponentKey][]ComponentKey{
	KeyAWSALB:          {KeyAWSVPC},
	KeyGCPLoadbalancer: {KeyGCPVPC},
	KeyAWSBastion:      {KeyAWSVPC},
	KeyAWSRDS:          {KeyAWSVPC},
	KeyGCPCloudSQL:     {KeyGCPVPC},
	KeyAWSElastiCache:  {KeyAWSVPC},
	KeyGCPMemorystore:  {KeyGCPVPC},
	KeyAWSOpenSearch:   {KeyAWSVPC},
	KeyAWSCloudfront:   {KeyAWSALB},
	KeyAWSEKS:          {KeyAWSVPC},
	KeyAWSECS:          {KeyAWSVPC},
	KeyGCPGKE:          {KeyGCPVPC},
	KeyAWSLambda:       {KeyAWSVPC},
	// SageMaker (#615): the AWS provider 6.x resource demands vpc_id +
	// subnet_ids on every aws_sagemaker_domain, regardless of whether the
	// network_mode is PublicInternetOnly or VpcOnly — selecting SageMaker
	// without a VPC leaves the required inputs without a wired source.
	KeyAWSSageMaker: {KeyAWSVPC},
	// Bedrock Agent (#762): the action group's executor is a Lambda function.
	// aws_bedrockagent_agent_action_group.action_group_executor.lambda has no
	// source unless aws/lambda is in the stack, so the action_group_lambda_arn
	// wire is unsatisfiable without it — a HARD dependency. The VPC arrives
	// transitively (KeyAWSLambda → KeyAWSVPC). The KB association is NOT a hard
	// dep: a Bedrock Agent is fully functional without RAG; the KB wire is
	// added conditionally by DefaultWiring only when aws_bedrock is selected.
	KeyAWSBedrockAgent: {KeyAWSLambda},
	// AgentCore Gateway (#763): the gateway's default tool target is a Lambda
	// function. aws_bedrockagentcore_gateway_target's lambda.lambda_arn has no
	// source unless aws/lambda is in the stack, so the target_lambda_arn wire
	// is unsatisfiable without it — a HARD dependency. The VPC arrives
	// transitively (KeyAWSLambda → KeyAWSVPC). OpenAPI/REST targets are
	// caller-supplied out-of-band and do NOT make this a hard dep on anything
	// else; the Lambda target is the composed default.
	KeyAWSAgentCoreGateway: {KeyAWSLambda},
	// App Runner (#598 row 2). The public-only service does NOT strictly
	// require a VPC, but the preset's optional VPC connector path
	// (enable_vpc_connector = true) feeds vpc_id + subnet_ids from
	// module.aws_vpc. Forcing the implicit dep mirrors the GCP analog
	// (gcp/cloud_run also declares KeyGCPVPC) and ensures the wiring is
	// satisfiable when the caller flips on private egress later.
	KeyAWSAppRunner: {KeyAWSVPC},
	// CodeBuild (#619). Public-network builds do not strictly require
	// a VPC, but the preset's optional vpc_config path (subnet_ids
	// non-empty) feeds vpc_id + subnet_ids from module.aws_vpc. Forcing
	// the implicit dep mirrors the GCP analog (gcp/cloud_build also
	// declares KeyGCPVPC) and keeps the wiring satisfiable when the
	// caller flips on private builds later.
	KeyAWSCodeBuild:    {KeyAWSVPC},
	KeyAWSEKSNodeGroup: {KeyAWSEKS, KeyAWSVPC},
	KeyAWSEC2:          {KeyAWSVPC},
	KeyGCPCompute:      {KeyGCPVPC},
	// Issue #600: GCP services that consume the VPC at apply time but were
	// previously not declared, causing silent apply-time failures whenever
	// private endpoints / VPC connectors were configured.
	//
	// Vertex AI private endpoints peer with the customer VPC via
	// servicenetworking.googleapis.com — without the VPC up first, the
	// google_vertex_ai_endpoint resource errors with NOT_FOUND on the
	// network reference.
	KeyGCPVertexAI: {KeyGCPVPC},
	// Cloud Functions Gen 2 with vpc_connector / VPC egress requires the
	// serverless VPC access connector (provisioned by gcp/vpc when
	// enable_serverless_connector is on). Selecting Cloud Functions without
	// the VPC leaves the connector ref dangling.
	KeyGCPCloudFunctions: {KeyGCPVPC},
	// Cloud Run with vpc_access_connector has the same dependency on the
	// serverless VPC access connector as Cloud Functions Gen 2.
	KeyGCPCloudRun: {KeyGCPVPC},
	// Cloud Build private worker pools peer with the customer VPC via
	// servicenetworking; the pool create call fails if the VPC + private
	// service connection isn't up.
	KeyGCPCloudBuild: {KeyGCPVPC},
	// Cloud Armor security policies only attach to backend services on an
	// HTTPS load balancer; selecting Cloud Armor without the LB silently
	// no-ops at apply time.
	KeyGCPCloudArmor: {KeyGCPLoadbalancer},
}

// ResolveDependencies recursively finds all required components for a given set of keys.
//
// This signature is kept stable for downstream callers (e.g. The InsideOut backend). For
// EKS auto-include of the worker node group, callers should use
// ResolveDependenciesForCompose instead so the node group is appended
// automatically whenever KeyAWSEKS is in the selected set.
func ResolveDependencies(keys []ComponentKey) []ComponentKey {
	added := make(map[ComponentKey]bool)
	var final []ComponentKey

	var resolve func(k ComponentKey)
	resolve = func(k ComponentKey) {
		if added[k] {
			return
		}
		// First resolve dependencies
		if deps, ok := ImplicitDependencies[k]; ok {
			for _, dep := range deps {
				resolve(dep)
			}
		}
		// Then add self
		if !added[k] {
			final = append(final, k)
			added[k] = true
		}
	}

	for _, k := range keys {
		resolve(k)
	}

	return final
}

// ResolveDependenciesForCompose is the comps-aware resolver used by ComposeStack
// and ComposeSingle. It runs ResolveDependencies first, then auto-includes
// KeyAWSEKSNodeGroup whenever KeyAWSEKS is in the resolved set (issue #206).
// The `comps` parameter is retained for API stability — Lambda vs EKS is now
// chosen explicitly via the caller's KeyAWSLambda / KeyAWSEKS selection
// (issue #224, polymorphic dispatch removed).
func ResolveDependenciesForCompose(keys []ComponentKey, comps *Components) []ComponentKey {
	_ = comps // retained for API stability; no longer used for polymorphic dispatch
	resolved := ResolveDependencies(keys)
	hasEKS := false
	hasNodeGroup := false
	for _, k := range resolved {
		switch k {
		case KeyAWSEKS:
			hasEKS = true
		case KeyAWSEKSNodeGroup:
			hasNodeGroup = true
		}
	}
	if !hasEKS || hasNodeGroup {
		return resolved
	}
	return append(resolved, KeyAWSEKSNodeGroup)
}

// GetModuleDir returns the output directory for a key (e.g., "modules/vpc").
// This is where the composed terraform files are placed.
//
// The `comps` parameter is retained for API stability; polymorphic dispatch
// (issue #224) has been removed — callers select KeyAWSEKS or KeyAWSLambda
// explicitly.
func GetModuleDir(k ComponentKey, comps *Components) string {
	_ = comps
	return ModulePath[k]
}

// PresetKeyMap maps component keys to their preset directory names.
// Used when the preset name differs from the component key.
var PresetKeyMap = map[ComponentKey]string{
	KeyAWSEKSNodeGroup:         "eks_nodegroup",
	KeyAWSVPC:                  "vpc",
	KeyAWSBastion:              "bastion",
	KeyAWSEC2:                  "ec2",
	KeyAWSEKS:                  "eks",
	KeyAWSECS:                  "ecs",
	KeyAWSLambda:               "lambda",
	KeyAWSAppRunner:            "apprunner",
	KeyAWSSageMaker:            "sagemaker",
	KeyAWSALB:                  "alb",
	KeyAWSCloudfront:           "cloudfront",
	KeyAWSWAF:                  "waf",
	KeyAWSAPIGateway:           "apigateway",
	KeyAWSRDS:                  "rds",
	KeyAWSElastiCache:          "elasticache",
	KeyAWSDynamoDB:             "dynamodb",
	KeyAWSOpenSearch:           "opensearch",
	KeyAWSS3:                   "s3",
	KeyAWSKMS:                  "kms",
	KeyAWSSecretsManager:       "secretsmanager",
	KeyAWSBedrock:              "bedrock",
	KeyAWSBedrockAgent:         "bedrock_agent",
	KeyAWSAgentCoreGateway:     "agentcore_gateway",
	KeyAWSKendra:               "kendra",
	KeyAWSSQS:                  "sqs",
	KeyAWSMSK:                  "msk",
	KeyAWSCloudWatchLogs:       "cloudwatchlogs",
	KeyAWSCloudWatchMonitoring: "cloudwatchmonitoring",
	KeyAWSGrafana:              "grafana",
	KeyAWSCognito:              "cognito",
	KeyAWSBackups:              "backups",
	KeyAWSGitHubActions:        "githubactions",
	KeyAWSCodeBuild:            "codebuild",
	KeyAWSCodePipeline:         "codepipeline",
	KeyAWSRoute53:              "route53",
	KeyAWSACM:                  "acm",
	KeyGCPVPC:                  "vpc",
	KeyGCPCompute:              "compute",
	KeyGCPGKE:                  "gke",
	KeyGCPLoadbalancer:         "loadbalancer",
	KeyGCPCloudSQL:             "cloudsql",
	KeyGCPMemorystore:          "memorystore",
	KeyGCPGCS:                  "gcs",
	KeyGCPCloudLogging:         "cloud_logging",
	KeyGCPSecretManager:        "secretmanager",
	KeyGCPCloudKMS:             "kms",
	KeyGCPPubSub:               "pubsub",
	KeyGCPCloudMonitoring:      "cloud_monitoring",
	KeyGCPVertexAI:             "vertex_ai",
	KeyGCPAgentEngine:          "agent_engine",
	KeyGCPCloudBuild:           "cloud_build",
	KeyGCPFirestore:            "firestore",
	KeyGCPCloudArmor:           "cloud_armor",
	KeyGCPAPIGateway:           "api_gateway",
	KeyGCPBackups:              "backups",
	KeyGCPIdentityPlatform:     "identity_platform",
	KeyGCPCloudRun:             "cloud_run",
	KeyGCPCloudFunctions:       "cloud_functions",
	KeyGCPBastion:              "bastion",
	KeyGCPCloudDNS:             "cloud_dns",
	KeyGCPGitHubActions:        "github_actions",
	KeyGCPCloudDeploy:          "cloud_deploy",
}

// GetPresetPath returns the cloud-prefixed preset path for a component.
// For example: GetPresetPath("aws", KeyAWSVPC, nil) returns "aws/vpc"
//
// The `comps` parameter is retained for API stability; polymorphic dispatch
// (issue #224) was removed — callers select KeyAWSEKS or KeyAWSLambda
// explicitly.
func GetPresetPath(cloud string, k ComponentKey, comps *Components) string {
	_ = comps
	presetName := string(k)

	// Handle special cases where preset name differs from key
	if mapped, ok := PresetKeyMap[k]; ok {
		presetName = mapped
	}

	return cloud + "/" + presetName
}

// CloudFor returns the cloud prefix ("aws" or "gcp") for a ComponentKey.
// Defaults to "aws" for any key whose string value doesn't carry the "gcp_"
// prefix.
func CloudFor(k ComponentKey) string {
	if strings.HasPrefix(string(k), "gcp_") {
		return "gcp"
	}
	return "aws"
}

// CloudFromKeys returns the dominant cloud ("aws" or "gcp") for a slice
// of component keys. Returns "gcp" if any key carries the "gcp_" prefix,
// otherwise "aws". Use this when only string keys are in hand — e.g.
// derived from a session's selected components — and the typed
// ComponentKey form isn't available.
func CloudFromKeys(keys []string) string {
	for _, k := range keys {
		if strings.HasPrefix(k, "gcp_") {
			return "gcp"
		}
	}
	return "aws"
}

// AllComponentKeys lists every ComponentKey backed by a preset module in
// this repo. It is the source of truth for tests that need to exercise
// every component the mapper might touch — TestMapperKeysSubsetOfModule
// Variables ranges over this list to verify each emitted tfvar matches a
// declared variable in the corresponding module's variables.tf.
//
// Adding a new ComponentKey requires adding it here too:
// TestAllComponentKeysCoversPresetKeyMap fails on drift between this list
// and PresetKeyMap.
//
// Excluded by design:
//   - KeyComposer / KeyArch / KeyCloud — conceptual classifiers; no module.
//   - KeySplunk / KeyDatadog — third-party toggles; no preset in this repo
//     (consumed directly by the InsideOut backend's composeradapter).
var AllComponentKeys = []ComponentKey{
	// AWS (alphabetical for reviewability)
	KeyAWSACM,
	KeyAWSAgentCoreGateway, // aws_agentcore_gateway — sorts between aws_acm and aws_alb
	KeyAWSALB,
	KeyAWSAPIGateway,
	KeyAWSAppRunner,
	KeyAWSBackups,
	KeyAWSBastion,
	KeyAWSBedrock,
	KeyAWSBedrockAgent,
	KeyAWSCloudWatchLogs,
	KeyAWSCloudWatchMonitoring,
	KeyAWSCloudfront,
	KeyAWSCodeBuild,
	KeyAWSCodePipeline,
	KeyAWSCognito,
	KeyAWSDynamoDB,
	KeyAWSEC2,
	KeyAWSECS,
	KeyAWSEKS,
	KeyAWSEKSNodeGroup,
	KeyAWSElastiCache,
	KeyAWSGitHubActions,
	KeyAWSGrafana,
	KeyAWSKendra, // aws_kendra — sorts between aws_grafana and aws_kms
	KeyAWSKMS,
	KeyAWSLambda,
	KeyAWSMSK,
	KeyAWSOpenSearch,
	KeyAWSRDS,
	KeyAWSRoute53,
	KeyAWSS3,
	KeyAWSSQS,
	KeyAWSSageMaker,
	KeyAWSSecretsManager,
	KeyAWSVPC,
	KeyAWSWAF,
	// GCP
	KeyGCPAgentEngine,
	KeyGCPAPIGateway,
	KeyGCPBackups,
	KeyGCPBastion,
	KeyGCPCloudArmor,
	KeyGCPCloudBuild,
	KeyGCPCloudDeploy,
	KeyGCPCloudDNS,
	KeyGCPCloudFunctions,
	KeyGCPCloudKMS,
	KeyGCPCloudLogging,
	KeyGCPCloudMonitoring,
	KeyGCPCloudRun,
	KeyGCPCloudSQL,
	KeyGCPCompute,
	KeyGCPFirestore,
	KeyGCPGCS,
	KeyGCPGKE,
	KeyGCPGitHubActions,
	KeyGCPIdentityPlatform,
	KeyGCPLoadbalancer,
	KeyGCPMemorystore,
	KeyGCPPubSub,
	KeyGCPSecretManager,
	KeyGCPVPC,
	KeyGCPVertexAI,
}

// isPublicVPC returns true if the VPC is configured as a Public VPC (no
// private subnets). Reads only comps.AWSVPC; the legacy comps.VPC string is
// promoted to AWSVPC by Components.Normalize, which ComposeStack /
// ComposeSingle call at entry.
func isPublicVPC(comps *Components) bool {
	if comps == nil {
		return false
	}
	return comps.AWSVPC == "Public VPC"
}

type WiredInputs struct {
	Names  []string
	RawHCL map[string]string // var name -> raw expression or object literal
}

// DefaultRootLocals returns the composed-root `locals { }` map keyed by
// local name to raw HCL expression. The composer emits these as a
// top-of-file `locals { }` block in main.tf and any module-block input
// that wires through this layer reads `local.<name>` instead of
// `module.<producer>.<output>` directly.
//
// The locals channel exists specifically to break cycle-validator
// rejections on real-terraform-valid back-edges (issue #601): the
// validator's extractWiringEdges only inspects `module.X.Y` traversals
// inside ModuleBlock.Raw, so a back-edge expressed as
// `local.acm_validation_record_fqdns` is invisible to the cycle topology
// while terraform plan still orders the data flow correctly.
//
// Returns nil when no back-edge locals fire for the current selection.
//
// Today's locals (extend here when adding new back-edge pairs):
//   - acm_validation_record_fqdns : list of FQDNs derived from
//     route53.record_fqdns, consumed by ACM. Wired when both KeyAWSACM
//     and KeyAWSRoute53 are selected (#601).
func DefaultRootLocals(selected map[ComponentKey]bool) map[string]string {
	locals := map[string]string{}
	if selected[KeyAWSACM] && selected[KeyAWSRoute53] {
		// route53.record_fqdns is map(string) keyed by "<name>-<type>";
		// ACM's validation_record_fqdns wants list(string). values() flattens.
		locals["acm_validation_record_fqdns"] = "values(" + WireRef(KeyAWSRoute53, "record_fqdns") + ")"
	}
	if len(locals) == 0 {
		return nil
	}
	return locals
}

// Module-reference helpers return "module.<name>" paths used by wiring to
// cross-reference resources. Callers with legacy ComponentKey selections
// must Normalize / use the composeradapter so the `selected` map carries
// KeyAWS* keys; ComposeStack rejects purely-legacy SelectedKeys at entry.

func vpcRef(selected map[ComponentKey]bool) string {
	if selected[KeyGCPVPC] {
		return ModuleRef(KeyGCPVPC)
	}
	return ModuleRef(KeyAWSVPC)
}

func albRef(_ map[ComponentKey]bool) string        { return ModuleRef(KeyAWSALB) }
func wafRef(_ map[ComponentKey]bool) string        { return ModuleRef(KeyAWSWAF) }
func bastionRef(_ map[ComponentKey]bool) string    { return ModuleRef(KeyAWSBastion) }
func rdsRef(_ map[ComponentKey]bool) string        { return ModuleRef(KeyAWSRDS) }
func s3Ref(_ map[ComponentKey]bool) string         { return ModuleRef(KeyAWSS3) }
func opensearchRef(_ map[ComponentKey]bool) string { return ModuleRef(KeyAWSOpenSearch) }
func sqsRef(_ map[ComponentKey]bool) string        { return ModuleRef(KeyAWSSQS) }
func lambdaRef(_ map[ComponentKey]bool) string     { return ModuleRef(KeyAWSLambda) }
func bedrockRef(_ map[ComponentKey]bool) string    { return ModuleRef(KeyAWSBedrock) }

// resourceRef returns the EKS/ECS module reference for the selected stack.
// Prefers KeyAWSEKS over KeyAWSECS when both are somehow present (validators
// reject the combination elsewhere). Falls back to module.aws_eks when
// nothing matches — the final fallback is effectively unreachable (wiring
// only runs when `hasResource` is true) but picks the prefixed name
// defensively.
func resourceRef(selected map[ComponentKey]bool) string {
	if selected[KeyAWSEKS] {
		return ModuleRef(KeyAWSEKS)
	}
	if selected[KeyAWSECS] {
		return ModuleRef(KeyAWSECS)
	}
	return ModuleRef(KeyAWSEKS)
}

// DefaultWiring returns cross-module references for module k. The caller's
// `selected` map must carry KeyAWS*-prefixed keys; ComposeStack rejects
// purely-legacy SelectedKeys at entry, and Components.Normalize promotes
// legacy struct fields before this function is reached.
func DefaultWiring(selected map[ComponentKey]bool, k ComponentKey, comps *Components) WiredInputs {
	wi := WiredInputs{RawHCL: map[string]string{}}

	hasVPC := selected[KeyAWSVPC]
	hasALB := selected[KeyAWSALB]
	hasWAF := selected[KeyAWSWAF]
	hasBastion := selected[KeyAWSBastion]
	hasPostgres := selected[KeyAWSRDS]
	hasS3 := selected[KeyAWSS3]
	hasOpenSearch := selected[KeyAWSOpenSearch]
	hasSQS := selected[KeyAWSSQS]
	hasResource := selected[KeyAWSEKS]

	switch k {

	/* ---------------- VPC fans out ---------------- */

	case KeyAWSALB:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["public_subnet_ids"] = vpc + ".public_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "public_subnet_ids")
		}

	case KeyAWSEKS:
		// EKS Wiring (Lambda has its own KeyAWSLambda case below; the
		// legacy KeyAWSEKSControlPlane polymorphic dispatch was removed
		// in #224).
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["private_subnet_ids"] = vpc + ".private_subnet_ids"
			wi.RawHCL["public_subnet_ids"] = vpc + ".public_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "private_subnet_ids", "public_subnet_ids")
		}
		wi.RawHCL["cluster_enabled_log_types"] = `["api", "audit", "authenticator", "controllerManager", "scheduler"]`
		wi.Names = append(wi.Names, "cluster_enabled_log_types")

	case KeyAWSECS:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["private_subnet_ids"] = vpc + ".private_subnet_ids"
			wi.RawHCL["public_subnet_ids"] = vpc + ".public_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "private_subnet_ids", "public_subnet_ids")
		}

	case KeyAWSLambda:
		// Only wire Lambda to VPC when private subnets are available.
		// Public VPCs have no private subnets, so Lambda would get empty
		// subnet_ids which causes AWS API error (SubnetIds and SecurityIds
		// must coexist or be both empty).
		if hasVPC && !isPublicVPC(comps) {
			vpc := vpcRef(selected)
			wi.RawHCL["enable_vpc"] = "true"
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			wi.RawHCL["security_group_ids"] = "[]"
			wi.Names = append(wi.Names, "enable_vpc", "vpc_id", "subnet_ids", "security_group_ids")
		}

	case KeyAWSAppRunner:
		// App Runner VPC-connector path consumes vpc_id + subnet_ids
		// (#598 row 2). The preset's enable_vpc_connector flag gates
		// actual creation of the connector, but threading the wiring
		// unconditionally is fine — when the flag is false the inputs
		// are simply ignored. Prefer private subnets when the upstream
		// VPC is non-public so the connector lands in the right tier;
		// fall back to public subnets on Public-VPC stacks (the connector
		// still works — egress NAT is provided by App Runner's own
		// network plane, the customer subnets are only the ENI placement
		// for cross-VPC reachability).
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			if isPublicVPC(comps) {
				wi.RawHCL["subnet_ids"] = vpc + ".public_subnet_ids"
			} else {
				wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			}
			wi.Names = append(wi.Names, "vpc_id", "subnet_ids")
		}

	case KeyAWSCodeBuild:
		// CodeBuild VPC-config path consumes vpc_id + subnet_ids
		// (#619). The preset gates vpc_config creation on a non-empty
		// subnet_ids list, but threading the wiring unconditionally is
		// fine — when subnet_ids is empty (e.g. on a single-module
		// preview) the preset simply leaves the vpc_config dynamic
		// block off. Prefer private subnets when the upstream VPC is
		// non-public so the build ENIs land in the right tier; fall
		// back to public subnets on Public-VPC stacks so callers still
		// get a non-empty list when they later opt-in by populating
		// subnet_ids out-of-stack.
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			if isPublicVPC(comps) {
				wi.RawHCL["subnet_ids"] = vpc + ".public_subnet_ids"
			} else {
				wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			}
			wi.Names = append(wi.Names, "vpc_id", "subnet_ids")
		}

	case KeyAWSSageMaker:
		// SageMaker domains demand vpc_id + subnet_ids on every shape
		// (AWS provider 6.x required arguments). The composer's
		// ImplicitDependencies entry guarantees KeyAWSVPC is selected
		// whenever KeyAWSSageMaker is — so the vpcRef wiring is always
		// satisfiable here.
		//
		// We prefer private subnets when available; on a Public-VPC stack
		// (no private subnets) we fall back to public subnets so the
		// resource at least gets a non-empty list. The studio app ENIs
		// only need outbound network so public subnets work for both
		// PublicInternetOnly and VpcOnly modes — VpcOnly callers who want
		// strict private-only egress should switch the upstream VPC off
		// "Public VPC" so private subnets are provisioned.
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			if isPublicVPC(comps) {
				wi.RawHCL["subnet_ids"] = vpc + ".public_subnet_ids"
			} else {
				wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			}
			wi.Names = append(wi.Names, "vpc_id", "subnet_ids")
		}

	case KeyAWSEKSNodeGroup:
		if hasResource {
			wi.RawHCL["cluster_name"] = resourceRef(selected) + ".cluster_name"
			wi.Names = append(wi.Names, "cluster_name")
		}
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			wi.Names = append(wi.Names, "subnet_ids")
		}

	case KeyAWSEC2: // Standalone EC2 instance
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			if isPublicVPC(comps) {
				wi.RawHCL["subnet_id"] = vpc + ".public_subnet_ids[0]"
				wi.RawHCL["associate_public_ip"] = "true"
				wi.Names = append(wi.Names, "vpc_id", "subnet_id", "associate_public_ip")
			} else {
				wi.RawHCL["subnet_id"] = vpc + ".private_subnet_ids[0]"
				wi.Names = append(wi.Names, "vpc_id", "subnet_id")
			}
		}

	case KeyAWSBastion:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["subnet_id"] = vpc + ".public_subnet_ids[0]"
			wi.Names = append(wi.Names, "vpc_id", "subnet_id")
		}

	case KeyAWSRDS:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "subnet_ids")
		}
		wi.RawHCL["enable_cloudwatch_logs"] = "true"
		wi.RawHCL["cloudwatch_logs_exports"] = `["postgresql", "upgrade"]`
		wi.RawHCL["skip_final_snapshot"] = "true"
		wi.RawHCL["apply_immediately"] = "true"
		wi.Names = append(wi.Names, "enable_cloudwatch_logs", "cloudwatch_logs_exports", "skip_final_snapshot", "apply_immediately")

	case KeyAWSElastiCache:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["cache_subnet_ids"] = vpc + ".private_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "cache_subnet_ids")
		}

	case KeyAWSMSK:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "subnet_ids")
		}

	case KeyAWSCloudfront:
		if hasALB {
			wi.RawHCL["origin_type"] = `"http"`
			wi.RawHCL["custom_origin_domain"] = albRef(selected) + ".alb_dns_name"
			wi.Names = append(wi.Names, "origin_type", "custom_origin_domain")
		}
		if hasWAF {
			wi.RawHCL["web_acl_id"] = wafRef(selected) + ".web_acl_arn"
			wi.Names = append(wi.Names, "web_acl_id")
		}

	case KeyAWSWAF:
		wi.RawHCL["scope"] = `"CLOUDFRONT"`
		wi.RawHCL["region"] = `"us-east-1"`
		wi.Names = append(wi.Names, "scope", "region")

	case KeyAWSRoute53:
		// Auto-emit alias records for in-stack endpoints (issue #584).
		//
		// Today's auto-aliases cover the two consumers whose presets already
		// expose the (dns_name, zone_id) pair the alias-target shape needs:
		//
		//   - ALB:        module.aws_alb.alb_dns_name + .alb_zone_id, alias
		//                 at the apex ("") with evaluate_target_health=true.
		//   - CloudFront: module.aws_cloudfront.domain_name; CloudFront's
		//                 hosted zone is the documented global static
		//                 Z2FDTNDATAQYW2; alias at "cdn".
		//                 evaluate_target_health must be false (CloudFront
		//                 contract — no Route 53 health-check semantics on
		//                 the global edge).
		//
		// Deferred (no preset outputs yet — TODO(#584-followup)):
		//   - API Gateway: needs aws/apigateway to expose
		//     aws_apigatewayv2_domain_name.api[0].domain_name_configuration[0]
		//     {target_domain_name,hosted_zone_id} as outputs. Today they are
		//     internal to the preset and the count=var.domain_name!=null gate
		//     makes the outputs themselves nullable, so even after adding
		//     them the wiring needs to gate on the user supplying a domain
		//     to apigateway. Filed as a follow-up.
		//   - Cognito: needs aws/cognito to manage an actual custom domain
		//     (aws_cognito_user_pool_domain with `domain` instead of
		//     `domain_prefix`) and surface its cloudfront_distribution_arn.
		//     The current preset only manages the hosted UI prefix.
		//
		// The aliases HCL is emitted as a list literal of objects. Only
		// emitted when at least one consumer is selected — leaving
		// var.aliases at its preset default of `[]` when route53 is
		// composed standalone (test contract: wiring stays inert when only
		// one side is selected).
		var aliasEntries []string
		if hasALB {
			aliasEntries = append(aliasEntries, `    {
      name                   = ""
      target_dns_name        = `+albRef(selected)+`.alb_dns_name
      target_zone_id         = `+albRef(selected)+`.alb_zone_id
      type                   = "A"
      evaluate_target_health = true
    }`)
		}
		if selected[KeyAWSCloudfront] {
			// Z2FDTNDATAQYW2 is the documented global hosted-zone ID for
			// every CloudFront distribution. evaluate_target_health must
			// remain false for CloudFront alias targets.
			aliasEntries = append(aliasEntries, `    {
      name                   = "cdn"
      target_dns_name        = `+WireRef(KeyAWSCloudfront, "domain_name")+`
      target_zone_id         = "Z2FDTNDATAQYW2"
      type                   = "A"
      evaluate_target_health = false
    }`)
		}
		if len(aliasEntries) > 0 {
			wi.RawHCL["aliases"] = "[\n" + strings.Join(aliasEntries, ",\n") + ",\n  ]"
			wi.Names = append(wi.Names, "aliases")
		}
		// Auto-inject ACM DNS-01 validation records when aws/acm is in the
		// stack (issue #593). ACM's validation_records output is a list of
		// {name, type, value} maps; route53.records expects a list of
		// {name, type, ttl, values} objects. The for-expression reshapes
		// the data and pins TTL to 60s — ACM's DNS validation polls every
		// 60s, so anything higher wastes time on first apply. The records
		// are CNAME (ACM's only validation method here), so the type pass-
		// through is safe.
		//
		// The back-edge (route53.record_fqdns → acm.validation_record_fqdns
		// + auto-flip acm.create_validation=true) lives on the KeyAWSACM
		// case below and is routed through a composed-root `locals { }`
		// block (DefaultRootLocals) so ValidateNoModuleCycles sees a
		// one-way graph. See #601.
		if selected[KeyAWSACM] {
			wi.RawHCL["records"] = `[for r in ` + WireRef(KeyAWSACM, "validation_records") + ` : {
      name   = r.name
      type   = r.type
      ttl    = 60
      values = [r.value]
    }]`
			wi.Names = append(wi.Names, "records")
		}

	case KeyAWSACM:
		// Back-edge into ACM: when route53 is also in the stack, ACM
		// reads its validation_record_fqdns from the composed-root local
		// `acm_validation_record_fqdns` (emitted by DefaultRootLocals as
		// `values(module.aws_route53.record_fqdns)`). The local layer is
		// the cycle-break — moduleRefPattern in validate_module_graph.go
		// only matches module.X.Y traversals, so the validator sees a
		// one-way graph (acm → route53) while terraform plan still orders
		// correctly through the local-to-module data flow. Issue #601.
		//
		// create_validation auto-flips to true so the composed stack
		// produces an ISSUED cert in one apply. The wired RawHCL value
		// (`true`) takes precedence over any cfg.AWSACM.CreateValidation
		// the caller supplies — matching the unconditional behavior of
		// the forward edge on the KeyAWSRoute53 case above. Callers who
		// need create_validation=false when route53 is also selected
		// must drop one of the keys from the selection.
		if selected[KeyAWSRoute53] {
			wi.RawHCL["validation_record_fqdns"] = "local.acm_validation_record_fqdns"
			wi.RawHCL["create_validation"] = "true"
			wi.Names = append(wi.Names, "validation_record_fqdns", "create_validation")
		}

	case KeyAWSCloudWatchMonitoring:
		// When any per-component observability consumer is in the stack
		// (issue #204), the per-component observability.tf files own the
		// CPU/storage/etc. alarms — disable the legacy aggregator-side
		// alarms and skip the back-edge wiring that would otherwise close
		// a 2-cycle with each consumer (issue #285). The forward-edge
		// wiring (alarm_topic_arn = module.aws_cloudwatch_monitoring.sns_topic_arn,
		// emitted post-switch) keeps per-component alarms notifying via
		// the shared SNS topic.
		perComponentActive := false
		for _, dep := range PricingDependencies[KeyAWSCloudWatchMonitoring] {
			if selected[dep] {
				perComponentActive = true
				break
			}
		}
		if perComponentActive {
			wi.RawHCL["disable_legacy_per_component_alarms"] = "true"
			wi.Names = append(wi.Names, "disable_legacy_per_component_alarms")
			break
		}
		// Aggregator-only fall-through. Today every back-edge target
		// (bastion/rds/alb/sqs) is itself in
		// PricingDependencies[KeyAWSCloudWatchMonitoring], so the
		// `if has*` blocks below cannot fire — any stack that selects
		// one of those keys flips perComponentActive=true above and
		// breaks first. The blocks remain as defense-in-depth: if a key
		// is later removed from PricingDependencies (intentional opt-out
		// of per-component observability), the legacy back-edge wiring
		// keeps the cwm dashboard widgets rendering per-resource series.
		if hasBastion {
			wi.RawHCL["instance_ids"] = "[" + bastionRef(selected) + ".bastion_instance_id]"
			wi.Names = append(wi.Names, "instance_ids")
		}
		if hasPostgres {
			wi.RawHCL["rds_instance_ids"] = "[" + rdsRef(selected) + ".instance_id]"
			wi.Names = append(wi.Names, "rds_instance_ids")
		}
		if hasALB {
			wi.RawHCL["alb_arn_suffixes"] = "[" + albRef(selected) + ".alb_arn_suffix]"
			wi.Names = append(wi.Names, "alb_arn_suffixes")
		}
		if hasSQS {
			wi.RawHCL["sqs_queue_arns"] = "[" + sqsRef(selected) + ".queue_arn]"
			wi.Names = append(wi.Names, "sqs_queue_arns")
		}

	case KeyAWSOpenSearch:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "subnet_ids")
		}

	case KeyAWSBedrock:
		if hasS3 {
			wi.RawHCL["s3_bucket_arn"] = s3Ref(selected) + ".bucket_arn"
			wi.Names = append(wi.Names, "s3_bucket_arn")
		}
		if hasOpenSearch {
			osRef := opensearchRef(selected)
			wi.RawHCL["opensearch_collection_arn"] = osRef + ".collection_arn"
			// Bedrock authors the AOSS data-access policy from the collection
			// NAME (AOSS access policies match collections by name, not ARN),
			// so it needs collection_name as well as collection_arn. Without
			// this edge the bedrock module skips the data-access policy
			// (count gates on var.opensearch_collection_name) and the KB role
			// has no data-plane grant. See aws/opensearch/main.tf header.
			wi.RawHCL["opensearch_collection_name"] = osRef + ".collection_name"
			wi.Names = append(wi.Names, "opensearch_collection_arn", "opensearch_collection_name")
		}

	case KeyAWSBedrockAgent:
		// The action group's executor is a Lambda function. KeyAWSLambda is a
		// HARD ImplicitDependency of KeyAWSBedrockAgent, so aws/lambda is always
		// in the stack here — wire the agent's action_group_lambda_arn from the
		// lambda module's function_arn output unconditionally.
		wi.RawHCL["action_group_lambda_arn"] = lambdaRef(selected) + ".function_arn"
		// enable_action_group is the plan-time-known gate for the action group /
		// invoke permission. action_group_lambda_arn above is a computed output,
		// so the preset can't gate its count on `!= null` (unknown at plan).
		// Lambda is a HARD dep here, so the action group always exists → true.
		wi.RawHCL["enable_action_group"] = "true"
		wi.Names = append(wi.Names, "action_group_lambda_arn", "enable_action_group")
		// "Agent that does RAG": when aws/bedrock is also selected with its
		// Knowledge Base enabled, wire the KB's id into the agent so the preset
		// creates an aws_bedrockagent_agent_knowledge_base_association. The
		// bedrock preset's knowledge_base_id output is null when the KB is
		// disabled — but its null-ness is unknown at plan, so we gate the
		// association on the bedrock module's plan-time-known
		// knowledge_base_enabled output (true iff the KB is provisioned) rather
		// than on knowledge_base_id. Composing aws_bedrock without
		// enable_knowledge_base leaves the agent KB-less, which is correct.
		if selected[KeyAWSBedrock] {
			wi.RawHCL["knowledge_base_id"] = bedrockRef(selected) + ".knowledge_base_id"
			wi.RawHCL["enable_knowledge_base_association"] = bedrockRef(selected) + ".knowledge_base_enabled"
			wi.Names = append(wi.Names, "knowledge_base_id", "enable_knowledge_base_association")
		}

	case KeyAWSAgentCoreGateway:
		// The gateway's default tool target is a Lambda function. KeyAWSLambda
		// is a HARD ImplicitDependency of KeyAWSAgentCoreGateway, so aws/lambda
		// is always in the stack here — wire the gateway's target_lambda_arn
		// from the lambda module's function_arn output unconditionally. The
		// preset gates the Lambda target / invoke policy / permission on a
		// non-null arn, so the wire is what turns the Lambda into an MCP tool.
		wi.RawHCL["target_lambda_arn"] = lambdaRef(selected) + ".function_arn"
		// enable_lambda_target is the plan-time-known gate for the Lambda target /
		// invoke policy / permission. target_lambda_arn above is a computed
		// output, so the preset can't gate their count on `!= null` (unknown at
		// plan). Lambda is a HARD dep here, so the target always exists → true.
		wi.RawHCL["enable_lambda_target"] = "true"
		wi.Names = append(wi.Names, "target_lambda_arn", "enable_lambda_target")

	case KeyAWSKendra:
		// Kendra has NO hard dependency — a bare index is valid. The optional
		// S3 data source is additive: only when aws_s3 is also selected do we
		// wire the stack bucket in, turning the index into an S3-crawling RAG
		// source. The preset gates the data source / access role / policy on a
		// non-null bucket name, so the wire is what stands the connector up.
		if hasS3 {
			s3 := s3Ref(selected)
			wi.RawHCL["s3_bucket_name"] = s3 + ".bucket_name"
			wi.RawHCL["s3_bucket_arn"] = s3 + ".bucket_arn"
			// enable_s3_data_source is the plan-time-known gate for the S3 data
			// source / access role / policy. s3_bucket_name above is a computed
			// output, so the preset can't gate their count on `!= null` (unknown
			// at plan). aws_s3 is selected here, so the data source exists → true.
			wi.RawHCL["enable_s3_data_source"] = "true"
			wi.Names = append(wi.Names, "s3_bucket_name", "s3_bucket_arn", "enable_s3_data_source")
		}

	case KeyAWSBackups:
		// Legacy sessions must Normalize before reaching DefaultWiring;
		// the InsideOut backend's composeradapter does this for us in production.
		enableEbs, enableRds, enableDdb, enableS3 := false, false, false, false
		if comps != nil && comps.AWSBackups != nil {
			enableEbs = boolVal(comps.AWSBackups.EC2)
			enableRds = boolVal(comps.AWSBackups.RDS)
			enableDdb = boolVal(comps.AWSBackups.DynamoDB)
			enableS3 = boolVal(comps.AWSBackups.S3)
		}
		wi.RawHCL["enable_ec2_ebs"] = boolToHCL(enableEbs)
		wi.RawHCL["enable_rds"] = boolToHCL(enableRds)
		wi.RawHCL["enable_dynamodb"] = boolToHCL(enableDdb)
		wi.RawHCL["enable_s3"] = boolToHCL(enableS3)
		wi.Names = append(wi.Names, "enable_ec2_ebs", "enable_rds", "enable_dynamodb", "enable_s3")
		wi.RawHCL["ec2_ebs_rule"] = `{
  selection = {
    resource_arns  = []
    selection_tags = [{ type = "STRINGEQUALS", key = "backup", value = "true" }]
  }
}`
		wi.Names = append(wi.Names, "ec2_ebs_rule")
		// AWS rejects backup selections with both resources=[] and selection_tags=[].
		// For each enabled service, wire the in-stack module's ARN. If the target
		// component isn't in the stack, fall back to a backup=true tag selection
		// so the selection block remains valid.
		hasDynamoDB := selected[KeyAWSDynamoDB]
		tagFallback := `{
  selection = {
    resource_arns  = []
    selection_tags = [{ type = "STRINGEQUALS", key = "backup", value = "true" }]
  }
}`
		if enableRds {
			if hasPostgres {
				wi.RawHCL["rds_rule"] = "{\n  selection = { resource_arns = [" + rdsRef(selected) + ".instance_arn], selection_tags = [] }\n}"
			} else {
				wi.RawHCL["rds_rule"] = tagFallback
			}
			wi.Names = append(wi.Names, "rds_rule")
		}
		if enableDdb {
			if hasDynamoDB {
				wi.RawHCL["dynamodb_rule"] = "{\n  selection = { resource_arns = [" + WireRef(KeyAWSDynamoDB, "table_arn") + "], selection_tags = [] }\n}"
			} else {
				wi.RawHCL["dynamodb_rule"] = tagFallback
			}
			wi.Names = append(wi.Names, "dynamodb_rule")
		}
		if enableS3 {
			if hasS3 {
				wi.RawHCL["s3_rule"] = "{\n  selection = { resource_arns = [" + s3Ref(selected) + ".bucket_arn], selection_tags = [] }\n}"
			} else {
				wi.RawHCL["s3_rule"] = tagFallback
			}
			wi.Names = append(wi.Names, "s3_rule")
		}

	// ==================== GCP Wiring ====================

	case KeyGCPVPC:
		wi.RawHCL["network_name"] = "\"vpc\""
		wi.Names = append(wi.Names, "network_name")
		if selected[KeyGCPCloudRun] || selected[KeyGCPCloudFunctions] {
			wi.RawHCL["enable_serverless_connector"] = "true"
			wi.Names = append(wi.Names, "enable_serverless_connector")
		}

	case KeyGCPGKE:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = WireRef(KeyGCPVPC, "network_self_link")
			wi.RawHCL["subnet_self_link"] = vpcSubnetSelfLinkExpr
			wi.RawHCL["pods_range_name"] = WireRef(KeyGCPVPC, "pods_range_name")
			wi.RawHCL["services_range_name"] = WireRef(KeyGCPVPC, "services_range_name")
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link", "pods_range_name", "services_range_name")
		}

	case KeyGCPLoadbalancer:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = WireRef(KeyGCPVPC, "network_self_link")
			wi.RawHCL["subnet_self_link"] = vpcSubnetSelfLinkExpr
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link")
		}
		if selected[KeyGCPCloudArmor] {
			wi.RawHCL["security_policy"] = WireRef(KeyGCPCloudArmor, "security_policy_id")
			wi.Names = append(wi.Names, "security_policy")
		}

	case KeyGCPCloudSQL:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = WireRef(KeyGCPVPC, "network_self_link")
			wi.Names = append(wi.Names, "network_self_link")
		}

	case KeyGCPMemorystore:
		if selected[KeyGCPVPC] {
			wi.RawHCL["authorized_network"] = WireRef(KeyGCPVPC, "network_self_link")
			wi.Names = append(wi.Names, "authorized_network")
		}

	case KeyGCPCompute:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = WireRef(KeyGCPVPC, "network_self_link")
			wi.RawHCL["subnet_self_link"] = vpcSubnetSelfLinkExpr
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link")
		}

	case KeyGCPBastion:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = WireRef(KeyGCPVPC, "network_self_link")
			wi.RawHCL["subnet_self_link"] = vpcSubnetSelfLinkExpr
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link")
		}

	case KeyGCPCloudRun:
		if selected[KeyGCPVPC] {
			wi.RawHCL["vpc_connector"] = WireRef(KeyGCPVPC, "connector_id")
			wi.Names = append(wi.Names, "vpc_connector")
		}

	case KeyGCPCloudFunctions:
		if selected[KeyGCPVPC] {
			wi.RawHCL["vpc_connector"] = WireRef(KeyGCPVPC, "connector_id")
			wi.Names = append(wi.Names, "vpc_connector")
		}

	case KeyGCPVertexAI:
		// Vertex AI Vector Search (#764). When a VPC is selected, wire its
		// vpc_id (the project-ID path projects/<project_id>/global/networks/<n>)
		// to the preset's network input. The preset extracts the network NAME
		// and rebuilds the project-NUMBER path google_vertex_ai_index_endpoint
		// actually requires (it cannot accept the project-ID form), so the wire
		// stays vpc_id and the form conversion lives in the preset. The endpoint
		// is still PUBLIC unless the operator also sets enable_private_endpoint
		// (the private path needs #774 PSC peering that gcp/vpc lacks today).
		if selected[KeyGCPVPC] {
			wi.RawHCL["network"] = WireRef(KeyGCPVPC, "vpc_id")
			wi.Names = append(wi.Names, "network")
		}
		// When GCS is selected, seed the index from a dedicated prefix under the
		// bucket rather than the bucket root. bucket_url is the gs://<bucket>
		// root; Vertex's contents_delta_uri expects a DIRECTORY of index data
		// files, so ingesting the whole bucket root would build a junk index.
		// Scope it to gs://<bucket>/vertex-index/ — operators stage embedding
		// files there (the index is created empty if the prefix is absent).
		if selected[KeyGCPGCS] {
			wi.RawHCL["contents_delta_uri"] = "\"${" + WireRef(KeyGCPGCS, "bucket_url") + "}/vertex-index/\""
			wi.Names = append(wi.Names, "contents_delta_uri")
		}

	case KeyGCPAgentEngine:
		// Agent Engine (#769). When GCS is selected, wire the bucket as the
		// staging bucket the application stages the packaged agent artifact
		// into. bucket_url is the gs://<bucket> root — the engine's preset
		// validates that the artifact URI (app-layer, supplied separately)
		// lives under this bucket. The artifact itself is NOT wired here: it is
		// built and uploaded by the application layer, not by the composer.
		if selected[KeyGCPGCS] {
			wi.RawHCL["staging_bucket"] = WireRef(KeyGCPGCS, "bucket_url")
			wi.Names = append(wi.Names, "staging_bucket")
		}
	}

	// Observability post-switch wiring (issue #204). Driven off the
	// PricingDependencies driver lists so a component added there
	// gets observability wiring "for free." When the matching
	// aggregator (aws_cloudwatch_monitoring or gcp_cloud_monitoring) is
	// selected, every per-component emitter receives the SNS topic ARN
	// (AWS) or notification channels (GCP) plus an enable_observability
	// = true gate. The aggregator itself is excluded.
	//
	// The per-component module's variables.tf must declare
	// alarm_topic_arn (AWS) or notification_channels (GCP) and
	// enable_observability for these inputs to bind; modules without
	// observability.tf today (lands in C7/C8) accept the wiring as
	// declared-but-unused via the validateRequiredIssues path.
	//
	// Backwards-compat: emitting these inputs is a no-op for any
	// module that hasn't yet adopted the per-component alarm pattern —
	// the module simply ignores variables it doesn't declare. The
	// composer's preset-inspection layer (compose.go) skips wiring
	// that doesn't match a declared variable, so the input never
	// reaches a module that can't consume it.
	if k != KeyAWSCloudWatchMonitoring && CloudFor(k) == "aws" && selected[KeyAWSCloudWatchMonitoring] {
		if slices.Contains(PricingDependencies[KeyAWSCloudWatchMonitoring], k) {
			wi.RawHCL["alarm_topic_arn"] = WireRef(KeyAWSCloudWatchMonitoring, "sns_topic_arn")
			wi.RawHCL["enable_observability"] = "true"
			wi.Names = append(wi.Names, "alarm_topic_arn", "enable_observability")
		}
	}
	if k != KeyGCPCloudMonitoring && CloudFor(k) == "gcp" && selected[KeyGCPCloudMonitoring] {
		if slices.Contains(PricingDependencies[KeyGCPCloudMonitoring], k) {
			wi.RawHCL["notification_channels"] = WireRef(KeyGCPCloudMonitoring, "notification_channels")
			wi.RawHCL["enable_observability"] = "true"
			wi.Names = append(wi.Names, "notification_channels", "enable_observability")
		}
	}

	return wi
}

func boolToHCL(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
