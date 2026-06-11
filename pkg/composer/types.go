package composer

// Components mirrors the TypeScript ZComponentsIR schema with cloud-specific field names.
// Use aws_* fields for AWS cloud, gcp_* fields for GCP cloud.
type Components struct {
	// Cloud-agnostic fields
	Architecture string `json:"architecture,omitempty"`
	Cloud        string `json:"cloud,omitempty"`

	// CpuArch is a stack-level default CPU architecture ("Intel" | "ARM").
	//
	// Deprecated: prefer per-component arch fields (AWSEC2, GCPCompute).
	// The mapper reads per-component arch first and only consults CpuArch
	// as a fallback for callers that have not migrated. New code MUST set
	// AWSEC2/GCPCompute directly; CpuArch will be removed in a future
	// release after all known consumers migrate.
	CpuArch string `json:"cpu_arch,omitempty"`

	// ==================== AWS Components ====================
	AWSVPC                  string `json:"aws_vpc,omitempty"` // "Private" or "Public"
	AWSBastion              *bool  `json:"aws_bastion,omitempty"`
	AWSEC2                  string `json:"aws_ec2,omitempty"` // "Intel" or "ARM" or empty for boolean
	AWSEKS                  *bool  `json:"aws_eks,omitempty"`
	AWSECS                  *bool  `json:"aws_ecs,omitempty"`
	AWSLambda               *bool  `json:"aws_lambda,omitempty"`
	AWSAppRunner            *bool  `json:"aws_apprunner,omitempty"`
	AWSSageMaker            *bool  `json:"aws_sagemaker,omitempty"`
	AWSALB                  *bool  `json:"aws_alb,omitempty"`
	AWSCloudFront           *bool  `json:"aws_cloudfront,omitempty"`
	AWSWAF                  *bool  `json:"aws_waf,omitempty"`
	AWSAPIGateway           *bool  `json:"aws_apigateway,omitempty"`
	AWSRDS                  *bool  `json:"aws_rds,omitempty"`
	AWSElastiCache          *bool  `json:"aws_elasticache,omitempty"`
	AWSDynamoDB             *bool  `json:"aws_dynamodb,omitempty"`
	AWSOpenSearch           *bool  `json:"aws_opensearch,omitempty"`
	AWSS3                   *bool  `json:"aws_s3,omitempty"`
	AWSKMS                  *bool  `json:"aws_kms,omitempty"`
	AWSSecretsManager       *bool  `json:"aws_secretsmanager,omitempty"`
	AWSBedrock              *bool  `json:"aws_bedrock,omitempty"`
	AWSBedrockAgent         *bool  `json:"aws_bedrock_agent,omitempty"`
	AWSSQS                  *bool  `json:"aws_sqs,omitempty"`
	AWSMSK                  *bool  `json:"aws_msk,omitempty"`
	AWSCloudWatchLogs       *bool  `json:"aws_cloudwatch_logs,omitempty"`
	AWSCloudWatchMonitoring *bool  `json:"aws_cloudwatch_monitoring,omitempty"`
	AWSGrafana              *bool  `json:"aws_grafana,omitempty"`
	AWSCognito              *bool  `json:"aws_cognito,omitempty"`
	AWSGitHubActions        *bool  `json:"aws_github_actions,omitempty"`
	AWSCodeBuild            *bool  `json:"aws_codebuild,omitempty"`
	AWSCodePipeline         *bool  `json:"aws_codepipeline,omitempty"`
	AWSRoute53              *bool  `json:"aws_route53,omitempty"`
	AWSACM                  *bool  `json:"aws_acm,omitempty"`
	AWSBackups              *struct {
		EC2         *bool `json:"aws_ec2,omitempty"`
		RDS         *bool `json:"aws_rds,omitempty"`
		ElastiCache *bool `json:"aws_elasticache,omitempty"`
		DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
		S3          *bool `json:"aws_s3,omitempty"`
	} `json:"aws_backups,omitempty"`

	// ==================== GCP Components ====================
	GCPVPC              *bool  `json:"gcp_vpc,omitempty"`
	GCPBastion          *bool  `json:"gcp_bastion,omitempty"`
	GCPCompute          string `json:"gcp_compute,omitempty"` // "Intel" or "ARM" or empty for boolean
	GCPGKE              *bool  `json:"gcp_gke,omitempty"`
	GCPCloudRun         *bool  `json:"gcp_cloud_run,omitempty"`
	GCPCloudFunctions   *bool  `json:"gcp_cloud_functions,omitempty"`
	GCPLoadbalancer     *bool  `json:"gcp_loadbalancer,omitempty"`
	GCPCloudArmor       *bool  `json:"gcp_cloud_armor,omitempty"`
	GCPAPIGateway       *bool  `json:"gcp_api_gateway,omitempty"`
	GCPCloudSQL         *bool  `json:"gcp_cloudsql,omitempty"`
	GCPMemorystore      *bool  `json:"gcp_memorystore,omitempty"`
	GCPFirestore        *bool  `json:"gcp_firestore,omitempty"`
	GCPGCS              *bool  `json:"gcp_gcs,omitempty"`
	GCPCloudKMS         *bool  `json:"gcp_cloud_kms,omitempty"`
	GCPSecretManager    *bool  `json:"gcp_secret_manager,omitempty"`
	GCPVertexAI         *bool  `json:"gcp_vertex_ai,omitempty"`
	GCPPubSub           *bool  `json:"gcp_pubsub,omitempty"`
	GCPCloudLogging     *bool  `json:"gcp_cloud_logging,omitempty"`
	GCPCloudMonitoring  *bool  `json:"gcp_cloud_monitoring,omitempty"`
	GCPIdentityPlatform *bool  `json:"gcp_identity_platform,omitempty"`
	GCPCloudBuild       *bool  `json:"gcp_cloud_build,omitempty"`
	GCPCloudDeploy      *bool  `json:"gcp_cloud_deploy,omitempty"`
	GCPCloudDNS         *bool  `json:"gcp_cloud_dns,omitempty"`
	GCPGitHubActions    *bool  `json:"gcp_github_actions,omitempty"`
	GCPBackups          *struct {
		Compute  *bool `json:"gcp_compute,omitempty"`
		CloudSQL *bool `json:"gcp_cloudsql,omitempty"`
		GCS      *bool `json:"gcp_gcs,omitempty"`
	} `json:"gcp_backups,omitempty"`

	// ==================== External/Third-Party ====================
	Splunk        *bool `json:"splunk,omitempty"`
	Datadog       *bool `json:"datadog,omitempty"`
	GitHubActions *bool `json:"githubactions,omitempty"` // External GitHub Actions (not aws_github_actions OIDC)
}

