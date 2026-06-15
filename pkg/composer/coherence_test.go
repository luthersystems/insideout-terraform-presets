package composer

// coherence_test.go locks the (Components, Config) coherence rules upstream.
// Ports the pure-function regression suite that originated in
// luthersystems/reliable's chatv2 + agentapi packages (#1435) so the same
// invariant cannot regress on either side.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(i int) *int { return &i }

// awsVPCSubFields mirrors the anonymous struct shape on Config.AWSVPC.
// Mirrors the literal used in mapper_test.go's cfgWithAWSVPC helper.
func cfgWithVPCSubBlock(single, enable *bool, az *int) *Config {
	c := &Config{}
	c.AWSVPC = &struct {
		SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
		EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
		AZCount          *int  `json:"azCount,omitempty"`
	}{SingleNATGateway: single, EnableNATGateway: enable, AZCount: az}
	return c
}

// ──────────────────────────────────────────────────────────────────────────
// ComponentSelected
// ──────────────────────────────────────────────────────────────────────────

func TestComponentSelected_StringFields(t *testing.T) {
	t.Parallel()

	assert.False(t, ComponentSelected(&Components{}, KeyAWSVPC))
	assert.True(t, ComponentSelected(&Components{AWSVPC: "Public VPC"}, KeyAWSVPC))

	assert.False(t, ComponentSelected(&Components{}, KeyAWSEC2))
	assert.True(t, ComponentSelected(&Components{AWSEC2: "Intel"}, KeyAWSEC2))

	assert.False(t, ComponentSelected(&Components{}, KeyGCPCompute))
	assert.True(t, ComponentSelected(&Components{GCPCompute: "n2-standard-2"}, KeyGCPCompute))
}

func TestComponentSelected_PointerFields_RequireTrue(t *testing.T) {
	t.Parallel()

	// nil pointer → not selected.
	assert.False(t, ComponentSelected(&Components{}, KeyAWSOpenSearch))

	// explicit &false → not selected (so cfg.AWSOpenSearch is stripped).
	assert.False(t, ComponentSelected(&Components{AWSOpenSearch: boolPtr(false)}, KeyAWSOpenSearch),
		"explicit &false must be treated as deselected so its config sub-block is stripped")

	// &true → selected.
	assert.True(t, ComponentSelected(&Components{AWSOpenSearch: boolPtr(true)}, KeyAWSOpenSearch))
}

func TestComponentSelected_BackupsPointer_NonNilEqualsSelected(t *testing.T) {
	t.Parallel()

	// AWSBackups is a *struct{...}; its presence alone signals selection.
	c := &Components{}
	assert.False(t, ComponentSelected(c, KeyAWSBackups))

	c.AWSBackups = &struct {
		EC2         *bool `json:"aws_ec2,omitempty"`
		RDS         *bool `json:"aws_rds,omitempty"`
		ElastiCache *bool `json:"aws_elasticache,omitempty"`
		DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
		S3          *bool `json:"aws_s3,omitempty"`
	}{}
	assert.True(t, ComponentSelected(c, KeyAWSBackups))
}

func TestComponentSelected_NilComponents_AlwaysFalse(t *testing.T) {
	t.Parallel()

	for _, key := range []ComponentKey{
		KeyAWSVPC, KeyAWSEC2, KeyAWSOpenSearch, KeyGCPCompute, KeyGCPBackups,
	} {
		assert.False(t, ComponentSelected(nil, key), "nil components must report not-selected for %s", key)
	}
}

func TestComponentSelected_NodeGroupAndNonConfigKeys_AlwaysFalse(t *testing.T) {
	t.Parallel()

	// KeyAWSEKSNodeGroup doesn't map to a Components field — selection is
	// encoded by AWSEKS (and auto-include of the node group is driven by
	// ResolveDependenciesForCompose). ComponentSelected must NOT report it
	// as a standalone selection (downstream code would treat it as
	// orphan-strippable and misbehave).
	c := &Components{AWSEKS: boolPtr(true), AWSLambda: boolPtr(true)}
	assert.False(t, ComponentSelected(c, KeyAWSEKSNodeGroup))

	// KeyComposer / KeyArch / KeyCloud are meta-keys, not components.
	assert.False(t, ComponentSelected(c, KeyComposer))
	assert.False(t, ComponentSelected(c, KeyArch))
	assert.False(t, ComponentSelected(c, KeyCloud))
}

// ──────────────────────────────────────────────────────────────────────────
// StackNeedsPrivateSubnets — exported wrapper around the mapper helper.
// ──────────────────────────────────────────────────────────────────────────

