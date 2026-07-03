package composer

// TestMapperKeysSubsetOfModuleVariables is the generic safety net for the
// upstream issue #131 audit — it verifies that every key the mapper writes
// for a given component is a declared variable in that module's
// variables.tf. The existing TestComposeStack_TFVarsMatchVariables only
// checks the *root* variables.tf the composer assembles, which means it
// can't catch mapper bugs where compose.go silently filters out tfvars
// whose key isn't a declared module variable (the most common shape of
// audit findings 5–8).
//
// Adding a new mapper case that writes a key the target module didn't
// declare will fail this test. Renaming a module variable upstream
// without updating the mapper will fail this test.

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readMapperSource returns the DefaultMapper source text for the
// allowlist-honesty grep in TestMapperReadConfigSubStructsInAllowlistAreTrulyUnread.
func readMapperSource() (string, error) {
	b, err := os.ReadFile("mapper.go")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// kitchenSinkConfig populates every cfg sub-struct the mapper reads with
// values that exercise each mapper branch. Used to drive a single mapper
// invocation per ComponentKey for the cross-module check below.
//
// The completeness of THIS struct gates whether
// TestMapperKeysSubsetOfModuleVariables can detect mapper-key/preset-var
// drift. The historical compute-mapper bug (`boot_disk_size_gb` →
// `disk_size_gb`) and the SM `secret_id` bug from #253 both slipped past
// the subset gate because the corresponding cfg sub-struct (GCPCompute,
// no GCP coverage at all) was unset, so the mapper branch never fired.
// Add a new entry here whenever a new cfg sub-struct lands in types.go.
func kitchenSinkConfig() *Config {
	t := true
	one := 1
	ten := 10

	cfg := &Config{
		Cloud:  "aws",
		Region: "us-east-1",
	}

	// AWS
	cfg.AWSEC2 = &struct {
		InstanceType          string `json:"instanceType,omitempty"`
		NumServers            string `json:"numServers,omitempty"`
		NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
		DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
		UserData              string `json:"userData,omitempty"`
		UserDataURL           string `json:"userDataURL,omitempty"`
		CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
		SSHPublicKey          string `json:"sshPublicKey,omitempty"`
		EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		GPUEnabled            *bool  `json:"gpuEnabled,omitempty"`
	}{
		// GPUEnabled pairs with a real GPU family (g5.xlarge) so the config is
		// internally consistent: the #759 mapper validation rejects GPUEnabled
		// with a non-GPU instance type like t3.medium, which would fail this
		// kitchen-sink mapper invocation outright.
		InstanceType:          "g5.xlarge",
		DiskSizePerServer:     "100",
		UserData:              "#!/bin/bash\necho hello",
		CustomIngressPorts:    []int{8080},
		SSHPublicKey:          "ssh-rsa AAAA...",
		EnableInstanceConnect: &t,
		// GPUEnabled exercises the #759 gpu_enabled tfvar emission so the
		// keys-subset gate confirms it is a declared module variable.
		GPUEnabled: &t,
	}
	cfg.AWSEKS = &struct {
		HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
		ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
		DesiredSize            string `json:"desiredSize,omitempty"`
		MaxSize                string `json:"maxSize,omitempty"`
		MinSize                string `json:"minSize,omitempty"`
		InstanceType           string `json:"instanceType,omitempty"`
		GPUEnabled             *bool  `json:"gpuEnabled,omitempty"`
	}{
		HaControlPlane:         &t,
		ControlPlaneVisibility: "private",
		DesiredSize:            "2",
		MinSize:                "1",
		MaxSize:                "3",
		// GPUEnabled pairs with a real GPU family (g5.xlarge) so the config is
		// internally consistent: the #759 mapper validation rejects GPUEnabled
		// with a non-GPU instance type like t3.medium.
		InstanceType: "g5.xlarge",
		// GPUEnabled exercises the #759 instance-type default path. The mapper
		// deliberately never emits ami_type — the preset's family auto-derive
		// (_gpu_x86_families → AL2023_x86_64_NVIDIA) owns AMI selection — so
		// this only confirms gpu-related emitted keys are declared variables.
		GPUEnabled: &t,
	}
	cfg.AWSECS = &struct {
		EnableContainerInsights *bool    `json:"enableContainerInsights,omitempty"`
		CapacityProviders       []string `json:"capacityProviders,omitempty"`
		DefaultCapacityProvider string   `json:"defaultCapacityProvider,omitempty"`
		EnableServiceConnect    *bool    `json:"enableServiceConnect,omitempty"`
	}{
		EnableContainerInsights: &t,
		CapacityProviders:       []string{"FARGATE"},
		DefaultCapacityProvider: "FARGATE",
		EnableServiceConnect:    &t,
	}
	cfg.AWSVPC = &struct {
		SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
		EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
		AZCount          *int  `json:"azCount,omitempty"`
	}{SingleNATGateway: &t, EnableNATGateway: &t, AZCount: &ten}
	ttl := "1h"
	op := "/v1"
	cfg.AWSCloudfront = &struct {
		DefaultTtl *string `json:"defaultTtl,omitempty"`
		OriginPath *string `json:"originPath,omitempty"`
		CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
	}{DefaultTtl: &ttl, OriginPath: &op}
	cfg.AWSRDS = &struct {
		CPUSize      string `json:"cpuSize,omitempty"`
		ReadReplicas string `json:"readReplicas,omitempty"`
		StorageSize  string `json:"storageSize,omitempty"`
	}{CPUSize: "8 vCPU", ReadReplicas: "2 read replicas", StorageSize: "200GB"}
	cfg.AWSElastiCache = &struct {
		HA       *bool  `json:"ha,omitempty"`
		Storage  string `json:"storageSize,omitempty"`
		NodeSize string `json:"nodeSize,omitempty"`
		Replicas string `json:"replicas,omitempty"`
	}{HA: &t, Storage: "20GB", NodeSize: "8 vCPU", Replicas: "2 read replicas"}
	cfg.AWSS3 = &struct {
		Versioning *bool `json:"versioning,omitempty"`
	}{Versioning: &t}
	cfg.AWSDynamoDB = &struct {
		Type string `json:"type,omitempty"`
	}{Type: "On demand"}
	cfg.AWSSQS = &struct {
		Type              string `json:"type,omitempty"`
		VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
	}{Type: "FIFO", VisibilityTimeout: "600"}
	cfg.AWSMSK = &struct {
		Retention string `json:"retentionPeriod,omitempty"`
	}{Retention: "7 days"}
	cfg.AWSCloudWatchLogs = &struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	}{RetentionDays: 90}
	cfg.AWSCloudWatchMonitoring = &struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	}{RetentionDays: 90}
	cfg.AWSCognito = &struct {
		SignInType  string `json:"signInType,omitempty"`
		MFARequired *bool  `json:"mfaRequired,omitempty"`
		MFAFactor   string `json:"mfaFactor,omitempty"`
		Okta        *struct {
			SelfSignupAllowed *bool `json:"selfSignupAllowed,omitempty"`
		} `json:"okta,omitempty"`
		Auth0 *struct {
			MFARequired *bool `json:"mfaRequired,omitempty"`
		} `json:"auth0,omitempty"`
	}{SignInType: "email", MFARequired: &t, MFAFactor: "TOTP"}
	cfg.AWSLambda = &struct {
		Runtime    string `json:"runtime,omitempty"`
		MemorySize string `json:"memorySize,omitempty"`
		Timeout    string `json:"timeout,omitempty"`
	}{Runtime: "nodejs20.x", MemorySize: "512", Timeout: "30s"}
	cfg.AWSAPIGateway = &struct {
		DomainName     string `json:"domainName,omitempty"`
		CertificateArn string `json:"certificateArn,omitempty"`
	}{DomainName: "api.example.com", CertificateArn: "arn:aws:acm:us-east-1:123456789012:certificate/abc"}
	cfg.AWSKMS = &struct {
		NumKeys string `json:"numKeys,omitempty"`
	}{NumKeys: "1"}
	cfg.AWSSecretsManager = &struct {
		NumSecrets string `json:"numSecrets,omitempty"`
	}{NumSecrets: "1"}
	cfg.AWSOpenSearch = &struct {
		DeploymentType string `json:"deploymentType,omitempty"`
		InstanceType   string `json:"instanceType,omitempty"`
		StorageSize    string `json:"storageSize,omitempty"`
		MultiAZ        *bool  `json:"multiAz,omitempty"`
	}{DeploymentType: "managed", InstanceType: "t3.medium.search", StorageSize: "1TB", MultiAZ: &t}
	cfg.AWSBedrock = &struct {
		KnowledgeBaseName   string `json:"knowledgeBaseName,omitempty"`
		ModelID             string `json:"modelId,omitempty"`
		EmbeddingModelID    string `json:"embeddingModelId,omitempty"`
		EnableKnowledgeBase *bool  `json:"enableKnowledgeBase,omitempty"`
		VectorStore         string `json:"vectorStore,omitempty"`
	}{KnowledgeBaseName: "kb", ModelID: "anthropic.claude-3", EmbeddingModelID: "amazon.titan-embed", EnableKnowledgeBase: &t, VectorStore: "s3vectors"}
	cfg.AWSBedrockAgent = &struct {
		FoundationModel string `json:"foundationModel,omitempty"`
		Instruction     string `json:"instruction,omitempty"`
		AgentName       string `json:"agentName,omitempty"`
	}{FoundationModel: "anthropic.claude-3-5-sonnet-20240620-v1:0", Instruction: "You are a helpful assistant that answers questions about the customer's documents.", AgentName: "support-agent"}
	// AWSAgentCoreGateway (#763): exercises every config field — GatewayName,
	// ProtocolType, and the inbound-auth surface (JwtDiscoveryURL +
	// allowed-audience/clients allowlists). ProtocolType "MCP" and the https://
	// discovery URL are internally consistent with the preset's validations so
	// the kitchen-sink config would also pass a real plan. The mapper emits
	// gateway_name / protocol_type / jwt_discovery_url / jwt_allowed_audience /
	// jwt_allowed_clients, all of which the keys-subset gate confirms are
	// declared aws/agentcore_gateway variables.
	cfg.AWSAgentCoreGateway = &struct {
		GatewayName        string   `json:"gatewayName,omitempty"`
		ProtocolType       string   `json:"protocolType,omitempty"`
		JwtDiscoveryURL    string   `json:"jwtDiscoveryUrl,omitempty"`
		JwtAllowedAudience []string `json:"jwtAllowedAudience,omitempty"`
		JwtAllowedClients  []string `json:"jwtAllowedClients,omitempty"`
	}{
		GatewayName:        "support-tools",
		ProtocolType:       "MCP",
		JwtDiscoveryURL:    "https://auth.example.com/.well-known/openid-configuration",
		JwtAllowedAudience: []string{"insideout-agents"},
		JwtAllowedClients:  []string{"client-abc"},
	}
	// AWSKendra (#760): exercises every config field — IndexName, Edition, and
	// UserContextPolicy. Edition "ENTERPRISE_EDITION" and UserContextPolicy
	// "ATTRIBUTE_FILTER" are internally consistent with the preset's
	// validations so the kitchen-sink config would also pass a real plan. The
	// mapper emits index_name / edition / user_context_policy, all of which the
	// keys-subset gate confirms are declared aws/kendra variables.
	cfg.AWSKendra = &struct {
		Edition           string `json:"edition,omitempty"`
		IndexName         string `json:"indexName,omitempty"`
		UserContextPolicy string `json:"userContextPolicy,omitempty"`
	}{
		Edition:           "ENTERPRISE_EDITION",
		IndexName:         "support-search",
		UserContextPolicy: "ATTRIBUTE_FILTER",
	}
	// AWSSageMaker exercises both the #615 Studio fields and the #761
	// inference fields so the keys-subset gate confirms every emitted tfvar
	// (network_mode … enable_inference / model_image / model_data_url /
	// endpoint_instance_type) is a declared aws/sagemaker variable. Values
	// are internally consistent with the preset's validations: EnableInference
	// pairs with a non-empty model_image, an s3:// model_data_url, and an ml.*
	// endpoint_instance_type so the config would also pass a real plan.
	cfg.AWSSageMaker = &AWSSageMakerConfig{
		NetworkMode:               "VpcOnly",
		WorkspaceBucket:           "kitchen-sink-ml-bucket",
		StudioUsers:               []string{"alice"},
		SageMakerManagedPolicyARN: "arn:aws:iam::123456789012:policy/MyScopedSagemaker",
		EnableInference:           &t,
		ModelImage:                "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest",
		ModelDataURL:              "s3://kitchen-sink-ml-bucket/model.tar.gz",
		EndpointInstanceType:      "ml.g5.xlarge",
		ModelEnvironment:          map[string]string{"HF_MODEL_ID": "sshleifer/tiny-distilbert-base-cased-distilled-squad", "HF_TASK": "question-answering"},
	}
	cfg.AWSRoute53 = &struct {
		DomainName   string   `json:"domainName,omitempty"`
		CreateZone   *bool    `json:"createZone,omitempty"`
		ZoneID       string   `json:"zoneId,omitempty"`
		PrivateZone  *bool    `json:"privateZone,omitempty"`
		VPCIDs       []string `json:"vpcIds,omitempty"`
		ForceDestroy *bool    `json:"forceDestroy,omitempty"`
	}{DomainName: "example.com", CreateZone: &t, ZoneID: "Z1234567890ABC", VPCIDs: []string{"vpc-aaa"}, ForceDestroy: &t}
	cfg.AWSACM = &struct {
		DomainName                     string   `json:"domainName,omitempty"`
		SubjectAlternativeNames        []string `json:"subjectAlternativeNames,omitempty"`
		KeyAlgorithm                   string   `json:"keyAlgorithm,omitempty"`
		CertificateTransparencyLogging string   `json:"certificateTransparencyLogging,omitempty"`
		CreateValidation               *bool    `json:"createValidation,omitempty"`
		ValidationTimeout              string   `json:"validationTimeout,omitempty"`
	}{DomainName: "example.com", SubjectAlternativeNames: []string{"www.example.com"}, KeyAlgorithm: "RSA_2048", CertificateTransparencyLogging: "ENABLED", CreateValidation: &t, ValidationTimeout: "45m"}

	// GCP
	// GPU fields set on N1 machines so the kitchen-sink stays internally
	// consistent with the #767 GPU machine-type validation (a GPU on an e2/g2
	// machine would be rejected). This also exercises the gpu_type/gpu_count
	// keys through the subset gate.
	cfg.GCPCompute = &struct {
		NumServers  string `json:"numServers,omitempty"`
		MachineType string `json:"machineType,omitempty"`
		DiskSizeGb  int    `json:"diskSizeGb,omitempty"`
		GPUType     string `json:"gpuType,omitempty"`
		GPUCount    int    `json:"gpuCount,omitempty"`
	}{NumServers: "1", MachineType: "n1-standard-4", DiskSizeGb: 50, GPUType: "nvidia-tesla-t4", GPUCount: 1}
	cfg.GCPGKE = &struct {
		Regional    *bool  `json:"regional,omitempty"`
		NodeCount   string `json:"nodeCount,omitempty"`
		MachineType string `json:"machineType,omitempty"`
		GPUType     string `json:"gpuType,omitempty"`
		GPUCount    int    `json:"gpuCount,omitempty"`
	}{Regional: &t, NodeCount: "3", MachineType: "n1-standard-4", GPUType: "nvidia-tesla-t4", GPUCount: 1}
	cfg.GCPCloudSQL = &struct {
		Tier             string `json:"tier,omitempty"`
		DiskSizeGb       int    `json:"diskSizeGb,omitempty"`
		HighAvailability *bool  `json:"highAvailability,omitempty"`
	}{Tier: "db-f1-micro", DiskSizeGb: 10, HighAvailability: &t}
	cfg.GCPMemorystore = &struct {
		Tier         string `json:"tier,omitempty"`
		MemorySizeGb int    `json:"memorySizeGb,omitempty"`
	}{Tier: "STANDARD_HA", MemorySizeGb: 5}
	cfg.GCPGCS = &struct {
		StorageClass string `json:"storageClass,omitempty"`
		Versioning   *bool  `json:"versioning,omitempty"`
	}{StorageClass: "STANDARD", Versioning: &t}
	cfg.GCPVertexAI = &struct {
		EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
		IndexDimensions       int    `json:"indexDimensions,omitempty"`
		EnableServing         *bool  `json:"enableServing,omitempty"`
		ModelGardenModel      string `json:"modelGardenModel,omitempty"`
		ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
	}{EnableVectorSearch: &t, IndexDimensions: 768, EnableServing: &t, ModelGardenModel: "publishers/google/models/gemma3@gemma-3-1b-it", ModelGardenAcceptEULA: &t}
	cfg.GCPDocumentAI = &struct {
		ProcessorType string `json:"processorType,omitempty"`
		Location      string `json:"location,omitempty"`
	}{ProcessorType: "FORM_PARSER_PROCESSOR", Location: "eu"}
	cfg.GCPModelArmor = &struct {
		FilterConfidenceLevel string `json:"filterConfidenceLevel,omitempty"`
		ManageFloorsetting    *bool  `json:"manageFloorsetting,omitempty"`
	}{FilterConfidenceLevel: "HIGH", ManageFloorsetting: &t}
	cfg.GCPPubSub = &struct {
		MessageRetentionDuration string `json:"messageRetentionDuration,omitempty"`
	}{MessageRetentionDuration: "604800s"}
	cfg.GCPCloudLogging = &struct {
		RetentionDays int `json:"retentionDays,omitempty"`
	}{RetentionDays: 30}
	cfg.GCPCloudRun = &struct {
		Memory       string `json:"memory,omitempty"`
		CPU          string `json:"cpu,omitempty"`
		MinInstances *int   `json:"minInstances,omitempty"`
		MaxInstances *int   `json:"maxInstances,omitempty"`
	}{Memory: "512Mi", CPU: "1", MinInstances: &one, MaxInstances: &ten}
	cfg.GCPCloudFunctions = &struct {
		Runtime    string `json:"runtime,omitempty"`
		MemorySize string `json:"memorySize,omitempty"`
		Timeout    string `json:"timeout,omitempty"`
	}{Runtime: "nodejs20", MemorySize: "256", Timeout: "60s"}
	cfg.GCPIdentityPlatform = &struct {
		SignInMethods []string `json:"signInMethods,omitempty"`
		MFARequired   *bool    `json:"mfaRequired,omitempty"`
	}{SignInMethods: []string{"EMAIL"}, MFARequired: &t}
	cfg.GCPAPIGateway = &struct {
		DomainName string `json:"domainName,omitempty"`
	}{DomainName: "api.example.com"}
	cfg.GCPLoadbalancer = &struct {
		EnableCDN *bool `json:"enable_cdn,omitempty"`
	}{EnableCDN: &t}
	cfg.GCPCloudDNS = &struct {
		DNSName          string   `json:"dnsName,omitempty"`
		CreateZone       *bool    `json:"createZone,omitempty"`
		ZoneShortName    string   `json:"zoneShortName,omitempty"`
		ZoneName         string   `json:"zoneName,omitempty"`
		PrivateZone      *bool    `json:"privateZone,omitempty"`
		NetworkSelfLinks []string `json:"networkSelfLinks,omitempty"`
		ForceDestroy     *bool    `json:"forceDestroy,omitempty"`
	}{DNSName: "example.com.", CreateZone: &t, ZoneShortName: "primary", ZoneName: "example-com", NetworkSelfLinks: []string{"projects/p/global/networks/n"}, ForceDestroy: &t}

	// AWSAppRunner (#598) — named type. Every field is emitted by the mapper
	// via the partial-config pattern (mapper.go case KeyAWSAppRunner), so a
	// full population exercises every conditional emitted tfvar key
	// (service_name … enable_www_subdomain) through the subset gate.
	port := 8080
	cfg.AWSAppRunner = &AWSAppRunnerConfig{
		ServiceName:            "kitchen-sink-svc",
		ImageRepositoryURL:     "123456789012.dkr.ecr.us-east-1.amazonaws.com/app:latest",
		ImageRepositoryType:    "ECR",
		Port:                   &port,
		EnvVars:                map[string]string{"FOO": "bar"},
		CPU:                    "1024",
		Memory:                 "2048",
		MinSize:                &one,
		MaxSize:                &ten,
		MaxConcurrency:         &ten,
		IsPubliclyAccessible:   &t,
		AutoDeploymentsEnabled: &t,
		HealthCheckProtocol:    "HTTP",
		HealthCheckPath:        "/health",
		EnableVPCConnector:     &t,
		VPCID:                  "vpc-aaa",
		SubnetIDs:              []string{"subnet-aaa"},
		CustomDomainName:       "app.example.com",
		EnableWWWSubdomain:     &t,
	}
	// AWSCodeBuild (#619) — named type. Full population exercises every
	// conditional emitted tfvar key (codebuild_project_name … security_group_ids).
	cfg.AWSCodeBuild = &AWSCodeBuildConfig{
		ProjectName:       "kitchen-sink-build",
		BuildImage:        "aws/codebuild/amazonlinux2-x86_64-standard:5.0",
		ComputeType:       "BUILD_GENERAL1_SMALL",
		SourceType:        "GITHUB",
		SourceLocation:    "https://github.com/example/repo.git",
		Buildspec:         "buildspec.yml",
		ArtifactsType:     "NO_ARTIFACTS",
		ArtifactsLocation: "",
		EnableS3Logs:      &t,
		VPCID:             "vpc-aaa",
		SubnetIDs:         []string{"subnet-aaa"},
		SecurityGroupIDs:  []string{"sg-aaa"},
	}
	// AWSBackups — inline anonymous struct. The mapper reads it only to size
	// the always-emitted default_rule; populating keeps the kitchen-sink
	// complete (see the reflection completeness guard) and exercises the
	// retention/frequency derivation.
	cfg.AWSBackups = &struct {
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
	}{
		EC2: &struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		}{FrequencyHours: 24, RetentionDays: 30, Region: "us-east-1"},
	}

	// GCPAgentEngine (#769) — inline anonymous struct. Emits display_name.
	cfg.GCPAgentEngine = &struct {
		DisplayName string `json:"displayName,omitempty"`
	}{DisplayName: "kitchen-sink-agent"}
	// GCPCloudDeploy (#613) — named type. Full population exercises
	// service_account_short_name / pipeline_short_name / targets.
	saName := "clouddeploy-runner"
	pipeName := "delivery"
	cfg.GCPCloudDeploy = &GCPCloudDeployConfig{
		ServiceAccountShortName: &saName,
		PipelineShortName:       &pipeName,
		Targets: []GCPCloudDeployTarget{
			{Name: "staging", Runtime: "run", RuntimeTarget: "staging-run", RequireApproval: &t},
			{Name: "prod", Runtime: "run", RuntimeTarget: "prod-run"},
		},
	}
	// GCPGitHubActions (#597) — named type. Full population exercises the
	// conditional keys allowed_branches / allowed_tags / allowed_pull_request /
	// deploy_roles beyond the always-emitted github_repository.
	cfg.GCPGitHubActions = &GCPGitHubActionsConfig{
		GitHubRepository:   "example/repo",
		AllowedBranches:    []string{"main"},
		AllowedTags:        []string{"v*"},
		AllowedPullRequest: &t,
		DeployRoles:        []string{"roles/run.developer"},
	}
	// GCPBackups — inline anonymous struct. Emits snapshot_retention_days.
	cfg.GCPBackups = &struct {
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
	}{
		Compute: &struct {
			FrequencyHours int `json:"frequencyHours,omitempty"`
			RetentionDays  int `json:"retentionDays,omitempty"`
		}{FrequencyHours: 24, RetentionDays: 14},
		CloudSQL: &struct {
			Enabled       *bool `json:"enabled,omitempty"`
			RetentionDays int   `json:"retentionDays,omitempty"`
		}{Enabled: &t, RetentionDays: 7},
		GCS: &struct {
			Enabled *bool `json:"enabled,omitempty"`
		}{Enabled: &t},
	}

	return cfg
}

