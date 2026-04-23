package composer

type ComponentKey string

const (
	KeyComposer ComponentKey = "composer"
	KeyArch     ComponentKey = "architecture"
	KeyCloud    ComponentKey = "cloud"

	// KeyEC2 is the polymorphic EKS node-group / Lambda compute key.
	// Distinct from KeyAWSEC2 (EKS node group only); see GetModuleDir.
	KeyEC2 ComponentKey = "ec2"
	// KeyResource is the polymorphic EKS control plane / Lambda runtime key;
	// see GetModuleDir.
	KeyResource ComponentKey = "resource"

	// Deprecated: Use KeyAWSVPC.
	KeyVPC ComponentKey = "vpc"
	// Deprecated: Use KeyAWSBastion.
	KeyBastion ComponentKey = "bastion"
	// Deprecated: Use KeyAWSALB.
	KeyALB ComponentKey = "alb"
	// Deprecated: Use KeyAWSCloudfront.
	KeyCloudfront ComponentKey = "cloudfront"
	// Deprecated: Use KeyAWSWAF.
	KeyWAF ComponentKey = "waf"
	// Deprecated: Use KeyAWSRDS.
	KeyPostgres ComponentKey = "rds"
	// Deprecated: Use KeyAWSElastiCache.
	KeyElastiCache ComponentKey = "elasticache"
	// Deprecated: Use KeyAWSS3.
	KeyS3 ComponentKey = "s3"
	// Deprecated: Use KeyAWSDynamoDB.
	KeyDynamoDB ComponentKey = "dynamodb"
	// Deprecated: Use KeyAWSSQS.
	KeySQS ComponentKey = "sqs"
	// Deprecated: Use KeyAWSMSK.
	KeyMSK ComponentKey = "msk"
	// Deprecated: Use KeyAWSCloudWatchLogs.
	KeyCloudWatchLogs ComponentKey = "cloudwatchlogs"
	// Deprecated: Use KeyAWSCloudWatchMonitoring.
	KeyCloudWatchMonitoring ComponentKey = "cloudwatchmonitoring"

	KeySplunk  ComponentKey = "splunk"
	KeyDatadog ComponentKey = "datadog"

	// Deprecated: Use KeyAWSGrafana.
	KeyGrafana ComponentKey = "grafana"
	// Deprecated: Use KeyAWSCognito.
	KeyCognito ComponentKey = "cognito"
	// Deprecated: Use KeyAWSBackups.
	KeyBackups ComponentKey = "backups"
	// Deprecated: Use KeyAWSGitHubActions.
	KeyGitHubActions ComponentKey = "githubactions"
	// Deprecated: Use KeyAWSCodePipeline.
	KeyCodePipeline ComponentKey = "codepipeline"
	// Deprecated: Use KeyAWSLambda.
	KeyLambda ComponentKey = "lambda"
	// Deprecated: Use KeyAWSAPIGateway.
	KeyAPIGateway ComponentKey = "apigateway"
	// Deprecated: Use KeyAWSKMS.
	KeyKMS ComponentKey = "kms"
	// Deprecated: Use KeyAWSSecretsManager.
	KeySecrets ComponentKey = "secretsmanager"
	// Deprecated: Use KeyAWSOpenSearch.
	KeyOpenSearch ComponentKey = "opensearch"
	// Deprecated: Use KeyAWSBedrock.
	KeyBedrock ComponentKey = "bedrock"

	// AWS components (new prefixed names for v2)
	KeyAWSVPC                  ComponentKey = "aws_vpc"
	KeyAWSBastion              ComponentKey = "aws_bastion"
	KeyAWSEC2                  ComponentKey = "aws_ec2"
	KeyAWSEKS                  ComponentKey = "aws_eks"
	KeyAWSECS                  ComponentKey = "aws_ecs"
	KeyAWSLambda               ComponentKey = "aws_lambda"
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
	KeyAWSSQS                  ComponentKey = "aws_sqs"
	KeyAWSMSK                  ComponentKey = "aws_msk"
	KeyAWSCloudWatchLogs       ComponentKey = "aws_cloudwatch_logs"
	KeyAWSCloudWatchMonitoring ComponentKey = "aws_cloudwatch_monitoring"
	KeyAWSGrafana              ComponentKey = "aws_grafana"
	KeyAWSCognito              ComponentKey = "aws_cognito"
	KeyAWSBackups              ComponentKey = "aws_backups"
	KeyAWSGitHubActions        ComponentKey = "aws_github_actions"
	KeyAWSCodePipeline         ComponentKey = "aws_codepipeline"

	// GCP components
	KeyGCPVPC              ComponentKey = "gcp_vpc"
	KeyGCPBastion          ComponentKey = "gcp_bastion"
	KeyGCPCompute          ComponentKey = "gcp_compute"
	KeyGCPGKE              ComponentKey = "gcp_gke"
	KeyGCPCloudRun         ComponentKey = "gcp_cloud_run"
	KeyGCPCloudFunctions   ComponentKey = "gcp_cloud_functions"
	KeyGCPLoadbalancer     ComponentKey = "gcp_loadbalancer"
	KeyGCPCloudCDN         ComponentKey = "gcp_cloud_cdn"
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
	KeyGCPFirestore        ComponentKey = "gcp_firestore"
	KeyGCPVertexAI         ComponentKey = "gcp_vertex_ai"
	KeyGCPCloudArmor       ComponentKey = "gcp_cloud_armor"
	KeyGCPAPIGateway       ComponentKey = "gcp_api_gateway"
	KeyGCPBackups          ComponentKey = "gcp_backups"
)