func TestStackNeedsPrivateSubnets_Exported(t *testing.T) {
	t.Parallel()

	assert.False(t, StackNeedsPrivateSubnets(nil))
	assert.False(t, StackNeedsPrivateSubnets(&Components{}))

	for _, c := range []*Components{
		{AWSEKS: boolPtr(true)},
		{AWSECS: boolPtr(true)},
		{AWSRDS: boolPtr(true)},
		{AWSElastiCache: boolPtr(true)},
		{AWSOpenSearch: boolPtr(true)},
		{AWSEC2: "Intel"},
		{AWSEC2: "ARM"},
	} {
		assert.True(t, StackNeedsPrivateSubnets(c), "%+v should need private subnets", c)
	}

	// Components that DON'T need private subnets:
	assert.False(t, StackNeedsPrivateSubnets(&Components{AWSLambda: boolPtr(true)}))
	assert.False(t, StackNeedsPrivateSubnets(&Components{AWSS3: boolPtr(true), AWSKMS: boolPtr(true)}))
}

// ──────────────────────────────────────────────────────────────────────────
// StripOrphanConfig
// ──────────────────────────────────────────────────────────────────────────

// TestStripOrphanConfig_ClearsUnselectedSubBlocks ports the failure shape
// from sess_v2_CnqUJ6NRJnLC: a config.aws_opensearch sub-block survives
// after aws_opensearch is removed from components. Strip must clear it.
func TestStripOrphanConfig_ClearsUnselectedSubBlocks(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Cloud: "AWS",
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "Production", InstanceType: "m6g.large"},
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Runtime: "nodejs20.x", Timeout: "30s"},
	}
	comps := &Components{
		Cloud:     "AWS",
		AWSVPC:    "Public VPC",
		AWSLambda: boolPtr(true),
		// AWSOpenSearch intentionally omitted.
	}

	StripOrphanConfig(comps, cfg)

	assert.Nil(t, cfg.AWSOpenSearch, "orphan opensearch sub-block must be cleared")
	assert.NotNil(t, cfg.AWSLambda, "selected Lambda sub-block must survive")
	assert.Equal(t, "nodejs20.x", cfg.AWSLambda.Runtime, "user values on selected components must survive")
}

// TestStripOrphanConfig_EmptyAllocatedSubBlockIsOrphan locks the "non-nil-
// but-empty" rule: an &AWSOpenSearch{} sub-block (no actual configuration)
// must NOT be a signal that opensearch is selected. The Components blob is
// the canonical selection witness.
func TestStripOrphanConfig_EmptyAllocatedSubBlockIsOrphan(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{}, // allocated but empty
	}
	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC"}

	StripOrphanConfig(comps, cfg)

	assert.Nil(t, cfg.AWSOpenSearch,
		"an allocated-but-empty sub-block must be treated as orphan when its component is unselected")
}

// TestStripOrphanConfig_ExplicitFalsePointerStripsConfig captures the
// "explicit aws_opensearch=false" case: the user (or a downstream rule)
// signalled deselection by writing &false to the components blob. The
// config sub-block must be cleared.
func TestStripOrphanConfig_ExplicitFalsePointerStripsConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "Production"},
	}
	comps := &Components{
		Cloud:         "AWS",
		AWSVPC:        "Public VPC",
		AWSOpenSearch: boolPtr(false), // explicit deselection
	}

	StripOrphanConfig(comps, cfg)

	assert.Nil(t, cfg.AWSOpenSearch,
		"AWSOpenSearch=&false must trigger strip — explicit deselection is still deselection")
}

// TestStripOrphanConfig_LeavesNonStrippableUntouched checks that fields
// outside the orphan-strippable set (Region, Cloud, EstimatedMonthlyRequests)
// survive the strip even when other components are missing.
func TestStripOrphanConfig_LeavesNonStrippableUntouched(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Cloud:                    "AWS",
		Region:                   "us-east-2",
		EstimatedMonthlyRequests: 100_000,
		EstimatedAvgDurationMs:   250,
	}
	comps := &Components{Cloud: "AWS"} // nothing selected

	StripOrphanConfig(comps, cfg)

	assert.Equal(t, "AWS", cfg.Cloud)
	assert.Equal(t, "us-east-2", cfg.Region)
	assert.Equal(t, int64(100_000), cfg.EstimatedMonthlyRequests)
	assert.Equal(t, 250, cfg.EstimatedAvgDurationMs)
}

// TestStripOrphanConfig_AWSBackupsRespectsSelection ports the
// Components.AWSBackups != nil convention: when AWSBackups is selected, the
// cfg.AWSBackups sub-block must survive even if some inner backup configs
// are zero.
func TestStripOrphanConfig_AWSBackupsRespectsSelection(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSBackups: &struct {
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
		}{},
	}
	comps := &Components{
		Cloud: "AWS",
		AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{S3: boolPtr(true)},
	}

	StripOrphanConfig(comps, cfg)

	assert.NotNil(t, cfg.AWSBackups, "cfg.AWSBackups must survive when comps.AWSBackups is non-nil")
}

// TestStripOrphanConfig_AWSBackupsCleared captures the orphan path: comps
// has no AWSBackups, so cfg.AWSBackups is stripped.
func TestStripOrphanConfig_AWSBackupsCleared(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSBackups: &struct {
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
		}{},
	}
	comps := &Components{Cloud: "AWS"} // no AWSBackups

	StripOrphanConfig(comps, cfg)

	assert.Nil(t, cfg.AWSBackups, "orphan AWSBackups sub-block must be cleared when comps.AWSBackups is nil")
}

