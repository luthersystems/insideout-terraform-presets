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
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	cfg.GCPCompute = &struct {
		NumServers  string `json:"numServers,omitempty"`
		MachineType string `json:"machineType,omitempty"`
		DiskSizeGb  int    `json:"diskSizeGb,omitempty"`
	}{NumServers: "1", MachineType: "e2-medium", DiskSizeGb: 50}
	cfg.GCPGKE = &struct {
		Regional    *bool  `json:"regional,omitempty"`
		NodeCount   string `json:"nodeCount,omitempty"`
		MachineType string `json:"machineType,omitempty"`
	}{Regional: &t, NodeCount: "3", MachineType: "e2-standard-2"}
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
		EnableVectorSearch *bool `json:"enableVectorSearch,omitempty"`
		IndexDimensions    int   `json:"indexDimensions,omitempty"`
	}{EnableVectorSearch: &t, IndexDimensions: 768}
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

	return cfg
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