// Config mirrors the TypeScript ZConfigIR schema with cloud-specific field names.
type Config struct {
	Region string `json:"region,omitempty"`
	Cloud  string `json:"cloud,omitempty"`

	// Estimated usage for cost calculation (Serverless/Lambda/Cloud Run)
	EstimatedMonthlyRequests int64 `json:"estimated_monthly_requests,omitempty"`
	EstimatedAvgDurationMs   int   `json:"estimated_avg_duration_ms,omitempty"`

	// ==================== AWS Configuration ====================
	AWSEC2 *struct {
		InstanceType          string `json:"instanceType,omitempty"`
		NumServers            string `json:"numServers,omitempty"`
		NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
		DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
		UserData              string `json:"userData,omitempty"`
		UserDataURL           string `json:"userDataURL,omitempty"`
		CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
		SSHPublicKey          string `json:"sshPublicKey,omitempty"`
		EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		// GPUEnabled selects an NVIDIA-GPU AMI for the instance (#759). Pair
		// with a GPU InstanceType (g4dn/g5/g6/p4d/p5, ...). GPU AMIs are
		// x86_64-only; the preset rejects gpu_enabled with arm64.
		GPUEnabled *bool `json:"gpuEnabled,omitempty"`
	} `json:"aws_ec2,omitempty"`

	AWSEKS *struct {
		HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
		ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
		DesiredSize            string `json:"desiredSize,omitempty"`
		MaxSize                string `json:"maxSize,omitempty"`
		MinSize                string `json:"minSize,omitempty"`
		InstanceType           string `json:"instanceType,omitempty"`
		// GPUEnabled provisions a GPU node group (#759): when true and no
		// explicit GPU InstanceType is given, the mapper defaults
		// instance_types to g5.xlarge and sets ami_type to
		// AL2023_x86_64_NVIDIA. The in-cluster NVIDIA device plugin is
		// app-layer and out of preset scope.
		GPUEnabled *bool `json:"gpuEnabled,omitempty"`
	} `json:"aws_eks,omitempty"`

	AWSECS *struct {
		EnableContainerInsights *bool    `json:"enableContainerInsights,omitempty"`
		CapacityProviders       []string `json:"capacityProviders,omitempty"`
		DefaultCapacityProvider string   `json:"defaultCapacityProvider,omitempty"`
		EnableServiceConnect    *bool    `json:"enableServiceConnect,omitempty"`
	} `json:"aws_ecs,omitempty"`

	// AWSVPC surfaces the preset's VPC topology knobs. All fields are pointers
	// so the zero value means "defer to the module's HCL default" rather than
	// "force to zero". SingleNATGateway=false spreads NAT gateways across AZs
	// (one per AZ, bounded by AZCount) — trade higher cost for AZ-level HA and
	// to avoid exhausting the per-AZ NAT gateway quota when many stacks share
	// an account.
	AWSVPC *struct {
		SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
		EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
		AZCount          *int  `json:"azCount,omitempty"`
	} `json:"aws_vpc,omitempty"`

	AWSCloudfront *struct {
		DefaultTtl *string `json:"defaultTtl,omitempty"`
		OriginPath *string `json:"originPath,omitempty"`
		CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
	} `json:"aws_cloudfront,omitempty"`

	AWSRDS *struct {
		CPUSize      string `json:"cpuSize,omitempty"`
		ReadReplicas string `json:"readReplicas,omitempty"`
		StorageSize  string `json:"storageSize,omitempty"`
	} `json:"aws_rds,omitempty"`

	AWSElastiCache *struct {
		HA       *bool  `json:"ha,omitempty"`
		Storage  string `json:"storageSize,omitempty"`
		NodeSize string `json:"nodeSize,omitempty"`
		Replicas string `json:"replicas,omitempty"`
	} `json:"aws_elasticache,omitempty"`

	AWSS3 *struct {
		Versioning *bool `json:"versioning,omitempty"`
	} `json:"aws_s3,omitempty"`

	AWSDynamoDB *struct {
		Type string `json:"type,omitempty"`
	} `json:"aws_dynamodb,omitempty"`

	AWSSQS *struct {
		Type              string `json:"type,omitempty"`
		VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
	} `json:"aws_sqs,omitempty"`

	AWSMSK *struct {
		Retention string `json:"retentionPeriod,omitempty"`
	} `json:"aws_msk,omitempty"`

	AWSCloudWatchLogs *struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	} `json:"aws_cloudwatch_logs,omitempty"`

	AWSCloudWatchMonitoring *struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	} `json:"aws_cloudwatch_monitoring,omitempty"`

	AWSCognito *struct {
		SignInType  string `json:"signInType,omitempty"`
		MFARequired *bool  `json:"mfaRequired,omitempty"`
		MFAFactor   string `json:"mfaFactor,omitempty"`
		Okta        *struct {
			SelfSignupAllowed *bool `json:"selfSignupAllowed,omitempty"`
		} `json:"okta,omitempty"`
		Auth0 *struct {
			MFARequired *bool `json:"mfaRequired,omitempty"`
		} `json:"auth0,omitempty"`
	} `json:"aws_cognito,omitempty"`

	AWSLambda *struct {
		Runtime    string `json:"runtime,omitempty"`
		MemorySize string `json:"memorySize,omitempty"`
		Timeout    string `json:"timeout,omitempty"`
	} `json:"aws_lambda,omitempty"`

	// AWSAppRunner carries the caller-supplied App Runner config (#598
	// row 2). Named (not inline) so callers can construct it without
	// re-typing the anonymous struct shape at every site, and so future
	// field additions don't force every test instantiation to be touched
	// (matches the GCPCloudDeployConfig + AWSSageMakerConfig pattern).
	AWSAppRunner *AWSAppRunnerConfig `json:"aws_apprunner,omitempty"`

	// AWSSageMaker carries the caller-supplied SageMaker Studio config
	// (#615). Named (not inline) so callers can construct it without
	// re-typing the anonymous struct shape at every site, and so future
	// field additions don't force every test instantiation to be touched
	// (matches the GCPGitHubActionsConfig pattern set in #597).
	AWSSageMaker *AWSSageMakerConfig `json:"aws_sagemaker,omitempty"`

	// AWSCodeBuild carries the caller-supplied CodeBuild project config
	// (#619). Named (not inline) so callers can construct it without
	// re-typing the anonymous struct shape at every site, and so future
	// field additions don't force every test instantiation to be touched
	// (matches the AWSAppRunnerConfig + AWSSageMakerConfig pattern).
	AWSCodeBuild *AWSCodeBuildConfig `json:"aws_codebuild,omitempty"`

	AWSAPIGateway *struct {
		DomainName     string `json:"domainName,omitempty"`
		CertificateArn string `json:"certificateArn,omitempty"`
	} `json:"aws_api_gateway,omitempty"`

	// AWSRoute53 carries the caller-supplied Route 53 configuration. DomainName
	// is the apex (e.g. "example.com") and is required when KeyAWSRoute53 is
	// selected. CreateZone toggles between creating a hosted zone in-stack
	// (true) and looking up an existing one by ZoneID (false). PrivateZone +
	// VPCIDs are only consulted when CreateZone is true and the zone is
	// intended to be private. Plain records and aliases (in addition to the
	// auto-derived aliases the composer wires from ALB / CloudFront — see
	// DefaultWiring at KeyAWSRoute53) flow through the same-named module
	// variables.
	AWSRoute53 *struct {
		DomainName   string   `json:"domainName,omitempty"`
		CreateZone   *bool    `json:"createZone,omitempty"`
		ZoneID       string   `json:"zoneId,omitempty"`
		PrivateZone  *bool    `json:"privateZone,omitempty"`
		VPCIDs       []string `json:"vpcIds,omitempty"`
		ForceDestroy *bool    `json:"forceDestroy,omitempty"`
	} `json:"aws_route53,omitempty"`

	// AWSACM carries the caller-supplied ACM certificate configuration.
	// DomainName is the primary FQDN; SubjectAlternativeNames adds SANs.
	// CreateValidation toggles the synchronous `aws_acm_certificate_validation`
	// wait — the composer leaves this at the module's default (false) today,
	// but a future #593-followup PR will flip it to true automatically when
	// aws/route53 is in the stack (once the back-edge wiring is unblocked).
	// KeyAlgorithm pins RSA_2048 (default) vs EC_prime256v1 / EC_secp384r1.
	// ValidationTimeout caps the validation wait when CreateValidation=true.
	AWSACM *struct {
		DomainName                     string   `json:"domainName,omitempty"`
		SubjectAlternativeNames        []string `json:"subjectAlternativeNames,omitempty"`
		KeyAlgorithm                   string   `json:"keyAlgorithm,omitempty"`
		CertificateTransparencyLogging string   `json:"certificateTransparencyLogging,omitempty"`
		CreateValidation               *bool    `json:"createValidation,omitempty"`
		ValidationTimeout              string   `json:"validationTimeout,omitempty"`
	} `json:"aws_acm,omitempty"`

	AWSKMS *struct {
		NumKeys string `json:"numKeys,omitempty"`
	} `json:"aws_kms,omitempty"`

	AWSSecretsManager *struct {
		NumSecrets string `json:"numSecrets,omitempty"`
	} `json:"aws_secretsmanager,omitempty"`

	AWSOpenSearch *struct {
		DeploymentType string `json:"deploymentType,omitempty"`
		InstanceType   string `json:"instanceType,omitempty"`
		StorageSize    string `json:"storageSize,omitempty"`
		MultiAZ        *bool  `json:"multiAz,omitempty"`
	} `json:"aws_opensearch,omitempty"`

	AWSBedrock *struct {
		KnowledgeBaseName   string `json:"knowledgeBaseName,omitempty"`
		ModelID             string `json:"modelId,omitempty"`
		EmbeddingModelID    string `json:"embeddingModelId,omitempty"`
		EnableKnowledgeBase *bool  `json:"enableKnowledgeBase,omitempty"`
		VectorStore         string `json:"vectorStore,omitempty"`
	} `json:"aws_bedrock,omitempty"`

	AWSBedrockAgent *struct {
		FoundationModel string `json:"foundationModel,omitempty"`
		Instruction     string `json:"instruction,omitempty"`
		AgentName       string `json:"agentName,omitempty"`
	} `json:"aws_bedrock_agent,omitempty"`

	AWSBackups *struct {
		EC2 *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_ec2,omitempty"`
		RDS *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_rds,omitempty"`
		ElastiCache *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_elasticache,omitempty"`
		DynamoDB *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_dynamodb,omitempty"`
		S3 *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_s3,omitempty"`
	} `json:"aws_backups,omitempty"`

	// ==================== GCP Configuration ====================
	GCPCompute *struct {
		NumServers  string `json:"numServers,omitempty"`
		MachineType string `json:"machineType,omitempty"`
		DiskSizeGb  int    `json:"diskSizeGb,omitempty"`
	} `json:"gcp_compute,omitempty"`

	GCPGKE *struct {
		Regional    *bool  `json:"regional,omitempty"`
		NodeCount   string `json:"nodeCount,omitempty"`
		MachineType string `json:"machineType,omitempty"`
	} `json:"gcp_gke,omitempty"`

	GCPCloudSQL *struct {
		Tier             string `json:"tier,omitempty"`
		DiskSizeGb       int    `json:"diskSizeGb,omitempty"`
		HighAvailability *bool  `json:"highAvailability,omitempty"`
	} `json:"gcp_cloudsql,omitempty"`

	GCPMemorystore *struct {
		Tier         string `json:"tier,omitempty"`
		MemorySizeGb int    `json:"memorySizeGb,omitempty"`
	} `json:"gcp_memorystore,omitempty"`

	GCPGCS *struct {
		StorageClass string `json:"storageClass,omitempty"`
		Versioning   *bool  `json:"versioning,omitempty"`
	} `json:"gcp_gcs,omitempty"`

	// GCPVertexAI carries the caller-supplied Vertex AI configuration (#764,
	// #768).
	//
	// EnableVectorSearch gates the Vector Search resources (index + endpoint +
	// deployed index) in the gcp/vertex_ai preset; the dataset is always
	// created. IndexDimensions is the embedding dimensionality of the Vector
	// Search index (immutable — changing it forces destroy/recreate).
	//
	// EnableServing (#768) gates a serving endpoint; ModelGardenModel, when
	// set alongside EnableServing, deploys that open Model Garden model
	// (publishers/<pub>/models/<model>@<version>) onto a managed endpoint.
	// ModelGardenAcceptEULA records the operator's acceptance of the model's
	// EULA/ToS; EULA-gated open models (Gemma, Llama) will not deploy unless it
	// is true. It defaults to the preset's explicit-consent false when unset.
	// Vector Search and serving are orthogonal flags.
	//
	// Every field is partial-config: the mapper only emits a field the caller
	// actually populated so the preset's own defaults win when left unset.
	GCPVertexAI *struct {
		EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
		IndexDimensions       int    `json:"indexDimensions,omitempty"`
		EnableServing         *bool  `json:"enableServing,omitempty"`
		ModelGardenModel      string `json:"modelGardenModel,omitempty"`
		ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
	} `json:"gcp_vertex_ai,omitempty"`

	GCPPubSub *struct {
		MessageRetentionDuration string `json:"messageRetentionDuration,omitempty"`
	} `json:"gcp_pubsub,omitempty"`

	GCPCloudLogging *struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	} `json:"gcp_cloud_logging,omitempty"`

	GCPCloudRun *struct {
		Memory       string `json:"memory,omitempty"`
		CPU          string `json:"cpu,omitempty"`
		MinInstances *int   `json:"minInstances,omitempty"`
		MaxInstances *int   `json:"maxInstances,omitempty"`
	} `json:"gcp_cloud_run,omitempty"`

	GCPCloudFunctions *struct {
		Runtime    string `json:"runtime,omitempty"`
		MemorySize string `json:"memorySize,omitempty"`
		Timeout    string `json:"timeout,omitempty"`
	} `json:"gcp_cloud_functions,omitempty"`

	GCPIdentityPlatform *struct {
		SignInMethods []string `json:"signInMethods,omitempty"`
		MFARequired   *bool    `json:"mfaRequired,omitempty"`
	} `json:"gcp_identity_platform,omitempty"`

	GCPAPIGateway *struct {
		DomainName string `json:"domainName,omitempty"`
	} `json:"gcp_api_gateway,omitempty"`

	GCPLoadbalancer *struct {
		EnableCDN *bool `json:"enable_cdn,omitempty"`
	} `json:"gcp_loadbalancer,omitempty"`

	// GCPCloudDNS carries the caller-supplied Cloud DNS configuration.
	// DNSName is the apex (e.g. "example.com.") and is required when
	// KeyGCPCloudDNS is selected. CreateZone toggles between creating a
	// managed zone in-stack (true) and looking up an existing one by
	// ZoneName (false). PrivateZone + NetworkSelfLinks are only consulted
	// when CreateZone is true and the zone is intended to be private.
	GCPCloudDNS *struct {
		DNSName          string   `json:"dnsName,omitempty"`
		CreateZone       *bool    `json:"createZone,omitempty"`
		ZoneShortName    string   `json:"zoneShortName,omitempty"`
		ZoneName         string   `json:"zoneName,omitempty"`
		PrivateZone      *bool    `json:"privateZone,omitempty"`
		NetworkSelfLinks []string `json:"networkSelfLinks,omitempty"`
		ForceDestroy     *bool    `json:"forceDestroy,omitempty"`
	} `json:"gcp_cloud_dns,omitempty"`

	// GCPGitHubActions carries the caller-supplied GitHub Actions WIF
	// configuration (#597 row 1). GitHubRepository is the OWNER/REPO that
	// the WIF provider's attribute_condition pins; without it the mapper
	// supplies a placeholder.invalid/placeholder default so single-module
	// previews compose, but the WIF condition built around the placeholder
	// will never match a real workflow — callers MUST override before
	// terraform apply (the placeholder is shaped to fail loudly rather
	// than silently accept any repo). AllowedBranches / AllowedTags /
	// AllowedPullRequest gate which refs / events from that repo can
	// mint credentials; DeployRoles is the project-level role grant list
	// on the deploy SA.
	GCPGitHubActions *GCPGitHubActionsConfig `json:"gcp_github_actions,omitempty"`

	// GCPCloudDeploy carries the caller-supplied Cloud Deploy delivery-
	// pipeline configuration (#613). Every field is optional; the mapper
	// only emits its tfvar when set so the preset's variables.tf defaults
	// (staging->prod Cloud Run pair in var.region) apply when callers
	// don't override. Targets is the ordered promotion chain — element [0]
	// is the first stage. ServiceAccountShortName / PipelineShortName let
	// callers rename the runner SA / pipeline when the var.project prefix
	// alone would collide with downstream consumers.
	GCPCloudDeploy *GCPCloudDeployConfig `json:"gcp_cloud_deploy,omitempty"`

	GCPBackups *struct {
		Compute *struct {
			FrequencyHours int `json:"frequencyHours,omitempty"`
			RetentionDays  int `json:"retentionDays,omitempty"`
		} `json:"gcp_compute,omitempty"`
		CloudSQL *struct {
			Enabled       *bool `json:"enabled,omitempty"`
			RetentionDays int   `json:"retentionDays,omitempty"`
		} `json:"gcp_cloudsql,omitempty"`
		GCS *struct {
			Enabled *bool `json:"enabled,omitempty"`
		} `json:"gcp_gcs,omitempty"`
	} `json:"gcp_backups,omitempty"`
}