// TestStripOrphanConfig_CrossCloudResidual locks the rule that a switch to
// the opposite cloud strips the previous cloud's sub-blocks. Mirrors the
// reliable-side TestUnion_Issue1435_OrphanConfig_CrossCloudResidual.
func TestStripOrphanConfig_CrossCloudResidual(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Cloud: "GCP",
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Runtime: "nodejs20.x"},
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{},
		GCPCloudRun: &struct {
			Memory       string `json:"memory,omitempty"`
			CPU          string `json:"cpu,omitempty"`
			MinInstances *int   `json:"minInstances,omitempty"`
			MaxInstances *int   `json:"maxInstances,omitempty"`
		}{Memory: "512Mi"},
	}
	comps := &Components{
		Cloud:       "GCP",
		GCPVPC:      boolPtr(true),
		GCPCloudRun: boolPtr(true),
	}

	StripOrphanConfig(comps, cfg)

	assert.Nil(t, cfg.AWSLambda, "AWS Lambda sub-block must be stripped on a GCP stack")
	assert.Nil(t, cfg.AWSOpenSearch, "AWS OpenSearch sub-block must be stripped on a GCP stack")
	assert.NotNil(t, cfg.GCPCloudRun, "GCP Cloud Run sub-block must survive when selected")
	assert.Equal(t, "512Mi", cfg.GCPCloudRun.Memory)
}

// TestStripOrphanConfig_Idempotent guards against a future "smart" rewrite
// that drifts on repeat calls.
func TestStripOrphanConfig_Idempotent(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "Production"},
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Runtime: "nodejs20.x"},
	}
	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSLambda: boolPtr(true)}

	StripOrphanConfig(comps, cfg)
	first, err := json.Marshal(cfg)
	require.NoError(t, err)

	StripOrphanConfig(comps, cfg)
	second, err := json.Marshal(cfg)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
}

func TestStripOrphanConfig_NilInputs_NoPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() { StripOrphanConfig(nil, &Config{}) })
	assert.NotPanics(t, func() { StripOrphanConfig(&Components{}, nil) })
	assert.NotPanics(t, func() { StripOrphanConfig(nil, nil) })
}

// ──────────────────────────────────────────────────────────────────────────
// DeriveCrossComponentFields
// ──────────────────────────────────────────────────────────────────────────

// TestDeriveCrossComponentFields_NoPrivateSubnets_ClearsNAT pins the actual
// production failure mode from sess_v2_CnqUJ6NRJnLC: a leftover
// EnableNATGateway=&true on a Public-VPC stack with no NAT-needing
// components must be cleared. If every AWSVPC field is cleared, the
// sub-block pointer itself is cleared so json:",omitempty" hides it.
func TestDeriveCrossComponentFields_NoPrivateSubnets_ClearsNAT(t *testing.T) {
	t.Parallel()

	cfg := cfgWithVPCSubBlock(boolPtr(true), boolPtr(true), intPtr(2))
	comps := &Components{
		Cloud:     "AWS",
		AWSVPC:    "Public VPC",
		AWSLambda: boolPtr(true),
		AWSS3:     boolPtr(true),
		// No EKS/ECS/RDS/ElastiCache/OpenSearch/EC2.
	}

	DeriveCrossComponentFields(comps, cfg)

	assert.Nil(t, cfg.AWSVPC,
		"NAT-related fields all cleared → AWSVPC sub-block pointer cleared too")
}

// TestDeriveCrossComponentFields_NeedsPrivateSubnets_Preserves locks the
// non-clobber rule: when the stack DOES need private subnets, the user's
// AWSVPC settings must survive untouched.
func TestDeriveCrossComponentFields_NeedsPrivateSubnets_Preserves(t *testing.T) {
	t.Parallel()

	cfg := cfgWithVPCSubBlock(boolPtr(false), boolPtr(true), intPtr(3))
	comps := &Components{
		Cloud:     "AWS",
		AWSVPC:    "Private VPC",
		AWSEKS:    boolPtr(true),
		AWSLambda: boolPtr(true),
	}

	DeriveCrossComponentFields(comps, cfg)

	require.NotNil(t, cfg.AWSVPC)
	require.NotNil(t, cfg.AWSVPC.EnableNATGateway)
	assert.True(t, *cfg.AWSVPC.EnableNATGateway, "NAT must survive when EKS is selected")
	require.NotNil(t, cfg.AWSVPC.AZCount)
	assert.Equal(t, 3, *cfg.AWSVPC.AZCount, "AZCount must survive when stack needs private subnets")
}

// TestDeriveCrossComponentFields_ClearsAllNATFields covers each of the
// three AWSVPC topology knobs in isolation.
func TestDeriveCrossComponentFields_ClearsAllNATFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		build func() *Config
	}{
		{
			"only_SingleNATGateway",
			func() *Config { return cfgWithVPCSubBlock(boolPtr(true), nil, nil) },
		},
		{
			"only_EnableNATGateway",
			func() *Config { return cfgWithVPCSubBlock(nil, boolPtr(true), nil) },
		},
		{
			"only_AZCount",
			func() *Config { return cfgWithVPCSubBlock(nil, nil, intPtr(3)) },
		},
	}
	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSLambda: boolPtr(true)}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.build()
			DeriveCrossComponentFields(comps, cfg)
			assert.Nil(t, cfg.AWSVPC, "AWSVPC must be nil after all fields cleared")
		})
	}
}