// kitchenSinkConfigCoverageAllowlist records top-level Config *struct
// sub-fields (by json tag) that kitchenSinkConfig() intentionally leaves
// nil — i.e. a config sub-block the DefaultMapper does NOT read to emit
// any tfvar KEY, so populating it would not add subset-gate coverage.
// Shrink-only: every entry needs a justification, and
// TestMapperReadConfigSubStructsInAllowlistAreTrulyUnread keeps it honest.
// Empty today — every mapper-read sub-struct is populated.
var kitchenSinkConfigCoverageAllowlist = map[string]string{}

// TestKitchenSinkConfigCoversAllConfigSubStructs is the meta-gate that
// keeps TestMapperKeysSubsetOfModuleVariables honest. The subset gate can
// only catch a mapper-emits-undeclared-key bug for a component if the
// kitchen-sink Config actually populates that component's config
// sub-block — otherwise the `if cfg.X != nil` mapper branch never fires
// and the emitted keys are never checked (this is exactly how the historic
// `boot_disk_size_gb`/`disk_size_gb` and #253 `secret_id` bugs slipped the
// gate: their cfg sub-structs were unset). This reflection guard fails the
// moment a new `aws_*`/`gcp_*` *struct field lands on Config without a
// matching kitchenSinkConfig() population, forcing the author to extend the
// fixture (or justify an allowlist entry) so the whole #131 class stays
// mistake-proofed. Triggering example: aws_apprunner / aws_codebuild /
// gcp_agent_engine / gcp_cloud_deploy / gcp_github_actions / gcp_backups
// were all silently unexercised before this guard landed.
func TestKitchenSinkConfigCoversAllConfigSubStructs(t *testing.T) {
	cfg := kitchenSinkConfig()
	cfgVal := reflect.ValueOf(cfg).Elem()
	cfgType := cfgVal.Type()

	visited := 0
	for i := 0; i < cfgType.NumField(); i++ {
		ft := cfgType.Field(i)
		// Only top-level pointer-to-struct sub-blocks — the per-component
		// config the mapper reads. Scalars (region, cloud, estimates) and
		// value structs are out of scope.
		if ft.Type.Kind() != reflect.Pointer || ft.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		tag := jsonTagName(ft.Tag.Get("json"))
		if !strings.HasPrefix(tag, "aws_") && !strings.HasPrefix(tag, "gcp_") {
			continue
		}
		visited++
		if reason, exempt := kitchenSinkConfigCoverageAllowlist[tag]; exempt {
			t.Logf("allowlisted (mapper reads no key from it): %s (%s)", tag, reason)
			continue
		}
		assert.Falsef(t, cfgVal.Field(i).IsNil(),
			"kitchenSinkConfig() leaves Config.%s (json %q) nil, so the mapper's `if cfg.%s != nil` branch never fires under TestMapperKeysSubsetOfModuleVariables — any tfvar key that branch emits is UNCHECKED against the preset's variables.tf (the #131 fail-open class). Populate it in kitchenSinkConfig(), or, if the mapper reads no KEY from it, add %q to kitchenSinkConfigCoverageAllowlist with a justification.",
			ft.Name, tag, ft.Name, tag)
	}
	// Self-validation: a Config refactor that renamed/flattened every
	// sub-block would otherwise pass this guard vacuously. 40 is a soft
	// floor — AWS + GCP per-component config sub-blocks exceed it today.
	require.GreaterOrEqual(t, visited, 40,
		"coverage guard exercised %d Config sub-structs — expected ≥40; Config layout may have changed", visited)
}