// GCPGitHubActionsConfig is the caller-facing config for the gcp/github_actions
// preset. Named (not inline) so callers can construct it without re-typing the
// anonymous struct shape at every site, and so future field additions don't
// force every test instantiation to be touched.
type GCPGitHubActionsConfig struct {
	GitHubRepository   string   `json:"githubRepository,omitempty"`
	AllowedBranches    []string `json:"allowedBranches,omitempty"`
	AllowedTags        []string `json:"allowedTags,omitempty"`
	AllowedPullRequest *bool    `json:"allowedPullRequest,omitempty"`
	DeployRoles        []string `json:"deployRoles,omitempty"`
}

// GCPCloudDeployTarget is a single entry in GCPCloudDeployConfig.Targets.
// Mirrors the gcp/cloud_deploy preset's var.targets list-of-objects shape:
//   - Name: pipeline-scoped target identifier (short form; the preset prefixes
//     it with var.project before sending to Cloud Deploy).
//   - Runtime: "run" (Cloud Run) | "gke" (GKE).
//   - RuntimeTarget: runtime-dispatched destination. For runtime="run", a
//     Cloud Run region (e.g. "us-central1"). For runtime="gke", a fully-
//     qualified cluster ID ("projects/<id>/locations/<loc>/clusters/<name>").
//   - RequireApproval: optional, default false. When true, Cloud Deploy halts
//     promotion to this target and waits for a manual operator step.
//
// Named (not inline) for the same UX reasons as GCPGitHubActionsConfig.
type GCPCloudDeployTarget struct {
	Name            string `json:"name"`
	Runtime         string `json:"runtime"`
	RuntimeTarget   string `json:"runtimeTarget"`
	RequireApproval *bool  `json:"requireApproval,omitempty"`
}