// TestDeriveCrossComponentFields_Idempotent locks no-drift on stable inputs.
func TestDeriveCrossComponentFields_Idempotent(t *testing.T) {
	t.Parallel()

	cfg := cfgWithVPCSubBlock(boolPtr(true), boolPtr(true), intPtr(2))
	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSLambda: boolPtr(true)}

	DeriveCrossComponentFields(comps, cfg)
	first, err := json.Marshal(cfg)
	require.NoError(t, err)

	DeriveCrossComponentFields(comps, cfg)
	second, err := json.Marshal(cfg)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
}

func TestDeriveCrossComponentFields_NilInputs_NoPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() { DeriveCrossComponentFields(nil, &Config{}) })
	assert.NotPanics(t, func() { DeriveCrossComponentFields(&Components{}, nil) })
	assert.NotPanics(t, func() { DeriveCrossComponentFields(&Components{}, &Config{}) })
	assert.NotPanics(t, func() { DeriveCrossComponentFields(nil, nil) })
}

// ──────────────────────────────────────────────────────────────────────────
// Composition with ApplyPresetDefaults — the production pipeline shape.
// ──────────────────────────────────────────────────────────────────────────

// TestCoherence_PipelineWithApplyPresetDefaults mirrors the wrapper sequence
// used in luthersystems/reliable's persistence path:
//
//	StripOrphanConfig → DeriveCrossComponentFields → ApplyPresetDefaults →
//	DeriveCrossComponentFields
//
// Inputs: stale NAT=&true from a prior turn on a now-Public-VPC stack that
// no longer needs private subnets. Expected: NAT is off (or cleared) and
// orphan sub-blocks for unselected components are gone.
func TestCoherence_PipelineWithApplyPresetDefaults(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Cloud: "AWS",
		AWSVPC: &struct {
			SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
			EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
			AZCount          *int  `json:"azCount,omitempty"`
		}{
			SingleNATGateway: boolPtr(true),
			EnableNATGateway: boolPtr(true),
			AZCount:          intPtr(2),
		},
		// Orphan opensearch sub-block from when it WAS in the stack:
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "Production"},
	}
	comps := &Components{
		Cloud:             "AWS",
		Architecture:      "Serverless",
		AWSVPC:            "Public VPC",
		AWSLambda:         boolPtr(true),
		AWSS3:             boolPtr(true),
		AWSKMS:            boolPtr(true),
		AWSSecretsManager: boolPtr(true),
		AWSCloudWatchLogs: boolPtr(true),
	}
	selected := []ComponentKey{
		KeyAWSVPC, KeyAWSLambda, KeyAWSS3, KeyAWSKMS,
		KeyAWSSecretsManager, KeyAWSCloudWatchLogs,
	}

	// Pipeline: strip → derive → ApplyPresetDefaults → derive.
	StripOrphanConfig(comps, cfg)
	DeriveCrossComponentFields(comps, cfg)
	require.NoError(t, New().ApplyPresetDefaults(cfg, comps, selected))
	DeriveCrossComponentFields(comps, cfg)

	assert.Nil(t, cfg.AWSOpenSearch, "orphan opensearch must be gone")

	// Either AWSVPC stays nil or its NAT fields are not true. Both are
	// acceptable outcomes — what we cannot tolerate is NAT=&true on a stack
	// that doesn't need private subnets.
	if cfg.AWSVPC != nil && cfg.AWSVPC.EnableNATGateway != nil {
		assert.False(t, *cfg.AWSVPC.EnableNATGateway,
			"#1435: NAT must be off on a Public-VPC stack with no private-subnet-needing components")
	}
	if cfg.AWSVPC != nil && cfg.AWSVPC.SingleNATGateway != nil {
		assert.False(t, *cfg.AWSVPC.SingleNATGateway,
			"#1435: SingleNATGateway must be off on a Public-VPC stack with no private-subnet-needing components")
	}
}

// TestCoherence_PipelineSurvivesUserSetFields locks the guardrail: the
// pipeline must NOT clobber user-set values on SELECTED components. Mirrors
// reliable's TestApplyPresetDefaults_Issue1435_UserSetFieldNotOverwritten.
func TestCoherence_PipelineSurvivesUserSetFields(t *testing.T) {
	t.Parallel()

	userTimeout := "120s"
	userMem := "2048"
	cfg := &Config{
		Cloud: "AWS",
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Timeout: userTimeout, MemorySize: userMem},
	}
	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSLambda: boolPtr(true)}
	selected := []ComponentKey{KeyAWSVPC, KeyAWSLambda}

	StripOrphanConfig(comps, cfg)
	DeriveCrossComponentFields(comps, cfg)
	require.NoError(t, New().ApplyPresetDefaults(cfg, comps, selected))
	DeriveCrossComponentFields(comps, cfg)

	require.NotNil(t, cfg.AWSLambda)
	assert.Equal(t, userTimeout, cfg.AWSLambda.Timeout)
	assert.Equal(t, userMem, cfg.AWSLambda.MemorySize)
}

