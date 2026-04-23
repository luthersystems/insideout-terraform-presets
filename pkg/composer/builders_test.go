package composer

// Test builders for common Components / Config shapes.
//
// Config's sub-configs (AWSEC2, AWSECS, Cloudfront, RDS, SQS, …) are declared
// as anonymous structs on the Config struct, so every inline literal has to
// redeclare the full anonymous type — which hides the one or two fields the
// test actually cares about under ten lines of type boilerplate. The helpers
// below absorb that boilerplate so test sites read as data, not schema.

// awsEC2CfgInput mirrors the subset of Config.AWSEC2 fields exercised by
// mapper tests.
type awsEC2CfgInput struct {
	InstanceType          string
	NumServers            string
	NumCoresPerServer     string
	DiskSizePerServer     string
	UserData              string
	UserDataURL           string
	CustomIngressPorts    []int
	SSHPublicKey          string
	EnableInstanceConnect *bool
}

// configWithAWSEC2 returns *Config with AWSEC2 populated from input, eliding
// the ten-line anonymous-struct redeclaration at each call site.
func configWithAWSEC2(in awsEC2CfgInput) *Config {
	return &Config{
		AWSEC2: &struct {
			InstanceType          string `json:"instanceType,omitempty"`
			NumServers            string `json:"numServers,omitempty"`
			NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
			DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
			UserData              string `json:"userData,omitempty"`
			UserDataURL           string `json:"userDataURL,omitempty"`
			CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
			SSHPublicKey          string `json:"sshPublicKey,omitempty"`
			EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
		}{
			InstanceType:          in.InstanceType,
			NumServers:            in.NumServers,
			NumCoresPerServer:     in.NumCoresPerServer,
			DiskSizePerServer:     in.DiskSizePerServer,
			UserData:              in.UserData,
			UserDataURL:           in.UserDataURL,
			CustomIngressPorts:    in.CustomIngressPorts,
			SSHPublicKey:          in.SSHPublicKey,
			EnableInstanceConnect: in.EnableInstanceConnect,
		},
	}
}

// awsECSCfgInput mirrors Config.AWSECS.
type awsECSCfgInput struct {
	EnableContainerInsights *bool
	CapacityProviders       []string
	DefaultCapacityProvider string
	EnableServiceConnect    *bool
}

// configWithAWSECS returns *Config with AWSECS populated from input.
func configWithAWSECS(in awsECSCfgInput) *Config {
	return &Config{
		AWSECS: &struct {
			EnableContainerInsights *bool    `json:"enableContainerInsights,omitempty"`
			CapacityProviders       []string `json:"capacityProviders,omitempty"`
			DefaultCapacityProvider string   `json:"defaultCapacityProvider,omitempty"`
			EnableServiceConnect    *bool    `json:"enableServiceConnect,omitempty"`
		}{
			EnableContainerInsights: in.EnableContainerInsights,
			CapacityProviders:       in.CapacityProviders,
			DefaultCapacityProvider: in.DefaultCapacityProvider,
			EnableServiceConnect:    in.EnableServiceConnect,
		},
	}
}

// awsKitchenSinkCfgBase returns the Config fields shared by the two composer
// kitchen-sink tests (legacy- and V2-key variants). Fields use the legacy
// (un-prefixed) Config names because Config.Normalize() promotes them to
// the cloud-prefixed equivalents during compose.
//
// The split into Base / WithReadReplicas / V2 (instead of a single shared
// builder) preserves a subtle fidelity invariant: the V2 variant leaves
// RDS.ReadReplicas unset, exercising the "unset" branch of the RDS mapper,
// while the WithReadReplicas variant sets it to "2" so both mapper branches
// are exercised. Collapsing into one shared helper would silently couple the
// two tests on that branch.
func awsKitchenSinkCfgBase() *Config {
	return &Config{
		Region: "us-west-2",
		Cloudfront: &struct {
			DefaultTtl *string `json:"defaultTtl,omitempty"`
			OriginPath *string `json:"originPath,omitempty"`
			CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
		}{DefaultTtl: ptrString("3600")},
		SQS: &struct {
			Type              string `json:"type,omitempty"`
			VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
		}{Type: "FIFO", VisibilityTimeout: "600"},
		CloudWatchLogs: &struct {
			RetentionDays int `json:"retentionDays,omitempty"`
		}{RetentionDays: 90},
	}
}

// awsKitchenSinkCfgV2 returns the Config for TestComposeStack_V2KitchenSink.
// RDS.ReadReplicas is deliberately unset — the test exercises the default
// (no-read-replicas) mapper branch for that field.
func awsKitchenSinkCfgV2() *Config {
	cfg := awsKitchenSinkCfgBase()
	cfg.RDS = &struct {
		CPUSize      string `json:"cpuSize,omitempty"`
		ReadReplicas string `json:"readReplicas,omitempty"`
		StorageSize  string `json:"storageSize,omitempty"`
	}{CPUSize: "db.m7i.2xlarge", StorageSize: "20"}
	return cfg
}

// awsKitchenSinkCfgWithReadReplicas returns the Config for
// TestComposeStack_KitchenSink. RDS.ReadReplicas is "2" to exercise the
// read-replicas mapper branch (cfg.RDS.ReadReplicas != "" in mapper.go).
func awsKitchenSinkCfgWithReadReplicas() *Config {
	cfg := awsKitchenSinkCfgBase()
	cfg.RDS = &struct {
		CPUSize      string `json:"cpuSize,omitempty"`
		ReadReplicas string `json:"readReplicas,omitempty"`
		StorageSize  string `json:"storageSize,omitempty"`
	}{CPUSize: "db.m7i.2xlarge", ReadReplicas: "2", StorageSize: "20"}
	return cfg
}

// awsKitchenSinkCompsV2 returns the Components shape for both kitchen-sink
// tests: AWSElastiCache toggle plus AWSBackups (prefixed). EC2 and RDS are
// the only backup targets enabled; DynamoDB and S3 are left unset so
// boolVal(nil) falls through to false, matching the wiring/backups subtest
// assertions in both kitchen-sink tests.
func awsKitchenSinkCompsV2() *Components {
	return &Components{
		AWSElastiCache: ptrBool(true),
		AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{
			EC2: ptrBool(true),
			RDS: ptrBool(true),
		},
	}
}
