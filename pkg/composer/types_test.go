package composer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComponents_Normalize_EmptyIsNoOp locks in the invariant that an
// empty Components.Normalize() must not invent a cloud and must not
// populate any cloud-scoped field. Catches the regression "silently
// default empty sessions to AWS" by checking both the Cloud string and
// the prefixed field maps stay zero.
func TestComponents_Normalize_EmptyIsNoOp(t *testing.T) {
	t.Parallel()
	c := Components{}
	c.Normalize()

	if c.Cloud != "" {
		t.Errorf("Cloud must remain empty for an empty session, got %q", c.Cloud)
	}
	if c.AWSVPC != "" || c.AWSEC2 != "" {
		t.Errorf("AWS fields must remain zero, got AWSVPC=%q AWSEC2=%q", c.AWSVPC, c.AWSEC2)
	}
	if c.GCPVPC != nil || c.GCPGKE != nil {
		t.Errorf("GCP fields must remain nil, got GCPVPC=%v GCPGKE=%v", c.GCPVPC, c.GCPGKE)
	}
	if c.VPC != "" || c.EC2 != "" {
		t.Errorf("legacy fields must remain zero, got VPC=%q EC2=%q", c.VPC, c.EC2)
	}
}

func TestComponents_Normalize_ClearsGCPFieldsForAWS(t *testing.T) {
	t.Parallel()
	// When cloud is AWS, all GCP fields should be cleared to nil
	c := Components{
		Cloud:       "AWS",
		GCPVPC:      boolPtr(true),
		GCPGKE:      boolPtr(true),
		GCPCloudSQL: boolPtr(true),
		GCPCloudKMS: boolPtr(true),
	}
	c.Normalize()

	if c.GCPVPC != nil {
		t.Errorf("GCPVPC should be nil, got %v", c.GCPVPC)
	}
	if c.GCPGKE != nil {
		t.Errorf("GCPGKE should be nil, got %v", c.GCPGKE)
	}
	if c.GCPCloudSQL != nil {
		t.Errorf("GCPCloudSQL should be nil, got %v", c.GCPCloudSQL)
	}
	if c.GCPCloudKMS != nil {
		t.Errorf("GCPCloudKMS should be nil, got %v", c.GCPCloudKMS)
	}
}

func TestComponents_Normalize_ClearsAWSFieldsForGCP(t *testing.T) {
	t.Parallel()
	// When cloud is GCP, all AWS fields should be cleared
	c := Components{
		Cloud:   "GCP",
		AWSVPC:  "VPC",
		AWSEKS:  boolPtr(true),
		AWSRDS:  boolPtr(true),
		AWSS3:   boolPtr(true),
	}
	c.Normalize()

	if c.AWSVPC != "" {
		t.Errorf("AWSVPC should be empty, got %q", c.AWSVPC)
	}
	if c.AWSEKS != nil {
		t.Errorf("AWSEKS should be nil, got %v", c.AWSEKS)
	}
	if c.AWSRDS != nil {
		t.Errorf("AWSRDS should be nil, got %v", c.AWSRDS)
	}
	if c.AWSS3 != nil {
		t.Errorf("AWSS3 should be nil, got %v", c.AWSS3)
	}
}

func TestComponents_Normalize_PreservesExplicitCloud(t *testing.T) {
	t.Parallel()
	// If cloud is explicitly set, it should be preserved
	c := Components{Cloud: "AWS"}
	c.Normalize()
	if c.Cloud != "AWS" {
		t.Errorf("Cloud should remain 'AWS', got %q", c.Cloud)
	}

	c = Components{Cloud: "GCP"}
	c.Normalize()
	if c.Cloud != "GCP" {
		t.Errorf("Cloud should remain 'GCP', got %q", c.Cloud)
	}
}

func TestComponents_Normalize_EmptySessionJSON(t *testing.T) {
	t.Parallel()
	// An empty session should serialize to minimal JSON
	c := Components{}
	c.Normalize()

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if _, exists := m["cloud"]; exists {
		t.Errorf("Empty session should NOT have 'cloud' in JSON, got %v", m["cloud"])
	}
}