var ComposeOrder = []ComponentKey{
	// Match TS intent: deps first, then consumers.
	KeyVPC,
	KeyAWSVPC,
	KeyGCPVPC,
	KeyResource, // EKS cluster or Lambda
	KeyAWSEKS,
	KeyAWSECS,
	KeyGCPGKE,
	KeyGCPCompute,
	KeyGCPBastion,
	KeyGCPCloudRun,
	KeyGCPCloudFunctions,
	KeyLambda, // Alternative key for Lambda
	KeyAWSLambda,
	KeyEC2, // node group after cluster
	KeyAWSEC2,
	KeyBastion,
	KeyAWSBastion,
	KeyALB,
	KeyAWSALB,
	KeyGCPLoadbalancer,
	KeyPostgres,
	KeyAWSRDS,
	KeyGCPCloudSQL,
	KeyElastiCache,
	KeyAWSElastiCache,
	KeyGCPMemorystore,
	KeyGCPFirestore,
	KeyMSK,
	KeyAWSMSK,
	KeyS3,
	KeyAWSS3,
	KeyGCPGCS,
	KeyDynamoDB,
	KeyAWSDynamoDB,
	KeyCloudfront,
	KeyAWSCloudfront,
	KeyGCPCloudCDN,
	KeyWAF,
	KeyAWSWAF,
	KeyGCPCloudArmor,
	KeyBackups,
	KeyAWSBackups,
	KeyGCPBackups,
	KeyCloudWatchLogs,
	KeyAWSCloudWatchLogs,
	KeyGCPCloudLogging,
	KeyCloudWatchMonitoring,
	KeyAWSCloudWatchMonitoring,
	KeyGCPCloudMonitoring,
	KeySplunk,
	KeyDatadog,
	KeyGrafana,
	KeyAWSGrafana,
	KeyCognito,
	KeyAWSCognito,
	KeyGCPIdentityPlatform,
	KeyAPIGateway,
	KeyAWSAPIGateway,
	KeyGCPAPIGateway,
	KeyKMS,
	KeyAWSKMS,
	KeyGCPCloudKMS,
	KeySecrets,
	KeyAWSSecretsManager,
	KeyGCPSecretManager,
	KeyOpenSearch,
	KeyAWSOpenSearch,
	KeyBedrock,
	KeyAWSBedrock,
	KeyGCPVertexAI,
	KeySQS,
	KeyAWSSQS,
	KeyGCPPubSub,
	KeyGCPCloudBuild,
	KeyGitHubActions,
	KeyAWSGitHubActions,
	KeyCodePipeline,
	KeyAWSCodePipeline,
	KeyArch,
	KeyCloud,
	KeyComposer,
}

