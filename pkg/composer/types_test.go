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
