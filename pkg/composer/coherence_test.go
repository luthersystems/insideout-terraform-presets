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