// TestMapperReadConfigSubStructsInAllowlistAreTrulyUnread guards the
// allowlist above from silently masking real coverage gaps: an entry is
// only legitimate if the DefaultMapper source genuinely never dereferences
// that Config field. We approximate "reads it" by grepping the mapper for
// the Go field name; a hit means the sub-struct IS read and must be
// populated, not allowlisted.
func TestMapperReadConfigSubStructsInAllowlistAreTrulyUnread(t *testing.T) {
	if len(kitchenSinkConfigCoverageAllowlist) == 0 {
		return
	}
	src, err := readMapperSource()
	require.NoError(t, err)
	cfgType := reflect.TypeOf(Config{})
	tagToField := map[string]string{}
	for i := 0; i < cfgType.NumField(); i++ {
		ft := cfgType.Field(i)
		tagToField[jsonTagName(ft.Tag.Get("json"))] = ft.Name
	}
	for tag := range kitchenSinkConfigCoverageAllowlist {
		field, ok := tagToField[tag]
		require.Truef(t, ok, "allowlist tag %q is not a Config field — drop the stale entry", tag)
		assert.NotContainsf(t, src, "cfg."+field+".",
			"kitchenSinkConfigCoverageAllowlist[%q] claims the mapper reads no key from Config.%s, but mapper.go dereferences cfg.%s — populate it in kitchenSinkConfig() instead of allowlisting.",
			tag, field, field)
	}
}