// GCPCloudDeployConfig is the caller-facing config for the gcp/cloud_deploy
// preset (#613). Every field is optional — the mapper only emits its tfvar
// when set, so the preset's variables.tf defaults (staging->prod Cloud Run
// pair in var.region; "delivery" pipeline short name; "clouddeploy-runner"
// SA short name) apply when callers don't override.
type GCPCloudDeployConfig struct {
	ServiceAccountShortName *string                `json:"serviceAccountShortName,omitempty"`
	PipelineShortName       *string                `json:"pipelineShortName,omitempty"`
	Targets                 []GCPCloudDeployTarget `json:"targets,omitempty"`
}

// AWSAppRunnerConfig is the caller-facing config for the aws/apprunner
// preset (#598 row 2). Named (not inline) so callers can construct it
// without re-typing the anonymous struct shape at every site, and so
// future field additions don't force every test instantiation to be
// touched. Mirrors AWSSageMakerConfig.
//
// Field semantics map 1:1 to aws/apprunner/variables.tf. Empty / nil
// values mean "defer to the module's HCL default" — the mapper only
// emits a tfvar when the caller supplies a value. VPCID / SubnetIDs are
// normally wired automatically (DefaultWiring reads module.aws_vpc) so
// callers don't usually populate them on this struct unless they need
// to override the wiring.
type AWSAppRunnerConfig struct {
	ServiceName            string            `json:"serviceName,omitempty"`
	ImageRepositoryURL     string            `json:"imageRepositoryUrl,omitempty"`
	ImageRepositoryType    string            `json:"imageRepositoryType,omitempty"`
	Port                   *int              `json:"port,omitempty"`
	EnvVars                map[string]string `json:"envVars,omitempty"`
	CPU                    string            `json:"cpu,omitempty"`
	Memory                 string            `json:"memory,omitempty"`
	MinSize                *int              `json:"minSize,omitempty"`
	MaxSize                *int              `json:"maxSize,omitempty"`
	MaxConcurrency         *int              `json:"maxConcurrency,omitempty"`
	IsPubliclyAccessible   *bool             `json:"isPubliclyAccessible,omitempty"`
	AutoDeploymentsEnabled *bool             `json:"autoDeploymentsEnabled,omitempty"`
	HealthCheckProtocol    string            `json:"healthCheckProtocol,omitempty"`
	HealthCheckPath        string            `json:"healthCheckPath,omitempty"`
	EnableVPCConnector     *bool             `json:"enableVpcConnector,omitempty"`
	VPCID                  string            `json:"vpcId,omitempty"`
	SubnetIDs              []string          `json:"subnetIds,omitempty"`
	CustomDomainName       string            `json:"customDomainName,omitempty"`
	EnableWWWSubdomain     *bool             `json:"enableWwwSubdomain,omitempty"`
}

