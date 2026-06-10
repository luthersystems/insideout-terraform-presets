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
var AWSIAMActions = map[ComponentKey][]string{
	KeyAWSVPC:          {"ec2:AllocateAddress", "ec2:CreateInternetGateway", "ec2:CreateNatGateway", "ec2:CreateRouteTable", "ec2:CreateSubnet", "ec2:CreateVpc"},
	KeyAWSBastion:      {"ec2:RunInstances"},
	KeyAWSEC2:          {"ec2:RunInstances"},
	KeyAWSEKS:          {"eks:CreateCluster", "eks:CreateNodegroup"},
	KeyAWSEKSNodeGroup: {"eks:CreateNodegroup"},
	KeyAWSECS:          {"ecs:CreateCluster", "ecs:CreateService"},
	KeyAWSLambda:       {"lambda:CreateFunction"},
	// App Runner (#598 row 2). Service + autoscaling-config-version
	// CREATE + the access role (only when image_repository_type = ECR)
	// + the instance role + optional VPC connector. PassRole is needed
	// so the App Runner control plane can assume the instance role and
	// (when private ECR) the access role.
	KeyAWSAppRunner: {
		"apprunner:CreateAutoScalingConfiguration",
		"apprunner:CreateService",
		"apprunner:CreateVpcConnector",
		"ec2:CreateSecurityGroup",
		"iam:AttachRolePolicy",
		"iam:CreateRole",
		"iam:PassRole",
	},
	// SageMaker Studio (#615). Domain + user-profile CREATE + the IAM
	// execution role / inline policy + the workspace S3 bucket setup
	// (versioning / encryption / public-access block). PassRole is needed
	// so the SageMaker control plane can assume the execution role we
	// create. Specific create permissions catch the fail-fast surface
	// before terraform hits AWS.
	KeyAWSSageMaker: {
		// CloudWatch alarms (#761 review MED-2). observability.tf creates the
		// invocation-5XX + model-latency alarms by default (enable_observability
		// defaults true) whenever inference is on, so the deploy principal needs
		// PutMetricAlarm to create them and DeleteAlarms so `terraform destroy`
		// can tear them down. Mirrors KeyAWSCloudWatchMonitoring's PutMetricAlarm.
		"cloudwatch:DeleteAlarms",
		"cloudwatch:PutMetricAlarm",
		"iam:AttachRolePolicy",
		"iam:CreateRole",
		"iam:PassRole",
		"iam:PutRolePolicy",
		"s3:CreateBucket",
		"s3:PutBucketPublicAccessBlock",
		"s3:PutBucketVersioning",
		"s3:PutEncryptionConfiguration",
		"sagemaker:CreateDomain",
		"sagemaker:CreateUserProfile",
		// Real-time inference endpoint (#761). Only exercised when
		// enable_inference is set, but listed unconditionally so the
		// pre-deploy SimulatePrincipalPolicy check (ui-core #192) confirms
		// the deploy principal can create the model / endpoint-config /
		// endpoint trio before a deploy attempts it.
		"sagemaker:CreateModel",
		"sagemaker:CreateEndpointConfig",
		"sagemaker:CreateEndpoint",
	},
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
	KeyAWSBedrockAgent:         {"bedrock:CreateAgent"},
	KeyAWSSQS:                  {"sqs:CreateQueue"},
	KeyAWSMSK:                  {"kafka:CreateClusterV2"},
	KeyAWSCloudWatchLogs:       {"logs:CreateLogGroup"},
	KeyAWSCloudWatchMonitoring: {"cloudwatch:PutMetricAlarm"},
	KeyAWSGrafana:              {"grafana:CreateWorkspace"},
	KeyAWSCognito:              {"cognito-idp:CreateUserPool"},
	KeyAWSBackups:              {"backup:CreateBackupPlan", "backup:CreateBackupVault"},
	KeyAWSGitHubActions:        {"iam:CreateOpenIDConnectProvider"},
	// CodeBuild (#619). Project CREATE + the inline service-role
	// policies the preset attaches (logs:CreateLogGroup for the build's
	// CloudWatch Logs group, optional S3 bucket setup when enable_s3_logs
	// is on, EC2 ENI lifecycle perms when the optional VPC config kicks
	// in). PassRole is needed so the CodeBuild control plane can assume
	// the service role we create.
	KeyAWSCodeBuild: {
		"codebuild:CreateProject",
		"ec2:CreateNetworkInterface",
		"iam:AttachRolePolicy",
		"iam:CreateRole",
		"iam:PassRole",
		"iam:PutRolePolicy",
		"logs:CreateLogGroup",
		"s3:CreateBucket",
		"s3:PutBucketPublicAccessBlock",
		"s3:PutBucketVersioning",
		"s3:PutEncryptionConfiguration",
	},
	KeyAWSCodePipeline: {"codepipeline:CreatePipeline"},
	// route53:CreateHostedZone covers the create_zone=true path; the data-
	// lookup path (create_zone=false) needs only route53:GetHostedZone, which
	// is implied by the create capability. Record-set CRUD is covered by
	// ChangeResourceRecordSets (issued via change batches by the provider).
	KeyAWSRoute53: {"route53:ChangeResourceRecordSets", "route53:CreateHostedZone"},
	// ACM (#593). acm:RequestCertificate covers the cert CREATE; the
	// surrounding tag / option / describe operations cover the lifecycle
	// the provider exercises during plan/apply/destroy. Public certs
	// require no extra permission for issuance — DNS validation runs
	// asynchronously inside AWS once the validation records exist.
	KeyAWSACM: {
		"acm:AddTagsToCertificate",
		"acm:DeleteCertificate",
		"acm:DescribeCertificate",
		"acm:ListCertificates",
		"acm:ListTagsForCertificate",
		"acm:RequestCertificate",
		"acm:UpdateCertificateOptions",
	},
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
	// Cloud DNS (#593). managedZones.create + resourceRecordSets.create
	// cover zone + record CRUD; changes.create covers the underlying
	// change-batch API the provider uses. SA needs dns.googleapis.com
	// enabled but the API-enable lives in always-required
	// (serviceusage.services.enable is implicit in resourcemanager.projects.get).
	KeyGCPCloudDNS: {
		"dns.changes.create",
		"dns.managedZones.create",
		"dns.resourceRecordSets.create",
	},
	// GCP GitHub Actions WIF preset (#597 row 1). The deploying SA needs
	// permissions to create the WIF pool + provider, the deploy SA, and
	// project-level IAM bindings on that SA — plus enable the IAM /
	// IAM Credentials / STS APIs.
	KeyGCPGitHubActions: {
		"iam.workloadIdentityPools.create",
		"iam.workloadIdentityPoolProviders.create",
		"iam.serviceAccounts.create",
		"iam.serviceAccounts.setIamPolicy",
		"resourcemanager.projects.setIamPolicy",
		"serviceusage.services.enable",
	},
	// GCP Cloud Deploy delivery-pipeline preset (#613). Deploying SA
	// needs delivery-pipeline + target create capability (the two Cloud
	// Deploy resource types this preset manages), service-account create
	// (for the runner SA), and project-level IAM binding capability (for
	// the single roles/clouddeploy.jobRunner grant on that SA — releaser
	// is granted out-of-stack to the principal that cuts releases), plus
	// the API enable for clouddeploy.googleapis.com.
	KeyGCPCloudDeploy: {
		"clouddeploy.deliveryPipelines.create",
		"clouddeploy.targets.create",
		"iam.serviceAccounts.create",
		"resourcemanager.projects.setIamPolicy",
		"serviceusage.services.enable",
	},
	// Components that need no extra permission beyond the always-required set:
	KeyGCPVPC:          nil,
	KeyGCPCompute:      nil,
	KeyGCPBastion:      nil,
	KeyGCPLoadbalancer: nil, // covered by always-required compute.networks.create.
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