// TestStripOrphanConfig_AWSAPIGateway_AliasedTag pins the specific drift
// that the coverage test below discovered: Config.AWSAPIGateway has json
// tag "aws_api_gateway" while KeyAWSAPIGateway = "aws_apigateway". Without
// configTagToKey's alias the orphan-strip silently skips the field. Locks
// the alias so a future cleanup of the alias map cannot regress the strip.
func TestStripOrphanConfig_AWSAPIGateway_AliasedTag(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSAPIGateway: &struct {
			DomainName     string `json:"domainName,omitempty"`
			CertificateArn string `json:"certificateArn,omitempty"`
		}{DomainName: "api.example.com"},
	}
	// Components does NOT select API Gateway → cfg.AWSAPIGateway is orphan.
	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC"}

	StripOrphanConfig(comps, cfg)

	assert.Nil(t, cfg.AWSAPIGateway,
		"orphan cfg.AWSAPIGateway must be cleared even though its json tag "+
			"(aws_api_gateway) differs from KeyAWSAPIGateway (aws_apigateway) — "+
			"configTagToKey's alias bridges the gap")
	// To verify this test still has teeth: remove the "aws_api_gateway"
	// entry from configTagToKey in coherence.go — this assertion will flip
	// to fail because the reflection-based strip falls back to the raw
	// tag and finds no matching key.
}

// TestStripOrphanConfig_KeyCoverage_ConfigSubFieldsHaveKeysAndSwitches walks
// every *struct sub-field on Config whose json tag begins with "aws_" or
// "gcp_" and asserts the tag is recognised by BOTH ComponentSelected AND
// isOrphanStrippableKey. Drift between the two switches (or between Config
// fields and the ComponentKey enum) would silently break orphan-strip for
// the new component — a class of bug that is otherwise invisible until a
// production session leaks an orphan sub-block. This test is the cheap
// drift detector. /review #1437 P2.
func TestStripOrphanConfig_KeyCoverage_ConfigSubFieldsHaveKeysAndSwitches(t *testing.T) {
	t.Parallel()

	cfgType := reflect.TypeOf(Config{})
	visited := 0
	for i := 0; i < cfgType.NumField(); i++ {
		ft := cfgType.Field(i)
		if ft.Type.Kind() != reflect.Pointer || ft.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		tag := jsonTagName(ft.Tag.Get("json"))
		if tag == "" {
			continue
		}
		// Only check cloud-prefixed tags — Config has cloud-agnostic
		// *struct fields too (none today, but the design allows them).
		if !strings.HasPrefix(tag, "aws_") && !strings.HasPrefix(tag, "gcp_") {
			continue
		}
		visited++
		key := configTagToKey(tag)

		// ComponentSelected must have a switch case for the key. The
		// only way to verify reflectively is to construct a Components
		// where the key SHOULD be selected and confirm ComponentSelected
		// returns true. Build a fully-selecting Components and assert
		// the key flips through.
		comps := selectingComponentsFor(key)
		assert.True(t, ComponentSelected(comps, key),
			"ComponentSelected has no case for Config field with json tag %q (key %q) — "+
				"add it to ComponentSelected's switch in coherence.go so orphan-strip recognises this component",
			tag, key)

		// isOrphanStrippableKey must include the key. The strip walks
		// Config reflectively, so a key missing here is silently ignored
		// even when ComponentSelected knows about it.
		assert.True(t, isOrphanStrippableKey(key),
			"isOrphanStrippableKey has no entry for Config field with json tag %q (key %q) — "+
				"add it to the switch in coherence.go so orphan-strip clears this sub-block",
			tag, key)
	}
	// Self-validation: ensure the loop actually exercised the matrix. A
	// future Config refactor that flattens or renames every sub-field would
	// otherwise pass this drift detector vacuously. 30 is a soft floor —
	// AWS + GCP per-component config sub-blocks comfortably exceed it today.
	require.GreaterOrEqual(t, visited, 30,
		"drift detector exercised %d Config sub-fields — expected ≥30; Config layout may have changed", visited)
}

// selectingComponentsFor returns a Components value where ComponentSelected
// (against the returned value, for the given key) should report true. It
// returns nil if the key has no corresponding Components field — which
// itself is a bug surfaced by the calling test. Helper for the coverage
// test above.
func selectingComponentsFor(key ComponentKey) *Components {
	c := &Components{}
	switch key {
	case KeyAWSVPC:
		c.AWSVPC = "Public VPC"
	case KeyAWSEC2:
		c.AWSEC2 = "Intel"
	case KeyGCPCompute:
		c.GCPCompute = "n2-standard-2"
	case KeyAWSBackups:
		c.AWSBackups = &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{}
	case KeyGCPBackups:
		c.GCPBackups = &struct {
			Compute  *bool `json:"gcp_compute,omitempty"`
			CloudSQL *bool `json:"gcp_cloudsql,omitempty"`
			GCS      *bool `json:"gcp_gcs,omitempty"`
		}{}
	default:
		// All remaining keys this coverage test exercises are pointer-
		// to-bool selections on Components. Find the matching field by
		// json tag and set it to a non-nil &true via reflection.
		setPointerToBoolTrueByTag(c, string(key))
	}
	return c
}