// AWSSageMakerConfig is the caller-facing config for the aws/sagemaker
// Studio preset (#615). Named (not inline) so callers can construct it
// without re-typing the anonymous struct shape at every site, and so
// future field additions don't force every test instantiation to be
// touched. Mirrors GCPGitHubActionsConfig pattern.
//
// Field semantics map 1:1 to aws/sagemaker/variables.tf. Empty / nil
// values mean "defer to the module's HCL default" — the mapper only
// emits a tfvar when the caller supplies a value. VPCID / SubnetIDs are
// normally wired automatically (DefaultWiring reads module.aws_vpc) so
// callers don't usually populate them on this struct.
type AWSSageMakerConfig struct {
	VPCID                       string   `json:"vpcId,omitempty"`
	SubnetIDs                   []string `json:"subnetIds,omitempty"`
	NetworkMode                 string   `json:"networkMode,omitempty"`
	WorkspaceBucket             string   `json:"workspaceBucket,omitempty"`
	WorkspaceBucketForceDestroy *bool    `json:"workspaceBucketForceDestroy,omitempty"`
	StudioUsers                 []string `json:"studioUsers,omitempty"`
	SageMakerManagedPolicyARN   string   `json:"sagemakerManagedPolicyArn,omitempty"`

	// Real-time inference endpoint (#761). When EnableInference is set, the
	// preset adds an aws_sagemaker_model + endpoint-configuration + endpoint
	// trio hosting ModelImage. The Studio domain stays unconditional.
	// ModelImage is required (validated non-empty in the preset) when
	// inference is on; ModelDataURL is optional (images may bundle weights).
	// Unset / nil fields defer to the preset's variables.tf defaults, same
	// partial-config contract as every other field above.
	EnableInference      *bool  `json:"enableInference,omitempty"`
	ModelImage           string `json:"modelImage,omitempty"`
	ModelDataURL         string `json:"modelDataUrl,omitempty"`
	EndpointInstanceType string `json:"endpointInstanceType,omitempty"`

	// ModelEnvironment carries container env vars injected into the model's
	// primary container (threads to aws/sagemaker var.model_environment →
	// primary_container.environment). Required for serving images that read
	// their config from env — e.g. an AWS HuggingFace DLC needs HF_MODEL_ID +
	// HF_TASK. Nil / empty defers to the preset default ({}), same
	// partial-config contract as the fields above.
	ModelEnvironment map[string]string `json:"modelEnvironment,omitempty"`
}

