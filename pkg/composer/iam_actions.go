package composer

import "sort"

// AlwaysRequiredAWSIAMActions are the IAM actions every AWS deploy needs,
// regardless of which composer components the caller selects.
//   - sts:GetCallerIdentity — terraform's AWS provider calls this on init.
//   - iam:CreateRole / iam:PassRole — every stack provisions at least one
//     service-linked role and passes it to a downstream resource.
//   - iam:CreatePolicy / iam:AttachRolePolicy — paired with CreateRole
//     for the policies the role consumes.
//
// Source of truth ui-core consumes (issue #192). Sorted output from
// RequiredAWSIAMActions keeps SimulatePrincipalPolicy input deterministic.
var AlwaysRequiredAWSIAMActions = []string{
	"iam:AttachRolePolicy",
	"iam:CreatePolicy",
	"iam:CreateRole",
	"iam:PassRole",
	"sts:GetCallerIdentity",
}

// AWSIAMActions maps composer ComponentKey to the IAM actions the caller's
// principal needs to apply that component's terraform module on AWS. Values
// are representative create-time actions — SimulatePrincipalPolicy returns
// DENIED if the caller can't perform any one of them, which is sufficient
// to fail fast before terraform apply.
//
// Source of truth for ui-core's awsComponentToIAMActions (issue #192).
// Every AWS-backed key in AllComponentKeys must have an entry here; the
// drift-guard test (TestAWSIAMActions_CoverAllAWSKeys) fails the package
// build otherwise. nil values are permitted for forward-compat.
//
// Polymorphic keys: KeyAWSEKSNodeGroup ("ec2") and KeyAWSEKSControlPlane
// ("resource") share their cloud-prefixed siblings' actions. The
// EKSControlPlane entry lists eks:CreateCluster (its default route); the
// Lambda runtime variant is covered separately by KeyAWSLambda — caller
// resolution happens in GetModuleDir, but IAM is declared per-key here.
var AWSIAMActions = map[ComponentKey][]string{
	KeyAWSVPC:                  {"ec2:AllocateAddress", "ec2:CreateInternetGateway", "ec2:CreateNatGateway", "ec2:CreateRouteTable", "ec2:CreateSubnet", "ec2:CreateVpc"},
	KeyAWSBastion:              {"ec2:RunInstances"},
	KeyAWSEC2:                  {"ec2:RunInstances"},
	KeyAWSEKS:                  {"eks:CreateCluster", "eks:CreateNodegroup"},
	KeyAWSEKSControlPlane:      {"eks:CreateCluster"},   // polymorphic; Lambda variant via KeyAWSLambda
	KeyAWSEKSNodeGroup:         {"eks:CreateNodegroup"}, // polymorphic
	KeyAWSECS:                  {"ecs:CreateCluster", "ecs:CreateService"},
	KeyAWSLambda:               {"lambda:CreateFunction"},
	KeyAWSALB:                  {"elasticloadbalancing:CreateLoadBalancer", "elasticloadbalancing:CreateTargetGroup"},
	KeyAWSCloudfront:           {"cloudfront:CreateDistribution"},
	KeyAWSWAF:                  {"wafv2:CreateWebACL"},
	KeyAWSAPIGateway:           {"apigateway:POST"},
	KeyAWSRDS:                  {"rds:CreateDBInstance", "rds:CreateDBSubnetGroup"},
	KeyAWSElastiCache:          {"elasticache:CreateCacheCluster"},
	KeyAWSDynamoDB:             {"dynamodb:CreateTable"},
	KeyAWSS3:                   {"s3:CreateBucket"},
	KeyAWSKMS:                  {"kms:CreateAlias", "kms:CreateKey"},
	KeyAWSSecretsManager:       {"secretsmanager:CreateSecret"},
	KeyAWSOpenSearch:           {"es:CreateDomain"},
	KeyAWSBedrock:              {"bedrock:GetFoundationModel"},
	KeyAWSSQS:                  {"sqs:CreateQueue"},
	KeyAWSMSK:                  {"kafka:CreateClusterV2"},
	KeyAWSCloudWatchLogs:       {"logs:CreateLogGroup"},
	KeyAWSCloudWatchMonitoring: {"cloudwatch:PutMetricAlarm"},
	KeyAWSGrafana:              {"grafana:CreateWorkspace"},
	KeyAWSCognito:              {"cognito-idp:CreateUserPool"},
	KeyAWSBackups:              {"backup:CreateBackupPlan", "backup:CreateBackupVault"},
	KeyAWSGitHubActions:        {"iam:CreateOpenIDConnectProvider"},
	KeyAWSCodePipeline:         {"codepipeline:CreatePipeline"},
}