// setPointerToBoolTrueByTag locates the field on *Components whose json tag
// matches `tag` and, if that field is *bool, sets it to a non-nil &true.
// Lets selectingComponentsFor handle the 30+ pointer-typed components
// without a per-key switch — a typo in the test fixture would itself be a
// drift-detector miss.
func setPointerToBoolTrueByTag(c *Components, tag string) {
	cv := reflect.ValueOf(c).Elem()
	ct := cv.Type()
	for i := 0; i < ct.NumField(); i++ {
		ft := ct.Field(i)
		if jsonTagName(ft.Tag.Get("json")) != tag {
			continue
		}
		fv := cv.Field(i)
		if fv.Kind() == reflect.Pointer && fv.Type().Elem().Kind() == reflect.Bool {
			b := true
			fv.Set(reflect.ValueOf(&b))
		}
		return
	}
}

// runDefaultCoherenceThenMapNAT runs the EXACT coherence + preset-default
// sequence reliable applies before composing a stack (strip → derive →
// ComputePresetDefaults → MergeConfigs → derive), then runs the mapper's
// KeyAWSVPC validation. It returns the resolved enable_nat_gateway tfvar value
// (nil if the mapper did not emit one), the in-place-resolved cfg, and the
// mapper error.
//
// It uses New() so the REAL embedded aws/vpc preset HCL defaults flow through —
// in particular enable_nat_gateway's intentional false "backstop" (#393). This
// is what reproduces the self-contradiction: the overlay injects the backstop
// false into the user's nil EnableNATGateway, then the mapper rejects that very
// value. A composer with a stubbed/empty preset FS would NOT reproduce it.
func runDefaultCoherenceThenMapNAT(t *testing.T, comps *Components, cfg *Config, selected []ComponentKey) (any, *Config, error) {
	t.Helper()
	c := New()
	StripOrphanConfig(comps, cfg)
	DeriveCrossComponentFields(comps, cfg)
	overlay, err := c.ComputePresetDefaults(*cfg, comps, selected)
	require.NoError(t, err)
	MergeConfigs(cfg, &overlay)
	DeriveCrossComponentFields(comps, cfg)
	vals, mapErr := DefaultMapper{}.BuildModuleValues(KeyAWSVPC, comps, cfg, "test", "us-east-1")
	if mapErr != nil {
		return nil, cfg, mapErr
	}
	return vals["enable_nat_gateway"], cfg, nil
}

// TestCoherence_DefaultPathNATForNeedsPrivate_Issue393 is the HEADLINE
// regression for the composer self-contradiction surfaced on staging session
// sess_v2_ygVBb4cl1nJf (stack = aws_vpc + aws_rds + lambda + apigateway + s3 +
// secretsmanager + bedrock + sqs + cognito + cloudwatch_logs + github_actions +
// kms; default VPC config).
//
// A stack that has a VPC plus any component needing private subnets, built with
// DEFAULT VPC config (the user never authored EnableNATGateway), used to:
//  1. get AWSVPC.EnableNATGateway=false from the composer's OWN preset-default
//     overlay (the aws/vpc HCL backstop, #393), then
//  2. get rejected by the composer's OWN mapper validation with
//     "AWSVPC.EnableNATGateway=false is incompatible with components that
//     require private subnets ...".
//
// reliable surfaced (2) as a red "Terraform Error" button. The asymmetry: the
// coherence derive only ACTS on the !needsPrivate case (clears NAT); for the
// needsPrivate case it does nothing, so the overlay-injected false survives to
// the mapper.
//
// Pre-fix: this FAILS — BuildModuleValues returns the validation error.
// Post-fix: the COMPUTED default is component-aware, so the resolved
// EnableNATGateway is true and no error is returned.
func TestCoherence_DefaultPathNATForNeedsPrivate_Issue393(t *testing.T) {
	t.Parallel()

	comps := &Components{
		Cloud:  "AWS",
		AWSVPC: "Public VPC",
		AWSRDS: boolPtr(true),
	}
	// Empty/default VPC config: the user NEVER authored EnableNATGateway. This
	// is the default path the bug lives on.
	cfg := &Config{Cloud: "AWS"}
	selected := []ComponentKey{KeyAWSVPC, KeyAWSRDS}

	natVal, resolved, err := runDefaultCoherenceThenMapNAT(t, comps, cfg, selected)
	require.NoError(t, err,
		"#393: a default-config VPC+RDS stack must NOT trip the composer's own "+
			"EnableNATGateway=false fail-fast — the computed default must be NAT=true")
	require.NotNil(t, resolved.AWSVPC, "AWSVPC sub-block must carry the derived NAT decision")
	require.NotNil(t, resolved.AWSVPC.EnableNATGateway, "EnableNATGateway must be resolved, not left nil")
	assert.True(t, *resolved.AWSVPC.EnableNATGateway,
		"resolved EnableNATGateway must be true on a needs-private stack with default config")
	assert.Equal(t, true, natVal, "mapper must emit enable_nat_gateway=true in tfvars")
}