func TestMapperKeysSubsetOfModuleVariables(t *testing.T) {
	m := DefaultMapper{}
	cfg := kitchenSinkConfig()
	c := newTestClient()

	varDeclRe := regexp.MustCompile(`variable\s+"([^"]+)"`)

	// Common keys DefaultMapper unconditionally sets for every component.
	// AWS modules consistently declare all three; most GCP modules don't
	// declare environment yet — that's a metadata-default mismatch the
	// composer drops, not an audit-class user-data bug. Exempt them so
	// this test stays focused on the audit class.
	commonDefaults := map[string]bool{
		"project":     true,
		"region":      true,
		"environment": true,
	}

	for _, key := range AllComponentKeys {
		t.Run(string(key), func(t *testing.T) {
			vals, err := m.BuildModuleValues(key, &Components{}, cfg, "test", "us-east-1")
			require.NoError(t, err, "mapper should not fail with the kitchen-sink config")

			presetPath := GetPresetPath(CloudFor(key), key, &Components{})
			files, err := c.GetPresetFiles(presetPath)
			require.NoError(t, err, "GetPresetFiles(%s)", presetPath)
			varsTF, ok := files["/variables.tf"]
			require.True(t, ok, "%s should have a /variables.tf", presetPath)

			declared := map[string]bool{}
			for _, m := range varDeclRe.FindAllStringSubmatch(string(varsTF), -1) {
				declared[m[1]] = true
			}

			for k := range vals {
				if commonDefaults[k] {
					continue
				}
				assert.True(t, declared[k],
					"mapper for %s emits key %q which is not declared in %s/variables.tf — declared: %v",
					key, k, presetPath, sortedKeys(declared))
			}
		})
	}
}