// RequiredAWSIAMActions returns the deduplicated, sorted list of IAM
// actions the caller's principal needs on the target account to deploy the
// given components, including AlwaysRequiredAWSIAMActions. Stable order
// keeps SimulatePrincipalPolicy input deterministic and lets test
// snapshots compare cleanly. Unknown component keys are silently ignored
// (forward-compat: a presets release introducing a new component shouldn't
// break in-flight deploys here).
func RequiredAWSIAMActions(components []ComponentKey) []string {
	want := len(AlwaysRequiredAWSIAMActions) + len(components)
	seen := make(map[string]bool, want)
	out := make([]string, 0, want)
	add := func(a string) {
		if seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	for _, a := range AlwaysRequiredAWSIAMActions {
		add(a)
	}
	for _, c := range components {
		for _, a := range AWSIAMActions[c] {
			add(a)
		}
	}
	sort.Strings(out)
	return out
}

// AlwaysRequiredGCPIAMPermissions are the IAM permissions the SA needs on
// the target project for any Luther GCP deploy:
//   - resourcemanager.projects.get — read project metadata.
//   - iam.serviceAccounts.actAs — terraform impersonates SAs it creates.
//   - compute.networks.create — every stack creates at least a VPC.
//
// Source of truth ui-core consumes (issue #192).
var AlwaysRequiredGCPIAMPermissions = []string{
	"compute.networks.create",
	"iam.serviceAccounts.actAs",
	"resourcemanager.projects.get",
}

// GCPIAMPermissions maps composer ComponentKey to the IAM permissions the
// SA needs on the target project to apply each component's terraform
// module. Values are representative create-permissions — testIamPermissions
// checks the SA has at least the create capability, which is sufficient to
// fail fast before terraform apply.
//
// Source of truth for ui-core's gcpComponentToIAMPermissions (issue #192).
// Every GCP-backed key in AllComponentKeys must have an entry here; the
// drift-guard test (TestGCPIAMPermissions_CoverAllGCPKeys) fails the
// package build otherwise. nil values are permitted and used for
// components already covered by AlwaysRequiredGCPIAMPermissions.
var GCPIAMPermissions = map[ComponentKey][]string{
	KeyGCPCloudKMS:         {"cloudkms.keyRings.create"},
	KeyGCPCloudSQL:         {"cloudsql.instances.create"},
	KeyGCPGKE:              {"container.clusters.create"},
	KeyGCPGCS:              {"storage.buckets.create"},
	KeyGCPCloudRun:         {"run.services.create"},
	KeyGCPCloudFunctions:   {"cloudfunctions.functions.create"},
	KeyGCPPubSub:           {"pubsub.topics.create"},
	KeyGCPMemorystore:      {"redis.instances.create"},
	KeyGCPSecretManager:    {"secretmanager.secrets.create"},
	KeyGCPCloudLogging:     {"logging.logEntries.create"},
	KeyGCPCloudMonitoring:  {"monitoring.alertPolicies.create"},
	KeyGCPIdentityPlatform: {"identityplatform.config.update"},
	KeyGCPCloudBuild:       {"cloudbuild.builds.create"},
	KeyGCPFirestore:        {"datastore.databases.create"},
	KeyGCPVertexAI:         {"aiplatform.endpoints.create"},
	KeyGCPAPIGateway:       {"apigateway.gateways.create"},
	KeyGCPBackups:          {"backupdr.managementServers.create"},
	// Components that need no extra permission beyond the always-required set:
	KeyGCPVPC:          nil,
	KeyGCPCompute:      nil,
	KeyGCPBastion:      nil,
	KeyGCPLoadbalancer: nil, // covered by always-required compute.networks.create.
	KeyGCPCloudCDN:     nil, // covered by always-required compute.networks.create.
	KeyGCPCloudArmor:   nil, // covered by always-required compute.networks.create.
}

// RequiredGCPIAMPermissions returns the deduplicated, sorted list of IAM
// permissions the SA needs on the target project to deploy the given
// components, including AlwaysRequiredGCPIAMPermissions. Stable order
// keeps testIamPermissions input deterministic. Unknown component keys
// are silently ignored (forward-compat).
func RequiredGCPIAMPermissions(components []ComponentKey) []string {
	want := len(AlwaysRequiredGCPIAMPermissions) + len(components)
	seen := make(map[string]bool, want)
	out := make([]string, 0, want)
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range AlwaysRequiredGCPIAMPermissions {
		add(p)
	}
	for _, c := range components {
		for _, p := range GCPIAMPermissions[c] {
			add(p)
		}
	}
	sort.Strings(out)
	return out
}
