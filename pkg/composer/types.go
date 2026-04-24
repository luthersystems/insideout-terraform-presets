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
// callers with legacy-shaped session JSON must upgrade via reliable's
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
	}
	if c.Cloud == "GCP" {
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
	}
}

// IsLambdaArchitecture returns true if the stack uses Lambda as its compute
// layer. Reads only c.AWSLambda. In v0.4.0 the legacy c.Lambda / c.Resource
// fields no longer exist; callers with pre-Phase-4 session JSON must fold
// those via reliable's composeradapter before reaching composer.
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
// 4). Legacy session JSON should be upgraded by reliable's composeradapter
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
		// Clear every GCP sub-config (14 fields — keep in sync with the
		// GCPConfiguration section of the Config struct).
		c.GCPCompute = nil
		c.GCPGKE = nil
		c.GCPCloudSQL = nil
		c.GCPMemorystore = nil
		c.GCPGCS = nil
		c.GCPPubSub = nil
		c.GCPCloudLogging = nil
		c.GCPCloudRun = nil
		c.GCPCloudFunctions = nil
		c.GCPIdentityPlatform = nil
		c.GCPAPIGateway = nil
		c.GCPCloudCDN = nil
		c.GCPLoadbalancer = nil
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
		c.AWSAPIGateway = nil
		c.AWSKMS = nil
		c.AWSSecretsManager = nil
		c.AWSOpenSearch = nil
		c.AWSBedrock = nil
		c.AWSBackups = nil
	}
}