// ModulePath defines the base directory for each component's preset.
var ModulePath = map[ComponentKey]string{
	KeyVPC:                  "modules/vpc",
	KeyEC2:                  "modules/eks_nodegroup", // EKS managed node group
	KeyResource:             "modules/eks", // EKS cluster (default)
	KeyALB:                  "modules/alb",
	KeyCloudfront:           "modules/cloudfront",
	KeyWAF:                  "modules/waf",
	KeyPostgres:             "modules/rds",
	KeyElastiCache:          "modules/elasticache",
	KeyS3:                   "modules/s3",
	KeyDynamoDB:             "modules/dynamodb",
	KeySQS:                  "modules/sqs",
	KeyMSK:                  "modules/msk",
	KeyCloudWatchLogs:       "modules/cloudwatchlogs",
	KeyCloudWatchMonitoring: "modules/cloudwatchmonitoring",
	KeySplunk:               "modules/splunk",
	KeyDatadog:              "modules/datadog",
	KeyGrafana:              "modules/grafana",
	KeyCognito:              "modules/cognito",
	KeyBackups:              "modules/backups",
	KeyBastion:              "modules/bastion",
	KeyGitHubActions:        "modules/githubactions",
	KeyCodePipeline:         "modules/codepipeline",
	KeyLambda:               "modules/lambda",
	KeyAPIGateway:           "modules/apigateway",
	KeyKMS:                  "modules/kms",
	KeySecrets:              "modules/secretsmanager",
	KeyOpenSearch:           "modules/opensearch",
	KeyBedrock:              "modules/bedrock",

	// AWS (new prefixed names)
	KeyAWSVPC:                  "modules/vpc",
	KeyAWSEC2:                  "modules/ec2",
	KeyAWSEKS:                  "modules/eks",
	KeyAWSECS:                  "modules/ecs",
	KeyAWSLambda:               "modules/lambda",
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
	KeyAWSSQS:                  "modules/sqs",
	KeyAWSMSK:                  "modules/msk",
	KeyAWSCloudWatchLogs:       "modules/cloudwatchlogs",
	KeyAWSCloudWatchMonitoring: "modules/cloudwatchmonitoring",
	KeyAWSGrafana:              "modules/grafana",
	KeyAWSCognito:              "modules/cognito",
	KeyAWSBackups:              "modules/backups",
	KeyAWSBastion:              "modules/bastion",
	KeyAWSGitHubActions:        "modules/githubactions",
	KeyAWSCodePipeline:         "modules/codepipeline",

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
	KeyGCPPubSub:           "gcp/pubsub",
	KeyGCPCloudLogging:     "gcp/cloud_logging",
	KeyGCPCloudMonitoring:  "gcp/cloud_monitoring",
	KeyGCPIdentityPlatform: "gcp/identity_platform",
	KeyGCPCloudBuild:       "gcp/cloud_build",
	KeyGCPBackups:          "gcp/backups",
}

// ImplicitDependencies defines components that must be automatically added
// if a certain component is selected.
var ImplicitDependencies = map[ComponentKey][]ComponentKey{
	KeyALB:             {KeyVPC},
	KeyAWSALB:          {KeyAWSVPC},
	KeyGCPLoadbalancer: {KeyGCPVPC},
	KeyBastion:         {KeyVPC},
	KeyAWSBastion:      {KeyAWSVPC},
	KeyPostgres:        {KeyVPC},
	KeyAWSRDS:          {KeyAWSVPC},
	KeyGCPCloudSQL:     {KeyGCPVPC},
	KeyElastiCache:     {KeyVPC},
	KeyAWSElastiCache:  {KeyAWSVPC},
	KeyGCPMemorystore:  {KeyGCPVPC},
	KeyOpenSearch:      {KeyVPC},
	KeyAWSOpenSearch:   {KeyAWSVPC},
	KeyBedrock:         {KeyS3, KeyOpenSearch},
	KeyAWSBedrock:      {KeyAWSS3, KeyAWSOpenSearch},
	KeyCloudfront:      {KeyALB},
	KeyAWSCloudfront:   {KeyAWSALB},
	KeyGCPCloudCDN:     {KeyGCPLoadbalancer},
	// KeyResource and KeyEC2 are polymorphic keys that Phase 4 will rename to
	// unambiguous prefixed names; their implicit deps target KeyAWSVPC (not
	// legacy KeyVPC) so that a direct caller selecting only a polymorphic key
	// still produces a prefixed-only stack.
	KeyResource:       {KeyAWSVPC},
	KeyAWSEKS:         {KeyAWSVPC},
	KeyAWSECS:         {KeyAWSVPC},
	KeyGCPGKE:         {KeyGCPVPC},
	KeyLambda:         {KeyVPC},
	KeyAWSLambda:      {KeyAWSVPC},
	KeyEC2:            {KeyResource, KeyAWSVPC},
	KeyAWSEC2:         {KeyAWSVPC},
	KeyGCPCompute:      {KeyGCPVPC},
}