// AWSCodeBuildConfig is the caller-facing config for the aws/codebuild
// preset (#619). Named (not inline) so callers can construct it
// without re-typing the anonymous struct shape at every site, and so
// future field additions don't force every test instantiation to be
// touched. Mirrors AWSAppRunnerConfig + AWSSageMakerConfig.
//
// Field semantics map 1:1 to aws/codebuild/variables.tf. Empty / nil
// values mean "defer to the module's HCL default" — the mapper only
// emits a tfvar when the caller supplies a value. VPCID / SubnetIDs are
// normally wired automatically (DefaultWiring reads module.aws_vpc) so
// callers don't usually populate them on this struct unless they need
// to override the wiring. SecurityGroupIDs is always caller-supplied —
// the preset doesn't create an SG.
type AWSCodeBuildConfig struct {
	ProjectName       string   `json:"projectName,omitempty"`
	BuildImage        string   `json:"buildImage,omitempty"`
	ComputeType       string   `json:"computeType,omitempty"`
	SourceType        string   `json:"sourceType,omitempty"`
	SourceLocation    string   `json:"sourceLocation,omitempty"`
	Buildspec         string   `json:"buildspec,omitempty"`
	ArtifactsType     string   `json:"artifactsType,omitempty"`
	ArtifactsLocation string   `json:"artifactsLocation,omitempty"`
	EnableS3Logs      *bool    `json:"enableS3Logs,omitempty"`
	VPCID             string   `json:"vpcId,omitempty"`
	SubnetIDs         []string `json:"subnetIds,omitempty"`
	SecurityGroupIDs  []string `json:"securityGroupIds,omitempty"`
}