func TestComponents_Normalize_SyncsLegacyFieldsForAWS(t *testing.T) {
	t.Parallel()
	// When cloud is AWS, legacy fields should be synced with AWS-prefixed fields
	c := Components{
		Cloud: "AWS",
		VPC:   "VPC",
		EC2:   "Intel",
	}
	c.Normalize()

	if c.AWSVPC != "VPC" {
		t.Errorf("AWSVPC should be 'VPC', got %q", c.AWSVPC)
	}
	if c.AWSEC2 != "Intel" {
		t.Errorf("AWSEC2 should be 'Intel', got %q", c.AWSEC2)
	}
}

// TestComponents_Normalize_SyncsLegacyBoolFieldsForAWS pins the AWS-branch
// promotion of every legacy *bool field composer helpers now rely on. If a
// future refactor drops one of these ↔ syncs, the corresponding composer
// helper (stackNeedsPrivateSubnets, IsLambdaArchitecture, ...) silently
// misreports — this test catches that at the Normalize layer.
func TestComponents_Normalize_SyncsLegacyBoolFieldsForAWS(t *testing.T) {
	t.Parallel()
	boolPtr := func(v bool) *bool { return &v }

	cases := []struct {
		name     string
		setup    func(*Components)
		assertOn func(t *testing.T, c *Components)
	}{
		{"Postgres → AWSRDS", func(c *Components) { c.Postgres = boolPtr(true) },
			func(t *testing.T, c *Components) {
				require.NotNil(t, c.AWSRDS, "AWSRDS should be non-nil after Normalize")
				assert.True(t, *c.AWSRDS)
			}},
		{"ElastiCache → AWSElastiCache", func(c *Components) { c.ElastiCache = boolPtr(true) },
			func(t *testing.T, c *Components) {
				require.NotNil(t, c.AWSElastiCache)
				assert.True(t, *c.AWSElastiCache)
			}},
		{"OpenSearch → AWSOpenSearch", func(c *Components) { c.OpenSearch = boolPtr(true) },
			func(t *testing.T, c *Components) {
				require.NotNil(t, c.AWSOpenSearch)
				assert.True(t, *c.AWSOpenSearch)
			}},
		{"Lambda (*bool) → AWSLambda", func(c *Components) { c.Lambda = boolPtr(true) },
			func(t *testing.T, c *Components) {
				require.NotNil(t, c.AWSLambda)
				assert.True(t, *c.AWSLambda)
			}},
		{"Resource \"Lambda\" → AWSLambda", func(c *Components) { c.Resource = "Lambda" },
			func(t *testing.T, c *Components) {
				require.NotNil(t, c.AWSLambda, "Normalize must promote legacy Resource=\"Lambda\" to AWSLambda")
				assert.True(t, *c.AWSLambda)
			}},
		{"Resource \"Serverless\" → AWSLambda", func(c *Components) { c.Resource = "Serverless" },
			func(t *testing.T, c *Components) {
				require.NotNil(t, c.AWSLambda)
				assert.True(t, *c.AWSLambda)
			}},
		{"Resource \"Kubernetes\" leaves AWSLambda unset", func(c *Components) { c.Resource = "Kubernetes" },
			func(t *testing.T, c *Components) {
				assert.Nil(t, c.AWSLambda,
					"Resource=\"Kubernetes\" must NOT promote to AWSLambda (EKS ≠ Lambda)")
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &Components{Cloud: "AWS"}
			tc.setup(c)
			c.Normalize()
			tc.assertOn(t, c)
		})
	}
}