// TestAllComponentKeysCoversPresetKeyMap is the registry-consistency
// guard. AllComponentKeys is the source of truth for which keys back a
// preset module; PresetKeyMap is the source of truth for the preset
// directory name. Every key in PresetKeyMap (minus KeySplunk/KeyDatadog,
// which are toggles with no in-repo preset) must appear in
// AllComponentKeys, and vice versa. Adding a new component key without
// updating both lists breaks this test loudly rather than silently
// dropping the new component from the subset-check coverage.
func TestAllComponentKeysCoversPresetKeyMap(t *testing.T) {
	registry := map[ComponentKey]bool{}
	for _, k := range AllComponentKeys {
		registry[k] = true
	}

	// Keys present in PresetKeyMap but intentionally excluded from
	// AllComponentKeys (no in-repo preset; consumed elsewhere).
	exempt := map[ComponentKey]bool{
		KeySplunk:  true,
		KeyDatadog: true,
	}

	for k := range PresetKeyMap {
		if exempt[k] {
			continue
		}
		assert.True(t, registry[k],
			"PresetKeyMap[%s] is set but AllComponentKeys is missing it — every preset-backed key must be in the registry so the subset test exercises it",
			k)
	}

	// And the reverse: every registry entry must resolve to a preset
	// path via PresetKeyMap. Issue #224 removed the previous
	// KeyAWSEKSControlPlane carve-out alongside the polymorphic-key collapse.
	for _, k := range AllComponentKeys {
		_, inMap := PresetKeyMap[k]
		if !inMap {
			t.Errorf("AllComponentKeys[%s] is registered but has no PresetKeyMap entry", k)
		}
	}
}