// TestCoherence_DefaultPathNAT_AllNeedsPrivateComponents is the future-proofing
// guard the user asked for: it sweeps the WHOLE needs-private component set
// (EKS / ECS / RDS / ElastiCache / OpenSearch / EC2 node groups, plus a
// combined stack) and asserts that, with a default VPC config, the
// default+coherence+mapper path resolves NAT on and never trips the fail-fast.
// Any new component added to stackNeedsPrivateSubnets should be added here too.
func TestCoherence_DefaultPathNAT_AllNeedsPrivateComponents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		comps    *Components
		selected []ComponentKey
	}{
		{"EKS", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSEKS: boolPtr(true)},
			[]ComponentKey{KeyAWSVPC, KeyAWSEKS}},
		{"ECS", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSECS: boolPtr(true)},
			[]ComponentKey{KeyAWSVPC, KeyAWSECS}},
		{"RDS", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSRDS: boolPtr(true)},
			[]ComponentKey{KeyAWSVPC, KeyAWSRDS}},
		{"ElastiCache", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSElastiCache: boolPtr(true)},
			[]ComponentKey{KeyAWSVPC, KeyAWSElastiCache}},
		{"OpenSearch", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSOpenSearch: boolPtr(true)},
			[]ComponentKey{KeyAWSVPC, KeyAWSOpenSearch}},
		{"EC2 node group", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSEC2: "Intel"},
			[]ComponentKey{KeyAWSVPC, KeyAWSEC2}},
		{"EKS + RDS combined", &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSEKS: boolPtr(true), AWSRDS: boolPtr(true)},
			[]ComponentKey{KeyAWSVPC, KeyAWSEKS, KeyAWSRDS}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{Cloud: "AWS"} // default VPC config
			natVal, resolved, err := runDefaultCoherenceThenMapNAT(t, tc.comps, cfg, tc.selected)
			require.NoError(t, err,
				"#393: default-config stack with %s must not trip the EnableNATGateway=false fail-fast", tc.name)
			require.NotNil(t, resolved.AWSVPC)
			require.NotNil(t, resolved.AWSVPC.EnableNATGateway)
			assert.True(t, *resolved.AWSVPC.EnableNATGateway,
				"resolved EnableNATGateway must be true for %s", tc.name)
			assert.Equal(t, true, natVal, "mapper must emit enable_nat_gateway=true for %s", tc.name)
		})
	}
}

// TestCoherence_DefaultPathNAT_CoercesExplicitFalse locks the REVERSED
// contract (supersedes #805's fail-fast, per the heal decision): an explicit
// EnableNATGateway=false on a needs-private stack is a known-always-invalid
// value that pre-#805 snapshots froze into stored config reliable composes
// verbatim, so the mapper now HEALS it — coercing enable_nat_gateway=true with
// no error — instead of failing fast the user can't act on.
//
// This was TestCoherence_DefaultPathNAT_PreservesExplicitFalse (#805), which
// asserted the opposite (STILL errors). It is intentionally inverted, not
// deleted: the explicit-false guard is replaced by the heal coercion.
func TestCoherence_DefaultPathNAT_CoercesExplicitFalse(t *testing.T) {
	t.Parallel()

	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}
	cfg := cfgWithVPCSubBlock(nil, boolPtr(false), nil) // user-explicit NAT=false
	selected := []ComponentKey{KeyAWSVPC, KeyAWSEKS}

	natVal, resolved, err := runDefaultCoherenceThenMapNAT(t, comps, cfg, selected)
	require.NoError(t, err,
		"explicit EnableNATGateway=false on a needs-private stack must now be HEALED "+
			"(coerced to NAT=true), not rejected — supersedes the #805 fail-fast")
	assert.Equal(t, true, natVal,
		"mapper must coerce enable_nat_gateway=true for a needs-private stack with frozen NAT=false")
	// Provenance: the heal rewrites only the emitted tfvars, never the stored
	// Config — the explicit false survives in cfg (reliable's source of truth).
	require.NotNil(t, resolved.AWSVPC)
	require.NotNil(t, resolved.AWSVPC.EnableNATGateway)
	assert.False(t, *resolved.AWSVPC.EnableNATGateway,
		"the stored Config is NOT mutated by the heal; only the emitted tfvars are coerced")
}