// VarEntry holds a module variable name and a value (or nil). RawExpr can be used for expressions.
type VarEntry struct {
	Name  string
	Value any
}
type RawExpr struct{ Expr string }

// simple helpers
func boolVal(p *bool) bool { return p != nil && *p }

// Normalize canonicalises a Components by clearing fields that belong to the
// opposite cloud. Phase 4 (v0.4.0) dropped the legacy-field sync logic;
// callers with legacy-shaped session JSON must upgrade via the InsideOut backend's
// composeradapter (or equivalent) before handing the Components to composer.
func (c *Components) Normalize() {
	if c == nil {
		return
	}
	if c.Cloud == "" {
		return
	}
	if c.Cloud == "AWS" {
		c.GCPVPC = nil
		c.GCPBastion = nil
		c.GCPCompute = ""
		c.GCPGKE = nil
		c.GCPCloudRun = nil
		c.GCPCloudFunctions = nil
		c.GCPLoadbalancer = nil
		c.GCPCloudArmor = nil
		c.GCPAPIGateway = nil
		c.GCPCloudSQL = nil
		c.GCPMemorystore = nil
		c.GCPFirestore = nil
		c.GCPGCS = nil
		c.GCPCloudKMS = nil
		c.GCPSecretManager = nil
		c.GCPVertexAI = nil
		c.GCPPubSub = nil
		c.GCPCloudLogging = nil
		c.GCPCloudMonitoring = nil
		c.GCPIdentityPlatform = nil
		c.GCPCloudBuild = nil
		c.GCPCloudDeploy = nil
		c.GCPCloudDNS = nil
		c.GCPGitHubActions = nil
		c.GCPBackups = nil
	}
	if c.Cloud == "GCP" {
		c.AWSVPC = ""
		c.AWSBastion = nil
		c.AWSEC2 = ""
		c.AWSEKS = nil
		c.AWSECS = nil
		c.AWSLambda = nil
		c.AWSAppRunner = nil
		c.AWSSageMaker = nil
		c.AWSALB = nil
		c.AWSCloudFront = nil
		c.AWSWAF = nil
		c.AWSAPIGateway = nil
		c.AWSRDS = nil
		c.AWSElastiCache = nil
		c.AWSDynamoDB = nil
		c.AWSOpenSearch = nil
		c.AWSS3 = nil
		c.AWSKMS = nil
		c.AWSSecretsManager = nil
		c.AWSBedrock = nil
		c.AWSBedrockAgent = nil
		c.AWSSQS = nil
		c.AWSMSK = nil
		c.AWSCloudWatchLogs = nil
		c.AWSCloudWatchMonitoring = nil
		c.AWSGrafana = nil
		c.AWSCognito = nil
		c.AWSGitHubActions = nil
		c.AWSCodeBuild = nil
		c.AWSCodePipeline = nil
		c.AWSRoute53 = nil
		c.AWSACM = nil
		c.AWSBackups = nil
	}
}