// LegacyToV2Key maps legacy (unprefixed) component keys to their V2 (aws_-prefixed) equivalents.
// Used by DeduplicateKeys to remove legacy duplicates when both forms are present.
//
// Deprecated: part of the reliable-legacy compat layer tracked by issue #76.
// New code should work with KeyAWS*-prefixed keys directly; legacy session
// payloads should be normalised by reliable's composeradapter before reaching
// composer.
var LegacyToV2Key = map[ComponentKey]ComponentKey{
	KeyVPC:                  KeyAWSVPC,
	KeyALB:                  KeyAWSALB,
	KeyBastion:              KeyAWSBastion,
	KeyPostgres:             KeyAWSRDS,
	KeyElastiCache:          KeyAWSElastiCache,
	KeyS3:                   KeyAWSS3,
	KeyDynamoDB:             KeyAWSDynamoDB,
	KeySQS:                  KeyAWSSQS,
	KeyMSK:                  KeyAWSMSK,
	KeyCloudWatchLogs:       KeyAWSCloudWatchLogs,
	KeyCloudWatchMonitoring: KeyAWSCloudWatchMonitoring,
	KeyCognito:              KeyAWSCognito,
	KeyBackups:              KeyAWSBackups,
	KeyGitHubActions:        KeyAWSGitHubActions,
	KeyCodePipeline:         KeyAWSCodePipeline,
	KeyLambda:               KeyAWSLambda,
	KeyAPIGateway:           KeyAWSAPIGateway,
	KeyKMS:                  KeyAWSKMS,
	KeySecrets:              KeyAWSSecretsManager,
	KeyOpenSearch:           KeyAWSOpenSearch,
	KeyBedrock:              KeyAWSBedrock,
	KeyCloudfront:           KeyAWSCloudfront,
	KeyWAF:                  KeyAWSWAF,
}

// DeduplicateKeys removes legacy keys when their V2 equivalent is also present.
// For example, if both KeyVPC and KeyAWSVPC are in keys, only KeyAWSVPC is kept.
// This prevents duplicate Terraform module blocks for the same infrastructure.
//
// Deprecated: part of the reliable-legacy compat layer tracked by issue #76.
// Callers that already produce AWS-prefixed keys should not need this.
func DeduplicateKeys(keys []ComponentKey) []ComponentKey {
	present := make(map[ComponentKey]bool, len(keys))
	for _, k := range keys {
		present[k] = true
	}

	result := make([]ComponentKey, 0, len(keys))
	for _, k := range keys {
		if v2, isLegacy := LegacyToV2Key[k]; isLegacy && present[v2] {
			continue // skip legacy key — V2 equivalent is present
		}
		result = append(result, k)
	}
	return result
}

// ResolveDependencies recursively finds all required components for a given set of keys.
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

// GetModuleDir returns the output directory for a key (e.g., "modules/vpc").
// This is where the composed terraform files are placed.
func GetModuleDir(k ComponentKey, comps *Components) string {
	if k == KeyResource && isLambda(comps) {
		return ModulePath[KeyLambda]
	}
	return ModulePath[k]
}