func TestComponents_Normalize_ClearsLegacyFieldsAfterSync(t *testing.T) {
	t.Parallel()
	// After normalization, legacy fields should be cleared to prevent serialization
	c := Components{
		Cloud:  "AWS",
		AWSVPC: "VPC",
		AWSEC2: "Intel",
	}
	c.Normalize()

	// Legacy fields should be empty after normalization
	if c.VPC != "" {
		t.Errorf("VPC should be empty after normalization, got %q", c.VPC)
	}
	if c.EC2 != "" {
		t.Errorf("EC2 should be empty after normalization, got %q", c.EC2)
	}
	// AWS-prefixed fields should be preserved
	if c.AWSVPC != "VPC" {
		t.Errorf("AWSVPC should be 'VPC', got %q", c.AWSVPC)
	}
	if c.AWSEC2 != "Intel" {
		t.Errorf("AWSEC2 should be 'Intel', got %q", c.AWSEC2)
	}
}

func TestConfig_Normalize_EmptySession(t *testing.T) {
	t.Parallel()
	// A fresh config with no cloud set must not invent one.
	cfg := Config{}
	cfg.Normalize()

	if cfg.Cloud != "" {
		t.Errorf("Config.Cloud must remain empty after Normalize() on an empty session, got %q", cfg.Cloud)
	}
}

func TestConfig_Normalize_ClearsGCPFieldsForAWS(t *testing.T) {
	t.Parallel()
	// When cloud is AWS, GCP config fields should be cleared
	cfg := Config{
		Cloud: "AWS",
		GCPGKE: &struct {
			Regional    *bool  `json:"regional,omitempty"`
			NodeCount   string `json:"nodeCount,omitempty"`
			MachineType string `json:"machineType,omitempty"`
		}{
			NodeCount: "3",
		},
	}
	cfg.Normalize()

	if cfg.GCPGKE != nil {
		t.Errorf("GCPGKE should be nil for AWS cloud, got %v", cfg.GCPGKE)
	}
}

func TestConfig_Normalize_ClearsAWSFieldsForGCP(t *testing.T) {
	t.Parallel()
	// When cloud is GCP, AWS config fields should be cleared
	cfg := Config{
		Cloud: "GCP",
		AWSEKS: &struct {
			HaControlPlane         *bool  `json:"haControlPlane,omitempty"`
			ControlPlaneVisibility string `json:"controlPlaneVisibility,omitempty"`
			DesiredSize            string `json:"desiredSize,omitempty"`
			MaxSize                string `json:"maxSize,omitempty"`
			MinSize                string `json:"minSize,omitempty"`
			InstanceType           string `json:"instanceType,omitempty"`
		}{
			DesiredSize: "3",
		},
	}
	cfg.Normalize()

	if cfg.AWSEKS != nil {
		t.Errorf("AWSEKS should be nil for GCP cloud, got %v", cfg.AWSEKS)
	}
}

// Helper function
func boolPtr(b bool) *bool {
	return &b
}

