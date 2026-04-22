package composer

import (
	"fmt"
	"strings"
)

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
	AWSSQS                  *bool  `json:"aws_sqs,omitempty"`
	AWSMSK                  *bool  `json:"aws_msk,omitempty"`
	AWSCloudWatchLogs       *bool  `json:"aws_cloudwatch_logs,omitempty"`
	AWSCloudWatchMonitoring *bool  `json:"aws_cloudwatch_monitoring,omitempty"`
	AWSGrafana              *bool  `json:"aws_grafana,omitempty"`
	AWSCognito              *bool  `json:"aws_cognito,omitempty"`
	AWSGitHubActions        *bool  `json:"aws_github_actions,omitempty"`
	AWSCodePipeline         *bool  `json:"aws_codepipeline,omitempty"`
	AWSBackups              *struct {
		EC2         *bool `json:"aws_ec2,omitempty"`
		RDS         *bool `json:"aws_rds,omitempty"`
		ElastiCache *bool `json:"aws_elasticache,omitempty"`
		DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
		S3          *bool `json:"aws_s3,omitempty"`
	} `json:"aws_backups,omitempty"`

	// ==================== GCP Components ====================
	GCPVPC              *bool `json:"gcp_vpc,omitempty"`
	GCPBastion          *bool `json:"gcp_bastion,omitempty"`
	GCPCompute          string `json:"gcp_compute,omitempty"` // "Intel" or "ARM" or empty for boolean
	GCPGKE              *bool `json:"gcp_gke,omitempty"`
	GCPCloudRun         *bool `json:"gcp_cloud_run,omitempty"`
	GCPCloudFunctions   *bool `json:"gcp_cloud_functions,omitempty"`
	GCPLoadbalancer     *bool `json:"gcp_loadbalancer,omitempty"`
	GCPCloudCDN         *bool `json:"gcp_cloud_cdn,omitempty"`
	GCPCloudArmor       *bool `json:"gcp_cloud_armor,omitempty"`
	GCPAPIGateway       *bool `json:"gcp_api_gateway,omitempty"`
	GCPCloudSQL         *bool `json:"gcp_cloudsql,omitempty"`
	GCPMemorystore      *bool `json:"gcp_memorystore,omitempty"`
	GCPFirestore        *bool `json:"gcp_firestore,omitempty"`
	GCPGCS              *bool `json:"gcp_gcs,omitempty"`
	GCPCloudKMS         *bool `json:"gcp_cloud_kms,omitempty"`
	GCPSecretManager    *bool `json:"gcp_secret_manager,omitempty"`
	GCPVertexAI         *bool `json:"gcp_vertex_ai,omitempty"`
	GCPPubSub           *bool `json:"gcp_pubsub,omitempty"`
	GCPCloudLogging     *bool `json:"gcp_cloud_logging,omitempty"`
	GCPCloudMonitoring  *bool `json:"gcp_cloud_monitoring,omitempty"`
	GCPIdentityPlatform *bool `json:"gcp_identity_platform,omitempty"`
	GCPCloudBuild       *bool `json:"gcp_cloud_build,omitempty"`
	GCPBackups          *struct {
		Compute  *bool `json:"gcp_compute,omitempty"`
		CloudSQL *bool `json:"gcp_cloudsql,omitempty"`
		GCS      *bool `json:"gcp_gcs,omitempty"`
	} `json:"gcp_backups,omitempty"`

	// ==================== External/Third-Party ====================
	Splunk        *bool `json:"splunk,omitempty"`
	Datadog       *bool `json:"datadog,omitempty"`
	GitHubActions *bool `json:"githubactions,omitempty"` // External GitHub Actions (not aws_github_actions OIDC)

	// ==================== Legacy Fields (backward compatibility) ====================
	// These are kept for backward compatibility when parsing old JSON.
	// Deprecated as a group: use the AWS*-prefixed fields above. See doc.go
	// and insideout-terraform-presets#76 for the removal plan; historical
	// session JSON should be normalised by reliable's composeradapter before
	// reaching composer.
	//
	// Deprecated: Use AWSEC2 (node-group flavour) or the polymorphic AWS
	// compute keys. Retained for legacy session JSON parsing only.
	EC2 string `json:"ec2,omitempty"`
	// Deprecated: Polymorphic EKS/Lambda indicator kept for legacy session
	// parsing. Use the prefixed keys on Components directly.
	Resource string `json:"resource,omitempty"`
	// Deprecated: Use AWSVPC.
	VPC string `json:"vpc,omitempty"`
	// Deprecated: Use AWSBastion.
	Bastion *bool `json:"bastion,omitempty"`
	// Deprecated: Use AWSALB.
	ALB *bool `json:"alb,omitempty"`
	// Deprecated: Use AWSCloudFront.
	CloudFront *bool `json:"cloudfront,omitempty"`
	// Deprecated: Use AWSWAF.
	WAF *bool `json:"waf,omitempty"`
	// Deprecated: Use AWSRDS.
	Postgres *bool `json:"postgres,omitempty"`
	// Deprecated: Use AWSElastiCache.
	ElastiCache *bool `json:"elasticache,omitempty"`
	// Deprecated: Use AWSS3.
	S3 *bool `json:"s3,omitempty"`
	// Deprecated: Use AWSDynamoDB.
	DynamoDB *bool `json:"dynamodb,omitempty"`
	// Deprecated: Use AWSSQS.
	SQS *bool `json:"sqs,omitempty"`
	// Deprecated: Use AWSMSK.
	MSK *bool `json:"msk,omitempty"`
	// Deprecated: Use AWSCloudWatchLogs.
	CloudWatchLogs *bool `json:"cloudwatchlogs,omitempty"`
	// Deprecated: Use AWSCloudWatchMonitoring.
	CloudWatchMonitoring *bool `json:"cloudwatchmonitoring,omitempty"`
	// Deprecated: Use AWSGrafana.
	Grafana *bool `json:"grafana,omitempty"`
	// Deprecated: Use AWSCognito.
	Cognito *bool `json:"cognito,omitempty"`
	// Deprecated: Use AWSAPIGateway.
	APIGateway *bool `json:"apigateway,omitempty"`
	// Deprecated: Use AWSKMS.
	KMS *bool `json:"kms,omitempty"`
	// Deprecated: Use AWSSecretsManager.
	SecretsManager *bool `json:"secretsmanager,omitempty"`
	// Deprecated: Use AWSOpenSearch.
	OpenSearch *bool `json:"opensearch,omitempty"`
	// Deprecated: Use AWSBedrock.
	Bedrock *bool `json:"bedrock,omitempty"`
	// Deprecated: Use AWSLambda.
	Lambda *bool `json:"lambda,omitempty"`
	// Deprecated: Use AWSCodePipeline.
	CodePipeline *bool `json:"codepipeline,omitempty"`
	// Deprecated: Use AWSBackups.
	Backups *struct {
		EC2         *bool `json:"ec2,omitempty"`
		Rds         *bool `json:"rds,omitempty"`
		ElastiCache *bool `json:"elasticache,omitempty"`
		DynamoDB    *bool `json:"dynamodb,omitempty"`
		S3          *bool `json:"s3,omitempty"`
	} `json:"backups,omitempty"`
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
		InstanceType       string `json:"instanceType,omitempty"`
		NumServers         string `json:"numServers,omitempty"`
		NumCoresPerServer  string `json:"numCoresPerServer,omitempty"`
		DiskSizePerServer  string `json:"diskSizePerServer,omitempty"`
		UserData           string `json:"userData,omitempty"`
		UserDataURL        string `json:"userDataURL,omitempty"`
		CustomIngressPorts []int  `json:"customIngressPorts,omitempty"`
		SSHPublicKey          string `json:"sshPublicKey,omitempty"`
		EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
	} `json:"aws_ec2,omitempty"`

	AWSEKS *struct {
		HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
		ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
		DesiredSize            string `json:"desiredSize,omitempty"`
		MaxSize                string `json:"maxSize,omitempty"`
		MinSize                string `json:"minSize,omitempty"`
		InstanceType           string `json:"instanceType,omitempty"`
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

	AWSAPIGateway *struct {
		DomainName     string `json:"domainName,omitempty"`
		CertificateArn string `json:"certificateArn,omitempty"`
	} `json:"aws_api_gateway,omitempty"`

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
		KnowledgeBaseName string `json:"knowledgeBaseName,omitempty"`
		ModelID           string `json:"modelId,omitempty"`
		EmbeddingModelID  string `json:"embeddingModelId,omitempty"`
	} `json:"aws_bedrock,omitempty"`

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

	GCPCloudCDN *struct {
		DefaultTtl string `json:"defaultTtl,omitempty"`
		OriginPath string `json:"originPath,omitempty"`
		CachePaths string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
	} `json:"gcp_cloud_cdn,omitempty"`

	GCPLoadbalancer *struct {
		EnableCDN *bool `json:"enable_cdn,omitempty"`
	} `json:"gcp_loadbalancer,omitempty"`

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

	// ==================== Legacy Fields (backward compatibility) ====================
	// Deprecated as a group: use the AWS*-prefixed fields above. See doc.go
	// and insideout-terraform-presets#76 for the removal plan; historical
	// session JSON should be normalised by reliable's composeradapter before
	// reaching composer.
	//
	// Deprecated: Use AWSEC2.
	EC2 *struct {
		NumServers        string `json:"numServers,omitempty"`
		NumCoresPerServer string `json:"numCoresPerServer,omitempty"`
		DiskSizePerServer string `json:"diskSizePerServer,omitempty"`
	} `json:"ec2,omitempty"`

	// Deprecated: Use AWSEKS.
	Eks *struct {
		HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
		ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
		DesiredSize            string `json:"desiredSize,omitempty"`
		MaxSize                string `json:"maxSize,omitempty"`
		MinSize                string `json:"minSize,omitempty"`
		InstanceType           string `json:"instanceType,omitempty"`
	} `json:"eks,omitempty"`

	// Deprecated: Use AWSCloudfront.
	Cloudfront *struct {
		DefaultTtl *string `json:"defaultTtl,omitempty"`
		OriginPath *string `json:"originPath,omitempty"`
		CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
	} `json:"cloudfront,omitempty"`

	// Deprecated: Use AWSRDS.
	RDS *struct {
		CPUSize      string `json:"cpuSize,omitempty"`
		ReadReplicas string `json:"readReplicas,omitempty"`
		StorageSize  string `json:"storageSize,omitempty"`
	} `json:"rds,omitempty"`

	// Deprecated: Use AWSElastiCache.
	ElastiCache *struct {
		HA       *bool  `json:"ha,omitempty"`
		Storage  string `json:"storageSize,omitempty"`
		NodeSize string `json:"nodeSize,omitempty"`
		Replicas string `json:"replicas,omitempty"`
	} `json:"elasticache,omitempty"`

	// Deprecated: Use AWSS3.
	S3 *struct {
		Versioning *bool `json:"versioning,omitempty"`
	} `json:"s3,omitempty"`
	// Deprecated: Use AWSDynamoDB.
	DynamoDB *struct {
		Type string `json:"type,omitempty"`
	} `json:"dynamodb,omitempty"`

	// Deprecated: Use AWSSQS.
	SQS *struct {
		Type              string `json:"type,omitempty"`
		VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
	} `json:"sqs,omitempty"`

	// Deprecated: Use AWSMSK.
	MSK *struct {
		Retention string `json:"retentionPeriod,omitempty"`
	} `json:"msk,omitempty"`
	// Deprecated: Use AWSCloudWatchLogs.
	CloudWatchLogs *struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	} `json:"cloudwatchlogs,omitempty"`
	// Deprecated: Use AWSCloudWatchMonitoring.
	CloudWatchMonitoring *struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	} `json:"cloudwatchmonitoring,omitempty"`

	// Deprecated: Use AWSCognito.
	Cognito *struct {
		SignInType  string `json:"signInType,omitempty"`
		MFARequired *bool  `json:"mfaRequired,omitempty"`
	} `json:"cognito,omitempty"`

	// Deprecated: Use AWSLambda.
	Lambda *struct {
		Runtime    string `json:"runtime,omitempty"`
		MemorySize string `json:"memorySize,omitempty"`
		Timeout    string `json:"timeout,omitempty"`
	} `json:"lambda,omitempty"`

	// Deprecated: Use AWSAPIGateway.
	APIGateway *struct {
		DomainName     string `json:"domainName,omitempty"`
		CertificateArn string `json:"certificateArn,omitempty"`
	} `json:"apigateway,omitempty"`

	// Deprecated: Use AWSKMS.
	KMS *struct {
		NumKeys string `json:"numKeys,omitempty"`
	} `json:"kms,omitempty"`

	// Deprecated: Use AWSSecretsManager.
	SecretsManager *struct {
		NumSecrets string `json:"numSecrets,omitempty"`
	} `json:"secretsmanager,omitempty"`
	// Deprecated: Use AWSOpenSearch.
	OpenSearch *struct {
		DeploymentType string `json:"deploymentType,omitempty"`
		InstanceType   string `json:"instanceType,omitempty"`
		StorageSize    string `json:"storageSize,omitempty"`
		MultiAZ        *bool  `json:"multiAz,omitempty"`
	} `json:"opensearch,omitempty"`

	// Deprecated: Use AWSBedrock.
	Bedrock *struct {
		KnowledgeBaseName string `json:"knowledgeBaseName,omitempty"`
		ModelID           string `json:"modelId,omitempty"`
		EmbeddingModelID  string `json:"embeddingModelId,omitempty"`
	} `json:"bedrock,omitempty"`

	// Deprecated: Use AWSBackups.
	Backups *struct {
		Details map[string]struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"details,omitempty"`
	} `json:"backups,omitempty"`
}

// VarEntry holds a module variable name and a value (or nil). RawExpr can be used for expressions.
type VarEntry struct {
	Name  string
	Value any
}
type RawExpr struct{ Expr string }

// simple helpers
func boolVal(p *bool) bool { return p != nil && *p }

// Normalize returns a Components with legacy fields migrated to cloud-specific fields and vice-versa.
// This should be called after unmarshaling to ensure consistent field access.
func (c *Components) Normalize() {
	if c == nil {
		return
	}

	// If cloud is not set, leave it empty for new sessions
	// The UI will show "Not selected" for empty cloud
	// Only proceed with normalization if cloud is explicitly set
	if c.Cloud == "" {
		return
	}

	// Sync legacy and cloud-specific fields for AWS
	if c.Cloud == "AWS" {
		// Clear all GCP fields to prevent mixed-cloud display
		c.GCPVPC = nil
		c.GCPBastion = nil
		c.GCPCompute = ""
		c.GCPGKE = nil
		c.GCPCloudRun = nil
		c.GCPCloudFunctions = nil
		c.GCPLoadbalancer = nil
		c.GCPCloudCDN = nil
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
		c.GCPBackups = nil

		// Sync VPC
		if c.VPC != "" && c.AWSVPC == "" {
			c.AWSVPC = c.VPC
		} else if c.AWSVPC != "" {
			c.VPC = c.AWSVPC
		}

		// Sync EC2
		if c.EC2 != "" && c.AWSEC2 == "" {
			c.AWSEC2 = c.EC2
		} else if c.AWSEC2 != "" {
			c.EC2 = c.AWSEC2
		}

		// Sync Bastion
		if c.Bastion != nil && c.AWSBastion == nil {
			c.AWSBastion = c.Bastion
		} else if c.AWSBastion != nil {
			c.Bastion = c.AWSBastion
		}

		// Sync ALB
		if c.ALB != nil && c.AWSALB == nil {
			c.AWSALB = c.ALB
		} else if c.AWSALB != nil {
			c.ALB = c.AWSALB
		}

		// Sync CloudFront
		if c.CloudFront != nil && c.AWSCloudFront == nil {
			c.AWSCloudFront = c.CloudFront
		} else if c.AWSCloudFront != nil {
			c.CloudFront = c.AWSCloudFront
		}

		// Sync WAF
		if c.WAF != nil && c.AWSWAF == nil {
			c.AWSWAF = c.WAF
		} else if c.AWSWAF != nil {
			c.WAF = c.AWSWAF
		}

		// Sync Postgres
		if c.Postgres != nil && c.AWSRDS == nil {
			c.AWSRDS = c.Postgres
		} else if c.AWSRDS != nil {
			c.Postgres = c.AWSRDS
		}

		// Sync ElastiCache
		if c.ElastiCache != nil && c.AWSElastiCache == nil {
			c.AWSElastiCache = c.ElastiCache
		} else if c.AWSElastiCache != nil {
			c.ElastiCache = c.AWSElastiCache
		}

		// Sync S3
		if c.S3 != nil && c.AWSS3 == nil {
			c.AWSS3 = c.S3
		} else if c.AWSS3 != nil {
			sc := *c.AWSS3
			c.S3 = &sc
		}

		// Sync DynamoDB
		if c.DynamoDB != nil && c.AWSDynamoDB == nil {
			c.AWSDynamoDB = c.DynamoDB
		} else if c.AWSDynamoDB != nil {
			c.DynamoDB = c.AWSDynamoDB
		}

		// Sync SQS
		if c.SQS != nil && c.AWSSQS == nil {
			c.AWSSQS = c.SQS
		} else if c.AWSSQS != nil {
			sc := *c.AWSSQS
			c.SQS = &sc
		}

		// Sync MSK
		if c.MSK != nil && c.AWSMSK == nil {
			c.AWSMSK = c.MSK
		} else if c.AWSMSK != nil {
			sc := *c.AWSMSK
			c.MSK = &sc
		}

		// Sync CloudWatchLogs
		if c.CloudWatchLogs != nil && c.AWSCloudWatchLogs == nil {
			c.AWSCloudWatchLogs = c.CloudWatchLogs
		} else if c.AWSCloudWatchLogs != nil {
			sc := *c.AWSCloudWatchLogs
			c.CloudWatchLogs = &sc
		}

		// Sync CloudWatchMonitoring
		if c.CloudWatchMonitoring != nil && c.AWSCloudWatchMonitoring == nil {
			c.AWSCloudWatchMonitoring = c.CloudWatchMonitoring
		} else if c.AWSCloudWatchMonitoring != nil {
			sc := *c.AWSCloudWatchMonitoring
			c.CloudWatchMonitoring = &sc
		}

		// Sync Grafana
		if c.Grafana != nil && c.AWSGrafana == nil {
			c.AWSGrafana = c.Grafana
		} else if c.AWSGrafana != nil {
			c.Grafana = c.AWSGrafana
		}

		// Sync Cognito
		if c.Cognito != nil && c.AWSCognito == nil {
			c.AWSCognito = c.Cognito
		} else if c.AWSCognito != nil {
			c.Cognito = c.AWSCognito
		}

		// Sync APIGateway
		if c.APIGateway != nil && c.AWSAPIGateway == nil {
			c.AWSAPIGateway = c.APIGateway
		} else if c.AWSAPIGateway != nil {
			c.APIGateway = c.AWSAPIGateway
		}

		// Sync KMS
		if c.KMS != nil && c.AWSKMS == nil {
			c.AWSKMS = c.KMS
		} else if c.AWSKMS != nil {
			c.KMS = c.AWSKMS
		}

		// Sync SecretsManager
		if c.SecretsManager != nil && c.AWSSecretsManager == nil {
			c.AWSSecretsManager = c.SecretsManager
		} else if c.AWSSecretsManager != nil {
			c.SecretsManager = c.AWSSecretsManager
		}

		// Sync OpenSearch
		if c.OpenSearch != nil && c.AWSOpenSearch == nil {
			c.AWSOpenSearch = c.OpenSearch
		} else if c.AWSOpenSearch != nil {
			c.OpenSearch = c.AWSOpenSearch
		}

		// Sync Bedrock
		if c.Bedrock != nil && c.AWSBedrock == nil {
			c.AWSBedrock = c.Bedrock
		} else if c.AWSBedrock != nil {
			c.Bedrock = c.AWSBedrock
		}

		// Sync Lambda. Legacy sessions encoded Lambda/serverless in THREE
		// different shapes:
		//   1. c.Lambda *bool      — earliest form, used in reliable sessions
		//   2. c.AWSLambda *bool   — v2 form
		//   3. c.Resource string   — earliest form, the architecture enum
		//                            ("Lambda" / "Serverless" / "Kubernetes")
		// Promote all three to c.AWSLambda so downstream composer reads a
		// single source of truth. Resource stays untouched for its GCP role
		// (GKE / CloudRun detection) and the final legacy-clearing block at
		// the end of Normalize clears it.
		if c.Lambda != nil && c.AWSLambda == nil {
			c.AWSLambda = c.Lambda
		} else if c.AWSLambda != nil {
			c.Lambda = c.AWSLambda
		}
		if c.AWSLambda == nil && c.Resource != "" {
			lower := strings.ToLower(c.Resource)
			if strings.Contains(lower, "lambda") || strings.Contains(lower, "serverless") {
				t := true
				c.AWSLambda = &t
				c.Lambda = &t // keep legacy mirror in sync
			}
		}

		// Sync CodePipeline
		if c.CodePipeline != nil && c.AWSCodePipeline == nil {
			c.AWSCodePipeline = c.CodePipeline
		} else if c.AWSCodePipeline != nil {
			c.CodePipeline = c.AWSCodePipeline
		}

		// Sync GitHubActions
		if c.GitHubActions != nil && c.AWSGitHubActions == nil {
			c.AWSGitHubActions = c.GitHubActions
		} else if c.AWSGitHubActions != nil {
			c.GitHubActions = c.AWSGitHubActions
		}

		// Sync Backups
		if c.Backups != nil && c.AWSBackups == nil {
			c.AWSBackups = &struct {
				EC2         *bool `json:"aws_ec2,omitempty"`
				RDS         *bool `json:"aws_rds,omitempty"`
				ElastiCache *bool `json:"aws_elasticache,omitempty"`
				DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
				S3          *bool `json:"aws_s3,omitempty"`
			}{
				EC2:         c.Backups.EC2,
				RDS:         c.Backups.Rds,
				ElastiCache: c.Backups.ElastiCache,
				DynamoDB:    c.Backups.DynamoDB,
				S3:          c.Backups.S3,
			}
		} else if c.AWSBackups != nil {
			c.Backups = &struct {
				EC2         *bool `json:"ec2,omitempty"`
				Rds         *bool `json:"rds,omitempty"`
				ElastiCache *bool `json:"elasticache,omitempty"`
				DynamoDB    *bool `json:"dynamodb,omitempty"`
				S3          *bool `json:"s3,omitempty"`
			}{
				EC2:         c.AWSBackups.EC2,
				Rds:         c.AWSBackups.RDS,
				ElastiCache: c.AWSBackups.ElastiCache,
				DynamoDB:    c.AWSBackups.DynamoDB,
				S3:          c.AWSBackups.S3,
			}
		}
	}

	// Sync legacy and cloud-specific fields for GCP
	if c.Cloud == "GCP" {
		// Clear all AWS fields to prevent mixed-cloud display
		c.AWSVPC = ""
		c.AWSBastion = nil
		c.AWSEC2 = ""
		c.AWSEKS = nil
		c.AWSECS = nil
		c.AWSLambda = nil
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
		c.AWSSQS = nil
		c.AWSMSK = nil
		c.AWSCloudWatchLogs = nil
		c.AWSCloudWatchMonitoring = nil
		c.AWSGrafana = nil
		c.AWSCognito = nil
		c.AWSGitHubActions = nil
		c.AWSCodePipeline = nil
		c.AWSBackups = nil

		// Sync VPC
		if c.VPC != "" && c.GCPVPC == nil {
			val := true
			c.GCPVPC = &val
		} else if c.GCPVPC != nil && *c.GCPVPC {
			c.VPC = "VPC"
		}

		// Sync Compute -> EC2
		if c.EC2 != "" && c.GCPCompute == "" {
			c.GCPCompute = c.EC2
		} else if c.GCPCompute != "" {
			c.EC2 = c.GCPCompute
		}

		// Sync Bastion
		if c.Bastion != nil && c.GCPBastion == nil {
			c.GCPBastion = c.Bastion
		} else if c.GCPBastion != nil {
			c.Bastion = c.GCPBastion
		}

		// Sync GKE/CloudRun -> Resource
		if (c.GCPGKE != nil && *c.GCPGKE || c.GCPCloudRun != nil && *c.GCPCloudRun) && c.Resource == "" {
			if c.GCPGKE != nil && *c.GCPGKE {
				c.Resource = "GKE"
			} else {
				c.Resource = "CloudRun"
			}
		} else if c.Resource != "" {
			val := true
			switch c.Resource {
			case "GKE":
				c.GCPGKE = &val
			case "CloudRun":
				c.GCPCloudRun = &val
			}
		}

		// Sync CloudSQL -> Postgres
		if c.Postgres != nil && c.GCPCloudSQL == nil {
			c.GCPCloudSQL = c.Postgres
		} else if c.GCPCloudSQL != nil {
			c.Postgres = c.GCPCloudSQL
		}

		// Sync Memorystore -> ElastiCache
		if c.ElastiCache != nil && c.GCPMemorystore == nil {
			c.GCPMemorystore = c.ElastiCache
		} else if c.GCPMemorystore != nil {
			c.ElastiCache = c.GCPMemorystore
		}

		// Sync GCS -> S3
		if c.S3 != nil && c.GCPGCS == nil {
			c.GCPGCS = c.S3
		} else if c.GCPGCS != nil {
			sc := *c.GCPGCS
			c.S3 = &sc
		}

		// Sync Loadbalancer -> ALB
		if c.ALB != nil && c.GCPLoadbalancer == nil {
			c.GCPLoadbalancer = c.ALB
		} else if c.GCPLoadbalancer != nil {
			c.ALB = c.GCPLoadbalancer
		}

		// Sync CloudCDN -> CloudFront
		if c.CloudFront != nil && c.GCPCloudCDN == nil {
			c.GCPCloudCDN = c.CloudFront
		} else if c.GCPCloudCDN != nil {
			c.CloudFront = c.GCPCloudCDN
		}

		// Sync CloudArmor -> WAF
		if c.WAF != nil && c.GCPCloudArmor == nil {
			c.GCPCloudArmor = c.WAF
		} else if c.GCPCloudArmor != nil {
			c.WAF = c.GCPCloudArmor
		}

		// Sync PubSub -> SQS
		if c.SQS != nil && c.GCPPubSub == nil {
			c.GCPPubSub = c.SQS
		} else if c.GCPPubSub != nil {
			sc := *c.GCPPubSub
			c.SQS = &sc
		}

		// Sync CloudLogging -> CloudWatchLogs
		if c.CloudWatchLogs != nil && c.GCPCloudLogging == nil {
			c.GCPCloudLogging = c.CloudWatchLogs
		} else if c.GCPCloudLogging != nil {
			sc := *c.GCPCloudLogging
			c.CloudWatchLogs = &sc
		}

		// Sync CloudMonitoring -> CloudWatchMonitoring
		if c.CloudWatchMonitoring != nil && c.GCPCloudMonitoring == nil {
			c.GCPCloudMonitoring = c.CloudWatchMonitoring
		} else if c.GCPCloudMonitoring != nil {
			sc := *c.GCPCloudMonitoring
			c.CloudWatchMonitoring = &sc
		}

		// Sync IdentityPlatform -> Cognito
		if c.Cognito != nil && c.GCPIdentityPlatform == nil {
			c.GCPIdentityPlatform = c.Cognito
		} else if c.GCPIdentityPlatform != nil {
			sc := *c.GCPIdentityPlatform
			c.Cognito = &sc
		}

		// Sync CloudBuild -> CodePipeline
		if c.CodePipeline != nil && c.GCPCloudBuild == nil {
			c.GCPCloudBuild = c.CodePipeline
		} else if c.GCPCloudBuild != nil {
			sc := *c.GCPCloudBuild
			c.CodePipeline = &sc
		}

		// Sync SecretManager -> SecretsManager
		if c.SecretsManager != nil && c.GCPSecretManager == nil {
			c.GCPSecretManager = c.SecretsManager
		} else if c.GCPSecretManager != nil {
			sc := *c.GCPSecretManager
			c.SecretsManager = &sc
		}

		// Sync CloudKMS -> KMS
		if c.KMS != nil && c.GCPCloudKMS == nil {
			c.GCPCloudKMS = c.KMS
		} else if c.GCPCloudKMS != nil {
			sc := *c.GCPCloudKMS
			c.KMS = &sc
		}

		// Sync APIGateway
		if c.APIGateway != nil && c.GCPAPIGateway == nil {
			c.GCPAPIGateway = c.APIGateway
		} else if c.GCPAPIGateway != nil {
			sc := *c.GCPAPIGateway
			c.APIGateway = &sc
		}

		// Sync VertexAI -> Bedrock
		if c.Bedrock != nil && c.GCPVertexAI == nil {
			c.GCPVertexAI = c.Bedrock
		} else if c.GCPVertexAI != nil {
			sc := *c.GCPVertexAI
			c.Bedrock = &sc
		}

		// Sync Backups
		if c.Backups != nil && c.GCPBackups == nil {
			c.GCPBackups = &struct {
				Compute  *bool `json:"gcp_compute,omitempty"`
				CloudSQL *bool `json:"gcp_cloudsql,omitempty"`
				GCS      *bool `json:"gcp_gcs,omitempty"`
			}{
				Compute:  c.Backups.EC2,
				CloudSQL: c.Backups.Rds,
				GCS:      c.Backups.S3,
			}
		} else if c.GCPBackups != nil {
			c.Backups = &struct {
				EC2         *bool `json:"ec2,omitempty"`
				Rds         *bool `json:"rds,omitempty"`
				ElastiCache *bool `json:"elasticache,omitempty"`
				DynamoDB    *bool `json:"dynamodb,omitempty"`
				S3          *bool `json:"s3,omitempty"`
			}{
				EC2: c.GCPBackups.Compute,
				Rds: c.GCPBackups.CloudSQL,
				S3:  c.GCPBackups.GCS,
			}
		}
	}

	// Clear ALL legacy fields to prevent them from being serialized
	// We only want cloud-prefixed fields in the output
	c.VPC = ""
	c.EC2 = ""
	c.Resource = ""
	c.Bastion = nil
	c.ALB = nil
	c.CloudFront = nil
	c.WAF = nil
	c.Postgres = nil
	c.ElastiCache = nil
	c.S3 = nil
	c.DynamoDB = nil
	c.SQS = nil
	c.MSK = nil
	c.CloudWatchLogs = nil
	c.CloudWatchMonitoring = nil
	c.Grafana = nil
	c.Cognito = nil
	c.APIGateway = nil
	c.KMS = nil
	c.SecretsManager = nil
	c.OpenSearch = nil
	c.Bedrock = nil
	c.Lambda = nil
	c.CodePipeline = nil
	c.GitHubActions = nil
	c.Backups = nil
}

// IsLambdaArchitecture returns true if the stack uses Lambda as its compute
// layer. Reads only c.AWSLambda — legacy shapes (c.Lambda *bool and the
// c.Resource string "Lambda" / "Serverless") are promoted to c.AWSLambda by
// Components.Normalize (AWS branch). ComposeStack / ComposeSingle call
// Normalize at entry, so most callers never need to think about this; direct
// callers of IsLambdaArchitecture on a legacy-shaped Components must call
// Normalize first. See #76.
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
func (c *Components) BackupsSelected() bool {
	return c.AWSBackupsSelected() || c.GCPBackupsSelected() || c.legacyBackupsSelected()
}

// legacyBackupsSelected checks the old Backups field for backward compatibility.
func (c *Components) legacyBackupsSelected() bool {
	if c == nil || c.Backups == nil {
		return false
	}
	b := c.Backups
	return boolVal(b.EC2) || boolVal(b.Rds) || boolVal(b.ElastiCache) || boolVal(b.DynamoDB) || boolVal(b.S3)
}

func (c *Config) Normalize() {
	if c == nil {
		return
	}

	// If cloud is not set, we can't safely clear fields, but we try to infer it.
	// This happens when parsing Config from a standalone block.
	if c.Cloud == "" {
		if c.GCPCompute != nil || c.GCPGKE != nil || c.GCPCloudSQL != nil {
			c.Cloud = "GCP"
		} else if c.AWSEC2 != nil || c.AWSEKS != nil || c.AWSRDS != nil || c.AWSCloudfront != nil || c.AWSS3 != nil {
			c.Cloud = "AWS"
		}
	}

	switch c.Cloud {
	case "AWS":
		// Clear GCP specific fields
		c.GCPCompute = nil
		c.GCPGKE = nil
		c.GCPCloudSQL = nil
		c.GCPMemorystore = nil
		c.GCPGCS = nil
		c.GCPPubSub = nil
		c.GCPCloudLogging = nil
		c.GCPIdentityPlatform = nil
		c.GCPCloudCDN = nil
		c.GCPLoadbalancer = nil
		c.GCPBackups = nil
		c.GCPCloudRun = nil
		c.GCPCloudFunctions = nil

		// First: Migrate legacy AWS fields TO cloud-prefixed fields (to preserve input data)
		if c.EC2 != nil && (c.EC2.NumServers != "" || c.EC2.NumCoresPerServer != "" || c.EC2.DiskSizePerServer != "") {
			if c.AWSEC2 == nil {
				c.AWSEC2 = &struct {
					InstanceType       string `json:"instanceType,omitempty"`
					NumServers         string `json:"numServers,omitempty"`
					NumCoresPerServer  string `json:"numCoresPerServer,omitempty"`
					DiskSizePerServer  string `json:"diskSizePerServer,omitempty"`
					UserData           string `json:"userData,omitempty"`
					UserDataURL        string `json:"userDataURL,omitempty"`
					CustomIngressPorts []int  `json:"customIngressPorts,omitempty"`
					SSHPublicKey          string `json:"sshPublicKey,omitempty"`
					EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
				}{}
			}
			if c.EC2.NumServers != "" {
				c.AWSEC2.NumServers = c.EC2.NumServers
			}
			if c.EC2.NumCoresPerServer != "" {
				c.AWSEC2.NumCoresPerServer = c.EC2.NumCoresPerServer
			}
			if c.EC2.DiskSizePerServer != "" {
				c.AWSEC2.DiskSizePerServer = c.EC2.DiskSizePerServer
			}
		}
		if c.Eks != nil && (c.Eks.DesiredSize != "" || c.Eks.InstanceType != "" || c.Eks.HaControlPlane != nil) {
			if c.AWSEKS == nil {
				c.AWSEKS = &struct {
					HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
					ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
					DesiredSize            string `json:"desiredSize,omitempty"`
					MaxSize                string `json:"maxSize,omitempty"`
					MinSize                string `json:"minSize,omitempty"`
					InstanceType           string `json:"instanceType,omitempty"`
				}{}
			}
			if c.Eks.HaControlPlane != nil {
				c.AWSEKS.HaControlPlane = c.Eks.HaControlPlane
			}
			if c.Eks.ControlPlaneVisibility != "" {
				c.AWSEKS.ControlPlaneVisibility = c.Eks.ControlPlaneVisibility
			}
			if c.Eks.DesiredSize != "" {
				c.AWSEKS.DesiredSize = c.Eks.DesiredSize
			}
			if c.Eks.MaxSize != "" {
				c.AWSEKS.MaxSize = c.Eks.MaxSize
			}
			if c.Eks.MinSize != "" {
				c.AWSEKS.MinSize = c.Eks.MinSize
			}
			if c.Eks.InstanceType != "" {
				c.AWSEKS.InstanceType = c.Eks.InstanceType
			}
		}
		if c.Cloudfront != nil && (c.Cloudfront.DefaultTtl != nil || c.Cloudfront.OriginPath != nil || c.Cloudfront.CachePaths != nil) {
			if c.AWSCloudfront == nil {
				c.AWSCloudfront = &struct {
					DefaultTtl *string `json:"defaultTtl,omitempty"`
					OriginPath *string `json:"originPath,omitempty"`
					CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
				}{}
			}
			if c.Cloudfront.DefaultTtl != nil {
				c.AWSCloudfront.DefaultTtl = c.Cloudfront.DefaultTtl
			}
			if c.Cloudfront.OriginPath != nil {
				c.AWSCloudfront.OriginPath = c.Cloudfront.OriginPath
			} else if c.Cloudfront.CachePaths != nil {
				c.AWSCloudfront.OriginPath = c.Cloudfront.CachePaths
				c.Cloudfront.CachePaths = nil // migrated to OriginPath
			}
		}
		// Migrate AWSCloudfront.CachePaths → OriginPath (deprecated field on new struct)
		if c.AWSCloudfront != nil && c.AWSCloudfront.CachePaths != nil {
			if c.AWSCloudfront.OriginPath == nil {
				c.AWSCloudfront.OriginPath = c.AWSCloudfront.CachePaths
			}
			c.AWSCloudfront.CachePaths = nil
		}
		if c.RDS != nil && (c.RDS.CPUSize != "" || c.RDS.StorageSize != "") {
			if c.AWSRDS == nil {
				c.AWSRDS = &struct {
					CPUSize      string `json:"cpuSize,omitempty"`
					ReadReplicas string `json:"readReplicas,omitempty"`
					StorageSize  string `json:"storageSize,omitempty"`
				}{}
			}
			if c.RDS.CPUSize != "" {
				c.AWSRDS.CPUSize = c.RDS.CPUSize
			}
			if c.RDS.ReadReplicas != "" {
				c.AWSRDS.ReadReplicas = c.RDS.ReadReplicas
			}
			if c.RDS.StorageSize != "" {
				c.AWSRDS.StorageSize = c.RDS.StorageSize
			}
		}
		if c.ElastiCache != nil && (c.ElastiCache.HA != nil || c.ElastiCache.Storage != "" || c.ElastiCache.NodeSize != "") {
			if c.AWSElastiCache == nil {
				c.AWSElastiCache = &struct {
					HA       *bool  `json:"ha,omitempty"`
					Storage  string `json:"storageSize,omitempty"`
					NodeSize string `json:"nodeSize,omitempty"`
					Replicas string `json:"replicas,omitempty"`
				}{}
			}
			if c.ElastiCache.HA != nil {
				c.AWSElastiCache.HA = c.ElastiCache.HA
			}
			if c.ElastiCache.Storage != "" {
				c.AWSElastiCache.Storage = c.ElastiCache.Storage
			}
			if c.ElastiCache.NodeSize != "" {
				c.AWSElastiCache.NodeSize = c.ElastiCache.NodeSize
			}
			if c.ElastiCache.Replicas != "" {
				c.AWSElastiCache.Replicas = c.ElastiCache.Replicas
			}
		}
		if c.S3 != nil && c.S3.Versioning != nil {
			if c.AWSS3 == nil {
				c.AWSS3 = &struct {
					Versioning *bool `json:"versioning,omitempty"`
				}{}
			}
			c.AWSS3.Versioning = c.S3.Versioning
		}
		if c.CloudWatchLogs != nil && c.CloudWatchLogs.RetentionDays > 0 {
			if c.AWSCloudWatchLogs == nil {
				c.AWSCloudWatchLogs = &struct {
					RetentionDays int `json:"retentionDays,omitempty"`
				}{}
			}
			c.AWSCloudWatchLogs.RetentionDays = c.CloudWatchLogs.RetentionDays
		}
		if c.Cognito != nil && (c.Cognito.MFARequired != nil || c.Cognito.SignInType != "") {
			if c.AWSCognito == nil {
				c.AWSCognito = &struct {
					SignInType  string `json:"signInType,omitempty"`
					MFARequired *bool  `json:"mfaRequired,omitempty"`
					Okta        *struct {
						SelfSignupAllowed *bool `json:"selfSignupAllowed,omitempty"`
					} `json:"okta,omitempty"`
					Auth0 *struct {
						MFARequired *bool `json:"mfaRequired,omitempty"`
					} `json:"auth0,omitempty"`
				}{}
			}
			c.AWSCognito.MFARequired = c.Cognito.MFARequired
			c.AWSCognito.SignInType = c.Cognito.SignInType
		}
		if c.SQS != nil && (c.SQS.Type != "" || c.SQS.VisibilityTimeout != "") {
			if c.AWSSQS == nil {
				c.AWSSQS = &struct {
					Type              string `json:"type,omitempty"`
					VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
				}{}
			}
			c.AWSSQS.Type = c.SQS.Type
			c.AWSSQS.VisibilityTimeout = c.SQS.VisibilityTimeout
		}

		// Then: Migrate cloud-prefixed AWS fields to legacy fields for unified checking
		// (This is not needed anymore since we clear legacy fields, but keeping for internal logic)
		if c.AWSEC2 != nil {
			if c.EC2 == nil {
				c.EC2 = &struct {
					NumServers        string `json:"numServers,omitempty"`
					NumCoresPerServer string `json:"numCoresPerServer,omitempty"`
					DiskSizePerServer string `json:"diskSizePerServer,omitempty"`
				}{}
			}
			c.EC2.NumServers = c.AWSEC2.NumServers
			c.EC2.NumCoresPerServer = c.AWSEC2.NumCoresPerServer
			c.EC2.DiskSizePerServer = c.AWSEC2.DiskSizePerServer
			// Note: UserData, CustomIngressPorts, SSHPublicKey are standalone EC2 only — no legacy equivalent
		}
		if c.AWSEKS != nil {
			if c.Eks == nil {
				c.Eks = &struct {
					HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
					ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
					DesiredSize            string `json:"desiredSize,omitempty"`
					MaxSize                string `json:"maxSize,omitempty"`
					MinSize                string `json:"minSize,omitempty"`
					InstanceType           string `json:"instanceType,omitempty"`
				}{}
			}
			c.Eks.HaControlPlane = c.AWSEKS.HaControlPlane
			c.Eks.ControlPlaneVisibility = c.AWSEKS.ControlPlaneVisibility
			c.Eks.DesiredSize = c.AWSEKS.DesiredSize
			c.Eks.MaxSize = c.AWSEKS.MaxSize
			c.Eks.MinSize = c.AWSEKS.MinSize
			c.Eks.InstanceType = c.AWSEKS.InstanceType
		}
		if c.AWSCloudfront != nil {
			if c.Cloudfront == nil {
				c.Cloudfront = &struct {
					DefaultTtl *string `json:"defaultTtl,omitempty"`
					OriginPath *string `json:"originPath,omitempty"`
					CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
				}{}
			}
			c.Cloudfront.DefaultTtl = c.AWSCloudfront.DefaultTtl
			if c.AWSCloudfront.OriginPath != nil {
				c.Cloudfront.OriginPath = c.AWSCloudfront.OriginPath
			} else if c.AWSCloudfront.CachePaths != nil {
				c.Cloudfront.OriginPath = c.AWSCloudfront.CachePaths
				c.AWSCloudfront.OriginPath = c.AWSCloudfront.CachePaths // migrate in-place
				c.AWSCloudfront.CachePaths = nil                        // clear deprecated
			}
		}
		if c.AWSRDS != nil {
			if c.RDS == nil {
				c.RDS = &struct {
					CPUSize      string `json:"cpuSize,omitempty"`
					ReadReplicas string `json:"readReplicas,omitempty"`
					StorageSize  string `json:"storageSize,omitempty"`
				}{}
			}
			c.RDS.CPUSize = c.AWSRDS.CPUSize
			c.RDS.ReadReplicas = c.AWSRDS.ReadReplicas
			c.RDS.StorageSize = c.AWSRDS.StorageSize
		}
		if c.AWSElastiCache != nil {
			if c.ElastiCache == nil {
				c.ElastiCache = &struct {
					HA       *bool  `json:"ha,omitempty"`
					Storage  string `json:"storageSize,omitempty"`
					NodeSize string `json:"nodeSize,omitempty"`
					Replicas string `json:"replicas,omitempty"`
				}{}
			}
			c.ElastiCache.HA = c.AWSElastiCache.HA
			c.ElastiCache.Storage = c.AWSElastiCache.Storage
			c.ElastiCache.NodeSize = c.AWSElastiCache.NodeSize
			c.ElastiCache.Replicas = c.AWSElastiCache.Replicas
		}
		if c.AWSS3 != nil {
			if c.S3 == nil {
				c.S3 = &struct {
					Versioning *bool `json:"versioning,omitempty"`
				}{}
			}
			c.S3.Versioning = c.AWSS3.Versioning
		}
		if c.AWSDynamoDB != nil {
			if c.DynamoDB == nil {
				c.DynamoDB = &struct {
					Type string `json:"type,omitempty"`
				}{}
			}
			c.DynamoDB.Type = c.AWSDynamoDB.Type
		}
		if c.AWSSQS != nil {
			if c.SQS == nil {
				c.SQS = &struct {
					Type              string `json:"type,omitempty"`
					VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
				}{}
			}
			c.SQS.Type = c.AWSSQS.Type
			c.SQS.VisibilityTimeout = c.AWSSQS.VisibilityTimeout
		}
		if c.AWSMSK != nil {
			if c.MSK == nil {
				c.MSK = &struct {
					Retention string `json:"retentionPeriod,omitempty"`
				}{}
			}
			c.MSK.Retention = c.AWSMSK.Retention
		}
		if c.AWSCloudWatchLogs != nil {
			if c.CloudWatchLogs == nil {
				c.CloudWatchLogs = &struct {
					RetentionDays int `json:"retentionDays,omitempty"`
				}{}
			}
			c.CloudWatchLogs.RetentionDays = c.AWSCloudWatchLogs.RetentionDays
		}
		if c.AWSCloudWatchMonitoring != nil {
			if c.CloudWatchMonitoring == nil {
				c.CloudWatchMonitoring = &struct {
					RetentionDays int `json:"retentionDays,omitempty"`
				}{}
			}
			c.CloudWatchMonitoring.RetentionDays = c.AWSCloudWatchMonitoring.RetentionDays
		}
		if c.AWSCognito != nil {
			if c.Cognito == nil {
				c.Cognito = &struct {
					SignInType  string `json:"signInType,omitempty"`
					MFARequired *bool  `json:"mfaRequired,omitempty"`
				}{}
			}
			c.Cognito.SignInType = c.AWSCognito.SignInType
			c.Cognito.MFARequired = c.AWSCognito.MFARequired
		}
		if c.AWSLambda != nil {
			if c.Lambda == nil {
				c.Lambda = &struct {
					Runtime    string `json:"runtime,omitempty"`
					MemorySize string `json:"memorySize,omitempty"`
					Timeout    string `json:"timeout,omitempty"`
				}{}
			}
			c.Lambda.Runtime = c.AWSLambda.Runtime
			c.Lambda.MemorySize = c.AWSLambda.MemorySize
			c.Lambda.Timeout = c.AWSLambda.Timeout
		}
		if c.AWSAPIGateway != nil {
			if c.APIGateway == nil {
				c.APIGateway = &struct {
					DomainName     string `json:"domainName,omitempty"`
					CertificateArn string `json:"certificateArn,omitempty"`
				}{}
			}
			c.APIGateway.DomainName = c.AWSAPIGateway.DomainName
			c.APIGateway.CertificateArn = c.AWSAPIGateway.CertificateArn
		}
		if c.AWSKMS != nil {
			if c.KMS == nil {
				c.KMS = &struct {
					NumKeys string `json:"numKeys,omitempty"`
				}{}
			}
			c.KMS.NumKeys = c.AWSKMS.NumKeys
		}
		if c.AWSSecretsManager != nil {
			if c.SecretsManager == nil {
				c.SecretsManager = &struct {
					NumSecrets string `json:"numSecrets,omitempty"`
				}{}
			}
			c.SecretsManager.NumSecrets = c.AWSSecretsManager.NumSecrets
		}
		if c.AWSOpenSearch != nil {
			if c.OpenSearch == nil {
				c.OpenSearch = &struct {
					DeploymentType string `json:"deploymentType,omitempty"`
					InstanceType   string `json:"instanceType,omitempty"`
					StorageSize    string `json:"storageSize,omitempty"`
					MultiAZ        *bool  `json:"multiAz,omitempty"`
				}{}
			}
			c.OpenSearch.DeploymentType = c.AWSOpenSearch.DeploymentType
			c.OpenSearch.InstanceType = c.AWSOpenSearch.InstanceType
			c.OpenSearch.StorageSize = c.AWSOpenSearch.StorageSize
			c.OpenSearch.MultiAZ = c.AWSOpenSearch.MultiAZ
		}
		if c.AWSBedrock != nil {
			if c.Bedrock == nil {
				c.Bedrock = &struct {
					KnowledgeBaseName string `json:"knowledgeBaseName,omitempty"`
					ModelID           string `json:"modelId,omitempty"`
					EmbeddingModelID  string `json:"embeddingModelId,omitempty"`
				}{}
			}
			c.Bedrock.KnowledgeBaseName = c.AWSBedrock.KnowledgeBaseName
			c.Bedrock.ModelID = c.AWSBedrock.ModelID
			c.Bedrock.EmbeddingModelID = c.AWSBedrock.EmbeddingModelID
		}
		if c.AWSBackups != nil {
			if c.Backups == nil {
				c.Backups = &struct {
					Details map[string]struct {
						FrequencyHours int    `json:"frequencyHours,omitempty"`
						RetentionDays  int    `json:"retentionDays,omitempty"`
						Region         string `json:"region,omitempty"`
					} `json:"details,omitempty"`
				}{}
			}
			if c.Backups.Details == nil {
				c.Backups.Details = make(map[string]struct {
					FrequencyHours int    `json:"frequencyHours,omitempty"`
					RetentionDays  int    `json:"retentionDays,omitempty"`
					Region         string `json:"region,omitempty"`
				})
			}
			if c.AWSBackups.EC2 != nil {
				c.Backups.Details["ec2"] = *c.AWSBackups.EC2
			}
			if c.AWSBackups.RDS != nil {
				c.Backups.Details["rds"] = *c.AWSBackups.RDS
			}
			if c.AWSBackups.ElastiCache != nil {
				c.Backups.Details["elasticache"] = *c.AWSBackups.ElastiCache
			}
			if c.AWSBackups.DynamoDB != nil {
				c.Backups.Details["dynamodb"] = *c.AWSBackups.DynamoDB
			}
			if c.AWSBackups.S3 != nil {
				c.Backups.Details["s3"] = *c.AWSBackups.S3
			}
		}
	case "GCP":
		// Clear AWS specific fields
		c.AWSEC2 = nil
		c.AWSEKS = nil
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
		c.AWSAPIGateway = nil
		c.AWSKMS = nil
		c.AWSSecretsManager = nil
		c.AWSOpenSearch = nil
		c.AWSBedrock = nil
		c.AWSBackups = nil

		// Sync legacy and cloud-specific fields for GCP
		if c.GCPCompute != nil {
			if c.EC2 == nil {
				c.EC2 = &struct {
					NumServers        string `json:"numServers,omitempty"`
					NumCoresPerServer string `json:"numCoresPerServer,omitempty"`
					DiskSizePerServer string `json:"diskSizePerServer,omitempty"`
				}{}
			}
			c.EC2.NumServers = c.GCPCompute.NumServers
			c.EC2.NumCoresPerServer = "2" // default if not specified
			c.EC2.DiskSizePerServer = fmt.Sprintf("%d", c.GCPCompute.DiskSizeGb)
		}
		if c.GCPGKE != nil {
			if c.Eks == nil {
				c.Eks = &struct {
					HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
					ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
					DesiredSize            string `json:"desiredSize,omitempty"`
					MaxSize                string `json:"maxSize,omitempty"`
					MinSize                string `json:"minSize,omitempty"`
					InstanceType           string `json:"instanceType,omitempty"`
				}{}
			}
			c.Eks.DesiredSize = c.GCPGKE.NodeCount
			c.Eks.MaxSize = c.GCPGKE.NodeCount
			c.Eks.MinSize = c.GCPGKE.NodeCount
			c.Eks.InstanceType = c.GCPGKE.MachineType
			c.Eks.HaControlPlane = c.GCPGKE.Regional
		}
		if c.GCPCloudSQL != nil {
			if c.RDS == nil {
				c.RDS = &struct {
					CPUSize      string `json:"cpuSize,omitempty"`
					ReadReplicas string `json:"readReplicas,omitempty"`
					StorageSize  string `json:"storageSize,omitempty"`
				}{}
			}
			c.RDS.CPUSize = c.GCPCloudSQL.Tier
			c.RDS.StorageSize = fmt.Sprintf("%d", c.GCPCloudSQL.DiskSizeGb)
			// No ReadReplicas field in GCPCloudSQL yet
		}
		if c.GCPMemorystore != nil {
			if c.ElastiCache == nil {
				c.ElastiCache = &struct {
					HA       *bool  `json:"ha,omitempty"`
					Storage  string `json:"storageSize,omitempty"`
					NodeSize string `json:"nodeSize,omitempty"`
					Replicas string `json:"replicas,omitempty"`
				}{}
			}
			c.ElastiCache.Storage = fmt.Sprintf("%d", c.GCPMemorystore.MemorySizeGb)
			c.ElastiCache.NodeSize = c.GCPMemorystore.Tier
		}
		if c.GCPGCS != nil {
			if c.S3 == nil {
				c.S3 = &struct {
					Versioning *bool `json:"versioning,omitempty"`
				}{}
			}
			c.S3.Versioning = c.GCPGCS.Versioning
		}
		if c.GCPPubSub != nil {
			if c.SQS == nil {
				c.SQS = &struct {
					Type              string `json:"type,omitempty"`
					VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
				}{}
			}
			// VisibilityTimeout doesn't map well but let's keep it empty or use retention
			c.SQS.VisibilityTimeout = c.GCPPubSub.MessageRetentionDuration
		}
		if c.GCPCloudLogging != nil {
			if c.CloudWatchLogs == nil {
				c.CloudWatchLogs = &struct {
					RetentionDays int `json:"retentionDays,omitempty"`
				}{}
			}
			c.CloudWatchLogs.RetentionDays = c.GCPCloudLogging.RetentionDays
		}
		if c.GCPIdentityPlatform != nil {
			if c.Cognito == nil {
				c.Cognito = &struct {
					SignInType  string `json:"signInType,omitempty"`
					MFARequired *bool  `json:"mfaRequired,omitempty"`
				}{}
			}
			c.Cognito.MFARequired = c.GCPIdentityPlatform.MFARequired
		}
		if c.GCPCloudCDN != nil {
			if c.Cloudfront == nil {
				c.Cloudfront = &struct {
					DefaultTtl *string `json:"defaultTtl,omitempty"`
					OriginPath *string `json:"originPath,omitempty"`
					CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
				}{}
			}
			c.Cloudfront.DefaultTtl = &c.GCPCloudCDN.DefaultTtl
			if c.GCPCloudCDN.OriginPath != "" {
				c.Cloudfront.OriginPath = &c.GCPCloudCDN.OriginPath
			} else if c.GCPCloudCDN.CachePaths != "" {
				c.Cloudfront.OriginPath = &c.GCPCloudCDN.CachePaths
				c.GCPCloudCDN.CachePaths = "" // migrated to OriginPath
			}
		}
	if c.GCPBackups != nil {
		if c.Backups == nil {
			c.Backups = &struct {
				Details map[string]struct {
					FrequencyHours int    `json:"frequencyHours,omitempty"`
					RetentionDays  int    `json:"retentionDays,omitempty"`
					Region         string `json:"region,omitempty"`
				} `json:"details,omitempty"`
			}{}
		}
		if c.Backups.Details == nil {
			c.Backups.Details = make(map[string]struct {
				FrequencyHours int    `json:"frequencyHours,omitempty"`
				RetentionDays  int    `json:"retentionDays,omitempty"`
				Region         string `json:"region,omitempty"`
			})
		}
			if c.GCPBackups.Compute != nil {
				c.Backups.Details["compute"] = struct {
					FrequencyHours int    `json:"frequencyHours,omitempty"`
					RetentionDays  int    `json:"retentionDays,omitempty"`
					Region         string `json:"region,omitempty"`
				}{
					FrequencyHours: c.GCPBackups.Compute.FrequencyHours,
					RetentionDays:  c.GCPBackups.Compute.RetentionDays,
				}
			}
			if c.GCPBackups.CloudSQL != nil {
				c.Backups.Details["cloudsql"] = struct {
					FrequencyHours int    `json:"frequencyHours,omitempty"`
					RetentionDays  int    `json:"retentionDays,omitempty"`
					Region         string `json:"region,omitempty"`
				}{
					RetentionDays: c.GCPBackups.CloudSQL.RetentionDays,
				}
			}
			if c.GCPBackups.GCS != nil && c.GCPBackups.GCS.Enabled != nil && *c.GCPBackups.GCS.Enabled {
				c.Backups.Details["gcs"] = struct {
					FrequencyHours int    `json:"frequencyHours,omitempty"`
					RetentionDays  int    `json:"retentionDays,omitempty"`
					Region         string `json:"region,omitempty"`
				}{
					FrequencyHours: 24,
					RetentionDays:  30,
				}
			}
		}
	}

	// Clear ALL legacy fields to prevent them from being serialized
	// We only want cloud-prefixed fields in the output
	c.EC2 = nil
	c.Eks = nil
	c.Cloudfront = nil
	c.RDS = nil
	c.ElastiCache = nil
	c.S3 = nil
	c.DynamoDB = nil
	c.SQS = nil
	c.MSK = nil
	c.CloudWatchLogs = nil
	c.CloudWatchMonitoring = nil
	c.Cognito = nil
	c.Lambda = nil
	c.APIGateway = nil
	c.KMS = nil
	c.SecretsManager = nil
	c.OpenSearch = nil
	c.Bedrock = nil
	c.Backups = nil
}