// PresetKeyMap maps component keys to their preset directory names.
// Used when the preset name differs from the component key.
var PresetKeyMap = map[ComponentKey]string{
	KeyPostgres:         "rds", // KeyPostgres uses "rds" preset
	KeyEC2:              "eks_nodegroup", // legacy KeyEC2 is the EKS managed node group
	KeyAWSVPC:           "vpc",
	KeyAWSBastion:       "bastion",
	KeyAWSEC2:           "ec2",
	KeyAWSEKS:           "resource", // Uses the same preset as KeyResource (aws/resource/)
	KeyAWSECS:           "ecs",
	KeyAWSLambda:        "lambda",
	KeyAWSALB:           "alb",
	KeyAWSCloudfront:    "cloudfront",
	KeyAWSWAF:           "waf",
	KeyAWSAPIGateway:    "apigateway",
	KeyAWSRDS:           "rds",
	KeyAWSElastiCache:   "elasticache",
	KeyAWSDynamoDB:      "dynamodb",
	KeyAWSOpenSearch:    "opensearch",
	KeyAWSS3:            "s3",
	KeyAWSKMS:           "kms",
	KeyAWSSecretsManager: "secretsmanager",
	KeyAWSBedrock:       "bedrock",
	KeyAWSSQS:           "sqs",
	KeyAWSMSK:           "msk",
	KeyAWSCloudWatchLogs: "cloudwatchlogs",
	KeyAWSCloudWatchMonitoring: "cloudwatchmonitoring",
	KeyAWSGrafana:       "grafana",
	KeyAWSCognito:       "cognito",
	KeyAWSBackups:       "backups",
	KeyAWSGitHubActions: "githubactions",
	KeyAWSCodePipeline:  "codepipeline",
	KeyGCPVPC:           "vpc",
	KeyGCPCompute:       "compute",
	KeyGCPGKE:           "gke",
	KeyGCPLoadbalancer:  "loadbalancer",
	KeyGCPCloudCDN:      "cloud_cdn",
	KeyGCPCloudSQL:      "cloudsql",
	KeyGCPMemorystore:   "memorystore",
	KeyGCPGCS:           "gcs",
	KeyGCPCloudLogging:  "cloud_logging",
	KeyGCPSecretManager:    "secretmanager",
	KeyGCPCloudKMS:         "kms",
	KeyGCPPubSub:           "pubsub",
	KeyGCPCloudMonitoring:  "cloud_monitoring",
	KeyGCPVertexAI:         "vertex_ai",
	KeyGCPCloudBuild:       "cloud_build",
	KeyGCPFirestore:        "firestore",
	KeyGCPCloudArmor:       "cloud_armor",
	KeyGCPAPIGateway:       "api_gateway",
	KeyGCPBackups:          "backups",
	KeyGCPIdentityPlatform: "identity_platform",
	KeyGCPCloudRun:         "cloud_run",
	KeyGCPCloudFunctions:   "cloud_functions",
	KeyGCPBastion:          "bastion",
}

// GetPresetPath returns the cloud-prefixed preset path for a component.
// For example: GetPresetPath("aws", KeyVPC, nil) returns "aws/vpc"
func GetPresetPath(cloud string, k ComponentKey, comps *Components) string {
	presetName := string(k)

	// Handle special cases where preset name differs from key
	if mapped, ok := PresetKeyMap[k]; ok {
		presetName = mapped
	}

	// Handle dynamic resource -> lambda mapping
	if k == KeyResource && isLambda(comps) {
		presetName = string(KeyLambda)
	}

	return cloud + "/" + presetName
}