// TestCoherence_DefaultPathNAT_NoNeedsPrivateStaysOff is the negative case: a
// stack with NO private-subnet-needing component (VPC + S3) must NOT get NAT
// turned on by the fix. The component-aware default only applies to
// needs-private stacks; here NAT ends nil/off and no error is raised.
func TestCoherence_DefaultPathNAT_NoNeedsPrivateStaysOff(t *testing.T) {
	t.Parallel()

	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSS3: boolPtr(true)}
	cfg := &Config{Cloud: "AWS"} // default VPC config
	selected := []ComponentKey{KeyAWSVPC, KeyAWSS3}

	natVal, resolved, err := runDefaultCoherenceThenMapNAT(t, comps, cfg, selected)
	require.NoError(t, err, "a non-needs-private stack must compose cleanly")
	// The final derive clears NAT fields (and possibly the whole AWSVPC block)
	// on a stack that doesn't need private subnets.
	if resolved.AWSVPC != nil {
		assert.Nil(t, resolved.AWSVPC.EnableNATGateway,
			"NAT must stay off (nil) on a stack with no private-subnet consumers")
	}
	// Public VPC with no consumers: the mapper emits enable_nat_gateway=false.
	assert.Equal(t, false, natVal,
		"Public VPC with no consumers must keep NAT off in tfvars")
}

// TestCoherence_DefaultPathLambdaTimeoutUnit is the regression for the SECOND
// instance of the composer self-contradiction class (same shape as the
// #393/#805 NAT bug), surfaced by the class-level Guard B
// (selfvalidation_test.go). A stack selecting aws_lambda built with DEFAULT
// config (the user never authored AWSLambda.Timeout) used to:
//
//  1. get AWSLambda.Timeout="3" from the composer's OWN preset-default overlay
//     — aws/lambda/variables.tf declares `timeout` as an HCL number (default
//     3 seconds), and the zero-only overlay stringifies that to the bare
//     integer "3"; then
//  2. get rejected by the composer's OWN mapper validation
//     (parseDurationToSeconds), which accepts only unit-suffixed durations
//     ("3s"/"30s"/"15m") and fails fast on a bare integer.
//
// reliable would surface (2) as a red "Terraform Error" — exactly the NAT
// symptom. The asymmetry: the IR enum default is "3s" (so the UI dodges it),
// but ANY default-materialized stack with an empty Timeout hits the bare
// integer.
//
// Pre-fix: this FAILS — BuildModuleValues returns the validation error.
// Post-fix: overrideLambdaTimeoutUnitDefault normalises the overlay's own
// bare-integer default to "3s", so the mapper emits timeout=3 and no error.
func TestCoherence_DefaultPathLambdaTimeoutUnit(t *testing.T) {
	t.Parallel()

	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSLambda: boolPtr(true)}
	// Empty/default config: the user NEVER authored AWSLambda.Timeout. This is
	// the default path the bug lives on.
	cfg := &Config{Cloud: "AWS"}
	selected := []ComponentKey{KeyAWSVPC, KeyAWSLambda}

	StripOrphanConfig(comps, cfg)
	DeriveCrossComponentFields(comps, cfg)
	require.NoError(t, New().ApplyPresetDefaults(cfg, comps, selected))
	DeriveCrossComponentFields(comps, cfg)

	require.NotNil(t, cfg.AWSLambda, "overlay must materialise the AWSLambda default block")
	assert.Equal(t, "3s", cfg.AWSLambda.Timeout,
		"the composer's own default timeout must be unit-suffixed (\"3s\"), not the "+
			"bare HCL number (\"3\") its mapper rejects")

	vals, err := DefaultMapper{}.BuildModuleValues(KeyAWSLambda, comps, cfg, "test", "us-east-1")
	require.NoError(t, err,
		"a default-config Lambda stack must NOT trip the composer's own Timeout "+
			"fail-fast — the computed default must already be mapper-valid")
	assert.Equal(t, 3, vals["timeout"], "mapper must emit timeout=3 (seconds) in tfvars")
}

// TestCoherence_DefaultPathLambdaTimeout_PreservesUserValue locks the
// provenance guarantee for the Lambda-timeout fix: the overlay is zero-only, so
// a user-explicit Timeout is never echoed into the overlay and therefore never
// rewritten by overrideLambdaTimeoutUnitDefault. A user "120s" survives intact.
func TestCoherence_DefaultPathLambdaTimeout_PreservesUserValue(t *testing.T) {
	t.Parallel()

	comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC", AWSLambda: boolPtr(true)}
	cfg := &Config{
		Cloud: "AWS",
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Timeout: "120s"},
	}
	selected := []ComponentKey{KeyAWSVPC, KeyAWSLambda}

	StripOrphanConfig(comps, cfg)
	DeriveCrossComponentFields(comps, cfg)
	require.NoError(t, New().ApplyPresetDefaults(cfg, comps, selected))
	DeriveCrossComponentFields(comps, cfg)

	require.NotNil(t, cfg.AWSLambda)
	assert.Equal(t, "120s", cfg.AWSLambda.Timeout,
		"a user-explicit Timeout must be preserved, never rewritten by the default normaliser")

	vals, err := DefaultMapper{}.BuildModuleValues(KeyAWSLambda, comps, cfg, "test", "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, 120, vals["timeout"], "mapper must emit the user's timeout (120s -> 120)")
}