// IsLambdaArchitecture returns true if the stack uses Lambda as its compute
// layer. Reads only c.AWSLambda. In v0.4.0 the legacy c.Lambda / c.Resource
// fields no longer exist; callers with pre-Phase-4 session JSON must fold
// those via the InsideOut backend's composeradapter before reaching composer.
func (c *Components) IsLambdaArchitecture() bool {
	if c == nil {
		return false
	}
	return boolVal(c.AWSLambda)
}

// AWSBackupsSelected returns true if any AWS backup component is selected.
func (c *Components) AWSBackupsSelected() bool {
	if c == nil || c.AWSBackups == nil {
		return false
	}
	b := c.AWSBackups
	return boolVal(b.EC2) || boolVal(b.RDS) || boolVal(b.ElastiCache) || boolVal(b.DynamoDB) || boolVal(b.S3)
}

// GCPBackupsSelected returns true if any GCP backup component is selected.
func (c *Components) GCPBackupsSelected() bool {
	if c == nil || c.GCPBackups == nil {
		return false
	}
	b := c.GCPBackups
	return boolVal(b.Compute) || boolVal(b.CloudSQL) || boolVal(b.GCS)
}

// BackupsSelected returns true if any backup component is selected (AWS or GCP).
// Callers with legacy session shapes must Normalize first; see composeradapter.
func (c *Components) BackupsSelected() bool {
	return c.AWSBackupsSelected() || c.GCPBackupsSelected()
}

// Normalize canonicalises a Config by inferring cloud (when unset) from which
// prefixed sub-configs are populated, then clearing the opposite cloud's
// fields. It also migrates the within-AWSCloudfront deprecated sub-field
// CachePaths to OriginPath (separate from the legacy-field removal in Phase
// 4). Legacy session JSON should be upgraded by the InsideOut backend's composeradapter
// before reaching composer.
func (c *Config) Normalize() {
	if c == nil {
		return
	}
	if c.Cloud == "" {
		if c.GCPCompute != nil || c.GCPGKE != nil || c.GCPCloudSQL != nil {
			c.Cloud = "GCP"
		} else if c.AWSEC2 != nil || c.AWSEKS != nil || c.AWSRDS != nil || c.AWSCloudfront != nil || c.AWSS3 != nil {
			c.Cloud = "AWS"
		}
	}
	switch c.Cloud {
	case "AWS":
		// Clear every GCP sub-config (15 fields — keep in sync with the
		// GCPConfiguration section of the Config struct).
		c.GCPCompute = nil
		c.GCPGKE = nil
		c.GCPCloudSQL = nil
		c.GCPMemorystore = nil
		c.GCPGCS = nil
		c.GCPVertexAI = nil
		c.GCPPubSub = nil
		c.GCPCloudLogging = nil
		c.GCPCloudRun = nil
		c.GCPCloudFunctions = nil
		c.GCPIdentityPlatform = nil
		c.GCPAPIGateway = nil
		c.GCPLoadbalancer = nil
		c.GCPCloudDNS = nil
		c.GCPGitHubActions = nil
		c.GCPCloudDeploy = nil
		c.GCPBackups = nil
		// AWSCloudfront.CachePaths is a within-prefixed deprecated sub-field;
		// migrate to OriginPath and clear. Distinct from the legacy Cloudfront
		// struct deletion in Phase 4.
		if c.AWSCloudfront != nil && c.AWSCloudfront.CachePaths != nil {
			if c.AWSCloudfront.OriginPath == nil {
				c.AWSCloudfront.OriginPath = c.AWSCloudfront.CachePaths
			}
			c.AWSCloudfront.CachePaths = nil
		}
	case "GCP":
		// Clear every AWS sub-config (21 fields — keep in sync with the
		// AWSConfiguration section of the Config struct).
		c.AWSEC2 = nil
		c.AWSEKS = nil
		c.AWSECS = nil
		c.AWSVPC = nil
		c.AWSCloudfront = nil
		c.AWSRDS = nil
		c.AWSElastiCache = nil
		c.AWSS3 = nil
		c.AWSDynamoDB = nil
		c.AWSSQS = nil
		c.AWSMSK = nil
		c.AWSCloudWatchLogs = nil
		c.AWSCloudWatchMonitoring = nil
		c.AWSCognito = nil
		c.AWSLambda = nil
		c.AWSAppRunner = nil
		c.AWSSageMaker = nil
		c.AWSCodeBuild = nil
		c.AWSAPIGateway = nil
		c.AWSKMS = nil
		c.AWSSecretsManager = nil
		c.AWSOpenSearch = nil
		c.AWSBedrock = nil
		c.AWSBedrockAgent = nil
		c.AWSRoute53 = nil
		c.AWSACM = nil
		c.AWSBackups = nil
	}
}