func isLambda(comps *Components) bool {
	if comps == nil {
		return false
	}
	return comps.IsLambdaArchitecture()
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

// Module-reference helpers return "module.<name>" paths used by wiring to
// cross-reference resources. Callers with legacy ComponentKey selections
// must Normalize / use the composeradapter so the `selected` map carries
// KeyAWS* keys; ComposeStack rejects purely-legacy SelectedKeys at entry.
// EmitRootMainTF auto-emits `moved {}` blocks for the rename transition.

func vpcRef(selected map[ComponentKey]bool) string {
	if selected[KeyGCPVPC] {
		return "module.gcp_vpc"
	}
	return "module.aws_vpc"
}

func albRef(_ map[ComponentKey]bool) string       { return "module.aws_alb" }
func wafRef(_ map[ComponentKey]bool) string       { return "module.aws_waf" }
func bastionRef(_ map[ComponentKey]bool) string   { return "module.aws_bastion" }
func rdsRef(_ map[ComponentKey]bool) string       { return "module.aws_rds" }
func s3Ref(_ map[ComponentKey]bool) string        { return "module.aws_s3" }
func opensearchRef(_ map[ComponentKey]bool) string { return "module.aws_opensearch" }
func sqsRef(_ map[ComponentKey]bool) string       { return "module.aws_sqs" }

// resourceRef returns the EKS/ECS module reference for the selected stack.
// Prefers the prefixed KeyAWSEKS / KeyAWSECS keys, with a KeyResource path
// for the polymorphic selection Phase 4 has not yet renamed. Falls back to
// module.aws_eks when nothing matches — the final fallback is effectively
// unreachable (wiring only runs when `hasResource` is true) but picks the
// prefixed name defensively.
func resourceRef(selected map[ComponentKey]bool) string {
	if selected[KeyAWSEKS] {
		return "module.aws_eks"
	}
	if selected[KeyAWSECS] {
		return "module.aws_ecs"
	}
	if selected[KeyResource] {
		return "module.resource"
	}
	return "module.aws_eks"
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
	hasResource := selected[KeyAWSEKS] || selected[KeyResource]

	switch k {

	/* ---------------- VPC fans out ---------------- */

	case KeyAWSALB:
		if hasVPC {
			vpc := vpcRef(selected)
			wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
			wi.RawHCL["public_subnet_ids"] = vpc + ".public_subnet_ids"
			wi.Names = append(wi.Names, "vpc_id", "public_subnet_ids")
		}

	case KeyResource, KeyAWSEKS:
		if isLambda(comps) {
			// Lambda Wiring
			if hasVPC {
				vpc := vpcRef(selected)
				wi.RawHCL["enable_vpc"] = "true"
				wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
				wi.RawHCL["subnet_ids"] = vpc + ".private_subnet_ids"
				wi.RawHCL["security_group_ids"] = "[]"
				wi.Names = append(wi.Names, "enable_vpc", "vpc_id", "subnet_ids", "security_group_ids")
			}
		} else {
			// EKS Wiring
			if hasVPC {
				vpc := vpcRef(selected)
				wi.RawHCL["vpc_id"] = vpc + ".vpc_id"
				wi.RawHCL["private_subnet_ids"] = vpc + ".private_subnet_ids"
				wi.RawHCL["public_subnet_ids"] = vpc + ".public_subnet_ids"
				wi.Names = append(wi.Names, "vpc_id", "private_subnet_ids", "public_subnet_ids")
			}
			wi.RawHCL["cluster_enabled_log_types"] = `["api", "audit", "authenticator", "controllerManager", "scheduler"]`
			wi.Names = append(wi.Names, "cluster_enabled_log_types")
		}

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

	case KeyEC2:
		if hasResource && !isLambda(comps) {
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

	case KeyAWSCloudWatchMonitoring:
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
			wi.RawHCL["opensearch_collection_arn"] = opensearchRef(selected) + ".collection_arn"
			wi.Names = append(wi.Names, "opensearch_collection_arn")
		}

	case KeyAWSBackups:
		// Legacy sessions must Normalize before reaching DefaultWiring;
		// reliable's composeradapter does this for us in production.
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
				wi.RawHCL["dynamodb_rule"] = "{\n  selection = { resource_arns = [module.aws_dynamodb.table_arn], selection_tags = [] }\n}"
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
			wi.RawHCL["network_self_link"] = "module.gcp_vpc.network_self_link"
			wi.RawHCL["subnet_self_link"] = "module.gcp_vpc.subnet_self_links[0]"
			wi.RawHCL["pods_range_name"] = "module.gcp_vpc.pods_range_name"
			wi.RawHCL["services_range_name"] = "module.gcp_vpc.services_range_name"
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link", "pods_range_name", "services_range_name")
		}

	case KeyGCPLoadbalancer:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = "module.gcp_vpc.network_self_link"
			wi.RawHCL["subnet_self_link"] = "module.gcp_vpc.subnet_self_links[0]"
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link")
		}
		if selected[KeyGCPCloudCDN] {
			wi.RawHCL["enable_cdn"] = "true"
			wi.Names = append(wi.Names, "enable_cdn")
		}
		if selected[KeyGCPCloudArmor] {
			wi.RawHCL["security_policy"] = "module.gcp_cloud_armor.security_policy_id"
			wi.Names = append(wi.Names, "security_policy")
		}

	case KeyGCPCloudSQL:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = "module.gcp_vpc.network_self_link"
			wi.Names = append(wi.Names, "network_self_link")
		}

	case KeyGCPMemorystore:
		if selected[KeyGCPVPC] {
			wi.RawHCL["authorized_network"] = "module.gcp_vpc.network_self_link"
			wi.Names = append(wi.Names, "authorized_network")
		}

	case KeyGCPCompute:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = "module.gcp_vpc.network_self_link"
			wi.RawHCL["subnet_self_link"] = "module.gcp_vpc.subnet_self_links[0]"
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link")
		}

	case KeyGCPBastion:
		if selected[KeyGCPVPC] {
			wi.RawHCL["network_self_link"] = "module.gcp_vpc.network_self_link"
			wi.RawHCL["subnet_self_link"] = "module.gcp_vpc.subnet_self_links[0]"
			wi.Names = append(wi.Names, "network_self_link", "subnet_self_link")
		}

	case KeyGCPCloudRun:
		if selected[KeyGCPVPC] {
			wi.RawHCL["vpc_connector"] = "module.gcp_vpc.connector_id"
			wi.Names = append(wi.Names, "vpc_connector")
		}

	case KeyGCPCloudFunctions:
		if selected[KeyGCPVPC] {
			wi.RawHCL["vpc_connector"] = "module.gcp_vpc.connector_id"
			wi.Names = append(wi.Names, "vpc_connector")
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