// TestConfig_Normalize_PromotesLegacyAWSSubConfigs pins the AWS-branch
// legacy → AWS-prefixed migration for the Config sub-configs that Phase 3b
// added: DynamoDB, MSK, APIGateway, KMS, SecretsManager, OpenSearch,
// Bedrock, Lambda, and Backups.Details. Composer's mapper (post-Phase 3b)
// reads only the AWS-prefixed fields, so these migrations are load-bearing
// for direct Go callers constructing Config from legacy JSON. Reliable's
// composeradapter emits prefixed-only Config in production, so this test
// is primarily a contract guard for direct library consumers.
func TestConfig_Normalize_PromotesLegacyAWSSubConfigs(t *testing.T) {
	t.Parallel()

	t.Run("DynamoDB", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", DynamoDB: &struct {
			Type string `json:"type,omitempty"`
		}{Type: "On Demand"}}
		cfg.Normalize()
		if cfg.AWSDynamoDB == nil || cfg.AWSDynamoDB.Type != "On Demand" {
			t.Fatalf("AWSDynamoDB.Type not promoted; got %#v", cfg.AWSDynamoDB)
		}
	})

	t.Run("MSK", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", MSK: &struct {
			Retention string `json:"retentionPeriod,omitempty"`
		}{Retention: "168"}}
		cfg.Normalize()
		if cfg.AWSMSK == nil || cfg.AWSMSK.Retention != "168" {
			t.Fatalf("AWSMSK.Retention not promoted; got %#v", cfg.AWSMSK)
		}
	})

	t.Run("APIGateway", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", APIGateway: &struct {
			DomainName     string `json:"domainName,omitempty"`
			CertificateArn string `json:"certificateArn,omitempty"`
		}{DomainName: "api.example.com", CertificateArn: "arn:aws:acm:us-east-1:123456789012:certificate/abc"}}
		cfg.Normalize()
		if cfg.AWSAPIGateway == nil ||
			cfg.AWSAPIGateway.DomainName != "api.example.com" ||
			cfg.AWSAPIGateway.CertificateArn != "arn:aws:acm:us-east-1:123456789012:certificate/abc" {
			t.Fatalf("AWSAPIGateway fields not promoted; got %#v", cfg.AWSAPIGateway)
		}
	})

	t.Run("KMS", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", KMS: &struct {
			NumKeys string `json:"numKeys,omitempty"`
		}{NumKeys: "3"}}
		cfg.Normalize()
		if cfg.AWSKMS == nil || cfg.AWSKMS.NumKeys != "3" {
			t.Fatalf("AWSKMS.NumKeys not promoted; got %#v", cfg.AWSKMS)
		}
	})

	t.Run("SecretsManager", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", SecretsManager: &struct {
			NumSecrets string `json:"numSecrets,omitempty"`
		}{NumSecrets: "5"}}
		cfg.Normalize()
		if cfg.AWSSecretsManager == nil || cfg.AWSSecretsManager.NumSecrets != "5" {
			t.Fatalf("AWSSecretsManager.NumSecrets not promoted; got %#v", cfg.AWSSecretsManager)
		}
	})

	t.Run("OpenSearch", func(t *testing.T) {
		multi := true
		cfg := Config{Cloud: "AWS", OpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "serverless", InstanceType: "t3.medium.search", StorageSize: "50", MultiAZ: &multi}}
		cfg.Normalize()
		if cfg.AWSOpenSearch == nil ||
			cfg.AWSOpenSearch.DeploymentType != "serverless" ||
			cfg.AWSOpenSearch.InstanceType != "t3.medium.search" ||
			cfg.AWSOpenSearch.StorageSize != "50" ||
			cfg.AWSOpenSearch.MultiAZ == nil || !*cfg.AWSOpenSearch.MultiAZ {
			t.Fatalf("AWSOpenSearch fields not promoted; got %#v", cfg.AWSOpenSearch)
		}
	})

	t.Run("Bedrock", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", Bedrock: &struct {
			KnowledgeBaseName string `json:"knowledgeBaseName,omitempty"`
			ModelID           string `json:"modelId,omitempty"`
			EmbeddingModelID  string `json:"embeddingModelId,omitempty"`
		}{
			KnowledgeBaseName: "kb-test",
			ModelID:           "anthropic.claude-3-sonnet-20240229-v1:0",
			EmbeddingModelID:  "amazon.titan-embed-text-v1",
		}}
		cfg.Normalize()
		if cfg.AWSBedrock == nil ||
			cfg.AWSBedrock.KnowledgeBaseName != "kb-test" ||
			cfg.AWSBedrock.ModelID != "anthropic.claude-3-sonnet-20240229-v1:0" ||
			cfg.AWSBedrock.EmbeddingModelID != "amazon.titan-embed-text-v1" {
			t.Fatalf("AWSBedrock fields not promoted; got %#v", cfg.AWSBedrock)
		}
	})

	t.Run("Lambda", func(t *testing.T) {
		cfg := Config{Cloud: "AWS", Lambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Runtime: "python3.12", MemorySize: "512", Timeout: "30s"}}
		cfg.Normalize()
		if cfg.AWSLambda == nil ||
			cfg.AWSLambda.Runtime != "python3.12" ||
			cfg.AWSLambda.MemorySize != "512" ||
			cfg.AWSLambda.Timeout != "30s" {
			t.Fatalf("AWSLambda fields not promoted; got %#v", cfg.AWSLambda)
		}
	})

	t.Run("Backups.Details map to AWSBackups typed sub-structs", func(t *testing.T) {
		// Legacy shape: Backups.Details is a service-keyed map. Post-Normalize
		// each known service key must land on the corresponding typed
		// sub-struct (AWSBackups.EC2, RDS, ElastiCache, DynamoDB, S3).
		type det = struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		}
		cfg := Config{Cloud: "AWS", Backups: &struct {
			Details map[string]det `json:"details,omitempty"`
		}{Details: map[string]det{
			"ec2":         {FrequencyHours: 1, RetentionDays: 7, Region: "us-east-1"},
			"rds":         {FrequencyHours: 4, RetentionDays: 14},
			"elasticache": {FrequencyHours: 24, RetentionDays: 30},
			"dynamodb":    {FrequencyHours: 24, RetentionDays: 90},
			"s3":          {FrequencyHours: 24, RetentionDays: 365},
		}}}
		cfg.Normalize()

		if cfg.AWSBackups == nil {
			t.Fatalf("AWSBackups not promoted from Backups.Details map")
		}
		if cfg.AWSBackups.EC2 == nil || cfg.AWSBackups.EC2.FrequencyHours != 1 || cfg.AWSBackups.EC2.RetentionDays != 7 || cfg.AWSBackups.EC2.Region != "us-east-1" {
			t.Errorf("AWSBackups.EC2 mis-promoted: %#v", cfg.AWSBackups.EC2)
		}
		if cfg.AWSBackups.RDS == nil || cfg.AWSBackups.RDS.FrequencyHours != 4 || cfg.AWSBackups.RDS.RetentionDays != 14 {
			t.Errorf("AWSBackups.RDS mis-promoted: %#v", cfg.AWSBackups.RDS)
		}
		if cfg.AWSBackups.ElastiCache == nil || cfg.AWSBackups.ElastiCache.RetentionDays != 30 {
			t.Errorf("AWSBackups.ElastiCache mis-promoted: %#v", cfg.AWSBackups.ElastiCache)
		}
		if cfg.AWSBackups.DynamoDB == nil || cfg.AWSBackups.DynamoDB.RetentionDays != 90 {
			t.Errorf("AWSBackups.DynamoDB mis-promoted: %#v", cfg.AWSBackups.DynamoDB)
		}
		if cfg.AWSBackups.S3 == nil || cfg.AWSBackups.S3.RetentionDays != 365 {
			t.Errorf("AWSBackups.S3 mis-promoted: %#v", cfg.AWSBackups.S3)
		}
	})

	t.Run("prefixed wins when both legacy and AWS fields set", func(t *testing.T) {
		// If a caller supplies both halves, don't overwrite the AWS side.
		cfg := Config{
			Cloud:          "AWS",
			KMS:            &struct{ NumKeys string `json:"numKeys,omitempty"` }{NumKeys: "1"},
			AWSKMS:         &struct{ NumKeys string `json:"numKeys,omitempty"` }{NumKeys: "99"},
		}
		cfg.Normalize()
		if cfg.AWSKMS.NumKeys != "99" {
			t.Errorf("pre-set AWSKMS.NumKeys must not be overwritten by legacy KMS; got %q", cfg.AWSKMS.NumKeys)
		}
	})

	t.Run("legacy fields are cleared after promotion", func(t *testing.T) {
		// The post-sync block clears every legacy field; this test pins that
		// the new migrations don't leave legacy state behind.
		cfg := Config{Cloud: "AWS",
			DynamoDB:       &struct{ Type string `json:"type,omitempty"` }{Type: "Provisioned"},
			Lambda:         &struct {
				Runtime    string `json:"runtime,omitempty"`
				MemorySize string `json:"memorySize,omitempty"`
				Timeout    string `json:"timeout,omitempty"`
			}{Runtime: "nodejs20.x"},
		}
		cfg.Normalize()
		if cfg.DynamoDB != nil {
			t.Errorf("legacy DynamoDB should be cleared; got %#v", cfg.DynamoDB)
		}
		if cfg.Lambda != nil {
			t.Errorf("legacy Lambda should be cleared; got %#v", cfg.Lambda)
		}
	})
}
