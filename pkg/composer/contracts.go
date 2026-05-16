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
	KeyAWSKMS,
	KeyGCPCloudKMS,
	KeyAWSSecretsManager,
	KeyGCPSecretManager,
	KeyAWSOpenSearch,
	KeyAWSBedrock,
	KeyGCPVertexAI,
	KeyAWSSQS,
	KeyGCPPubSub,
	KeyGCPCloudBuild,
	KeyAWSGitHubActions,
	KeyAWSCodePipeline,
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
	KeyAWSALB:          {KeyAWSVPC},
	KeyGCPLoadbalancer: {KeyGCPVPC},
	KeyAWSBastion:      {KeyAWSVPC},
	KeyAWSRDS:          {KeyAWSVPC},
	KeyGCPCloudSQL:     {KeyGCPVPC},
	KeyAWSElastiCache:  {KeyAWSVPC},
	KeyGCPMemorystore:  {KeyGCPVPC},
	KeyAWSOpenSearch:   {KeyAWSVPC},
	KeyAWSBedrock:      {KeyAWSS3, KeyAWSOpenSearch},
	KeyAWSCloudfront:   {KeyAWSALB},
	KeyAWSEKS:          {KeyAWSVPC},
	KeyAWSECS:          {KeyAWSVPC},
	KeyGCPGKE:          {KeyGCPVPC},
	KeyAWSLambda:       {KeyAWSVPC},
	KeyAWSEKSNodeGroup: {KeyAWSEKS, KeyAWSVPC},
	KeyAWSEC2:          {KeyAWSVPC},
	KeyGCPCompute:      {KeyGCPVPC},
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
	KeyAWSSQS:                  "sqs",
	KeyAWSMSK:                  "msk",
	KeyAWSCloudWatchLogs:       "cloudwatchlogs",
	KeyAWSCloudWatchMonitoring: "cloudwatchmonitoring",
	KeyAWSGrafana:              "grafana",
	KeyAWSCognito:              "cognito",
	KeyAWSBackups:              "backups",
	KeyAWSGitHubActions:        "githubactions",
	KeyAWSCodePipeline:         "codepipeline",
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
	KeyGCPCloudBuild:           "cloud_build",
	KeyGCPFirestore:            "firestore",
	KeyGCPCloudArmor:           "cloud_armor",
	KeyGCPAPIGateway:           "api_gateway",
	KeyGCPBackups:              "backups",
	KeyGCPIdentityPlatform:     "identity_platform",
	KeyGCPCloudRun:             "cloud_run",
	KeyGCPCloudFunctions:       "cloud_functions",
	KeyGCPBastion:              "bastion",
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
	KeyAWSALB,
	KeyAWSAPIGateway,
	KeyAWSBackups,
	KeyAWSBastion,
	KeyAWSBedrock,
	KeyAWSCloudWatchLogs,
	KeyAWSCloudWatchMonitoring,
	KeyAWSCloudfront,
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
	KeyAWSKMS,
	KeyAWSLambda,
	KeyAWSMSK,
	KeyAWSOpenSearch,
	KeyAWSRDS,
	KeyAWSS3,
	KeyAWSSQS,
	KeyAWSSecretsManager,
	KeyAWSVPC,
	KeyAWSWAF,
	// GCP
	KeyGCPAPIGateway,
	KeyGCPBackups,
	KeyGCPBastion,
	KeyGCPCloudArmor,
	KeyGCPCloudBuild,
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
	KeyGCPIdentityPlatform,
	KeyGCPLoadbalancer,
	KeyGCPMemorystore,
	KeyGCPPubSub,
	KeyGCPSecretManager,
	KeyGCPVPC,
	KeyGCPVertexAI,
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
			wi.RawHCL["opensearch_collection_arn"] = opensearchRef(selected) + ".collection_arn"
			wi.Names = append(wi.Names, "opensearch_collection_arn")
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
