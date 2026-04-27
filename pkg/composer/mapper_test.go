package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildModuleValues_AWSEC2_ArchAndInstanceType(t *testing.T) {
	m := DefaultMapper{}

	t.Run("ARM arch maps to arm64 and defaults to t4g.medium", func(t *testing.T) {
		comps := &Components{AWSEC2: "ARM"}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, "arm64", vals["arch"])
		assert.Equal(t, "t4g.medium", vals["instance_type"])
	})

	t.Run("Intel arch maps to x86_64, no default instance_type override", func(t *testing.T) {
		comps := &Components{AWSEC2: "Intel"}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, "x86_64", vals["arch"])
		// Intel uses preset default (t3.medium), so instance_type should not be set
		_, hasInstanceType := vals["instance_type"]
		assert.False(t, hasInstanceType, "Intel should use preset default, not override instance_type")
	})

	t.Run("explicit instance_type from config overrides default", func(t *testing.T) {
		comps := &Components{AWSEC2: "ARM"}
		cfg := configWithAWSEC2(awsEC2CfgInput{InstanceType: "c7g.large"})
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "arm64", vals["arch"])
		assert.Equal(t, "c7g.large", vals["instance_type"], "explicit config should override default")
	})
}

func TestBuildModuleValues_AWSEC2_DiskSize(t *testing.T) {
	m := DefaultMapper{}

	t.Run("diskSizePerServer maps to root_volume_size as int", func(t *testing.T) {
		cfg := configWithAWSEC2(awsEC2CfgInput{DiskSizePerServer: "32"})
		vals, err := m.BuildModuleValues(KeyAWSEC2, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 32, vals["root_volume_size"], "should convert string to int")
	})

	t.Run("empty diskSizePerServer does not set root_volume_size", func(t *testing.T) {
		cfg := configWithAWSEC2(awsEC2CfgInput{})
		vals, err := m.BuildModuleValues(KeyAWSEC2, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasKey := vals["root_volume_size"]
		assert.False(t, hasKey, "should not set root_volume_size when empty")
	})
}

func TestBuildModuleValues_AWSEC2_EnableInstanceConnect(t *testing.T) {
	m := DefaultMapper{}
	trueVal := true
	falseVal := false

	t.Run("enableInstanceConnect maps to enable_instance_connect", func(t *testing.T) {
		cfg := configWithAWSEC2(awsEC2CfgInput{EnableInstanceConnect: &trueVal})
		vals, err := m.BuildModuleValues(KeyAWSEC2, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, true, vals["enable_instance_connect"])
	})

	t.Run("explicit false enableInstanceConnect does not set key", func(t *testing.T) {
		cfg := configWithAWSEC2(awsEC2CfgInput{EnableInstanceConnect: &falseVal})
		vals, err := m.BuildModuleValues(KeyAWSEC2, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasKey := vals["enable_instance_connect"]
		assert.False(t, hasKey, "explicit false should not set enable_instance_connect")
	})

	t.Run("nil enableInstanceConnect does not set key", func(t *testing.T) {
		cfg := configWithAWSEC2(awsEC2CfgInput{})
		vals, err := m.BuildModuleValues(KeyAWSEC2, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasKey := vals["enable_instance_connect"]
		assert.False(t, hasKey, "should not set enable_instance_connect when nil")
	})
}

func TestBuildModuleValues_VPC_PublicPrivateMode(t *testing.T) {
	m := DefaultMapper{}
	boolPtr := func(v bool) *bool { return &v }

	// assertVPCCaseRan verifies the function processed the VPC case arm
	// by checking common keys are present (guards against no-op mutations).
	assertVPCCaseRan := func(t *testing.T, vals map[string]any) {
		t.Helper()
		assert.Equal(t, "test", vals["project"], "common key 'project' should be set")
		assert.Equal(t, "us-east-1", vals["region"], "common key 'region' should be set")
		assert.Equal(t, "prod", vals["environment"], "common key 'environment' should be set")
	}

	t.Run("Public VPC disables private subnets and NAT when no downstream needs them", func(t *testing.T) {
		comps := &Components{AWSVPC: "Public VPC"}
		vals, err := m.BuildModuleValues(KeyAWSVPC, comps, nil, "test", "us-east-1")
		require.NoError(t, err)
		assertVPCCaseRan(t, vals)
		assert.Equal(t, false, vals["enable_private_subnets"])
		assert.Equal(t, false, vals["enable_nat_gateway"])
	})

	// Each "keeps private subnets" test verifies that enable_private_subnets
	// is NOT set to false (i.e. the key is absent, so the preset default of
	// true takes effect). We also check that enable_nat_gateway is absent.
	keepsCases := []struct {
		name  string
		comps *Components
	}{
		{"EKS", &Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}},
		{"RDS", &Components{AWSVPC: "Public VPC", AWSRDS: boolPtr(true)}},
		{"ElastiCache", &Components{AWSVPC: "Public VPC", AWSElastiCache: boolPtr(true)}},
		{"OpenSearch", &Components{AWSVPC: "Public VPC", AWSOpenSearch: boolPtr(true)}},
		{"EC2 node group", &Components{AWSVPC: "Public VPC", AWSEC2: "Intel"}},
		{"ECS", &Components{AWSVPC: "Public VPC", AWSECS: boolPtr(true)}},
		{"EKS + RDS composite stack", &Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true), AWSRDS: boolPtr(true)}},
	}
	for _, tc := range keepsCases {
		t.Run("Public VPC keeps private subnets when "+tc.name+" is selected", func(t *testing.T) {
			vals, err := m.BuildModuleValues(KeyAWSVPC, tc.comps, nil, "test", "us-east-1")
			require.NoError(t, err)
			assertVPCCaseRan(t, vals)
			_, hasPrivate := vals["enable_private_subnets"]
			_, hasNat := vals["enable_nat_gateway"]
			assert.False(t, hasPrivate, "should not disable private subnets when %s needs them", tc.name)
			assert.False(t, hasNat, "should not disable NAT when %s needs it", tc.name)
		})
	}

	t.Run("Private VPC uses preset defaults (no override)", func(t *testing.T) {
		comps := &Components{AWSVPC: "Private VPC"}
		vals, err := m.BuildModuleValues(KeyAWSVPC, comps, nil, "test", "us-east-1")
		require.NoError(t, err)
		assertVPCCaseRan(t, vals)
		_, hasPrivate := vals["enable_private_subnets"]
		_, hasNat := vals["enable_nat_gateway"]
		assert.False(t, hasPrivate, "Private VPC should not override enable_private_subnets (preset default is true)")
		assert.False(t, hasNat, "Private VPC should not override enable_nat_gateway (preset default is true)")
	})

	t.Run("empty AWSVPC uses preset defaults", func(t *testing.T) {
		comps := &Components{}
		vals, err := m.BuildModuleValues(KeyAWSVPC, comps, nil, "test", "us-east-1")
		require.NoError(t, err)
		assertVPCCaseRan(t, vals)
		_, hasPrivate := vals["enable_private_subnets"]
		_, hasNat := vals["enable_nat_gateway"]
		assert.False(t, hasPrivate)
		assert.False(t, hasNat)
	})
}

func TestStackNeedsPrivateSubnets(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	assert.False(t, stackNeedsPrivateSubnets(nil), "nil comps")
	assert.False(t, stackNeedsPrivateSubnets(&Components{}), "empty comps")
	assert.False(t, stackNeedsPrivateSubnets(&Components{AWSEKS: boolPtr(false)}), "EKS explicitly false")

	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSEKS: boolPtr(true)}), "AWSEKS")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSECS: boolPtr(true)}), "AWSECS")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSRDS: boolPtr(true)}), "AWSRDS")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSElastiCache: boolPtr(true)}), "AWSElastiCache")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSOpenSearch: boolPtr(true)}), "AWSOpenSearch")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSEC2: "Intel"}), "AWSEC2 Intel")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSEC2: "ARM"}), "AWSEC2 ARM")

	_ = boolPtr
}

// cfgWithAWSVPC builds a Config with an AWSVPC sub-config populated from the
// provided pointer fields. Kills the anonymous-struct literal repetition that
// would otherwise appear in every subtest below.
func cfgWithAWSVPC(single, enable *bool, az *int) *Config {
	c := &Config{}
	c.AWSVPC = &struct {
		SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
		EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
		AZCount          *int  `json:"azCount,omitempty"`
	}{SingleNATGateway: single, EnableNATGateway: enable, AZCount: az}
	return c
}

func TestBuildModuleValues_VPC_AWSVPCConfig(t *testing.T) {
	m := DefaultMapper{}
	boolPtr := func(v bool) *bool { return &v }
	intPtr := func(v int) *int { return &v }

	t.Run("SingleNATGateway=false writes single_nat_gateway=false", func(t *testing.T) {
		cfg := cfgWithAWSVPC(boolPtr(false), nil, nil)
		vals, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, cfg, "test", "us-east-1")
		require.NoError(t, err)
		// Type-guard the cast so a future mutation writing a stringified bool
		// would fail the type assertion rather than pass assert.Equal.
		assert.False(t, vals["single_nat_gateway"].(bool))
	})

	t.Run("AZCount=3 writes az_count=3 as int", func(t *testing.T) {
		cfg := cfgWithAWSVPC(nil, nil, intPtr(3))
		vals, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, cfg, "test", "us-east-1")
		require.NoError(t, err)
		assert.Equal(t, 3, vals["az_count"].(int))
	})

	t.Run("unset fields do not write to vals (defer to HCL default)", func(t *testing.T) {
		cfg := cfgWithAWSVPC(nil, nil, nil)
		vals, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, cfg, "test", "us-east-1")
		require.NoError(t, err)
		for _, k := range []string{"single_nat_gateway", "enable_nat_gateway", "az_count"} {
			_, has := vals[k]
			assert.False(t, has, "unset pointer should not write %q", k)
		}
	})

	t.Run("nil cfg.AWSVPC is a no-op — no VPC-topology keys leak", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, &Config{}, "test", "us-east-1")
		require.NoError(t, err)
		for _, k := range []string{"single_nat_gateway", "enable_nat_gateway", "az_count"} {
			_, has := vals[k]
			assert.False(t, has, "nil cfg.AWSVPC should not write %q", k)
		}
	})

	t.Run("Public VPC with user SingleNATGateway=false: both apply", func(t *testing.T) {
		// Public VPC forces enable_nat_gateway=false (no private subnets);
		// user's SingleNATGateway=false still applies (vestigial but not wrong).
		comps := &Components{AWSVPC: "Public VPC"}
		cfg := cfgWithAWSVPC(boolPtr(false), nil, nil)
		vals, err := m.BuildModuleValues(KeyAWSVPC, comps, cfg, "test", "us-east-1")
		require.NoError(t, err)
		assert.False(t, vals["enable_nat_gateway"].(bool), "Public VPC sets enable_nat_gateway=false")
		assert.False(t, vals["single_nat_gateway"].(bool), "user config still applies")
	})

	t.Run("user EnableNATGateway=true overrides Public-VPC-derived false", func(t *testing.T) {
		comps := &Components{AWSVPC: "Public VPC"}
		cfg := cfgWithAWSVPC(nil, boolPtr(true), nil)
		vals, err := m.BuildModuleValues(KeyAWSVPC, comps, cfg, "test", "us-east-1")
		require.NoError(t, err)
		assert.True(t, vals["enable_nat_gateway"].(bool), "user override wins over Public VPC default")
	})
}

// TestBuildModuleValues_VPC_AWSVPCConfig_Validation pins the mapper-level
// validation for invalid Config.AWSVPC combinations that would produce a
// broken stack (private subnets without egress) or fail at `terraform validate`.
// Catching these in Go fails fast and gives actionable errors.
func TestBuildModuleValues_VPC_AWSVPCConfig_Validation(t *testing.T) {
	m := DefaultMapper{}
	boolPtr := func(v bool) *bool { return &v }
	intPtr := func(v int) *int { return &v }

	t.Run("EnableNATGateway=false with EKS returns ValidationError", func(t *testing.T) {
		comps := &Components{AWSEKS: boolPtr(true)}
		cfg := cfgWithAWSVPC(nil, boolPtr(false), nil)
		_, err := m.BuildModuleValues(KeyAWSVPC, comps, cfg, "test", "us-east-1")
		require.Error(t, err)
		var verr *ValidationError
		assert.ErrorAs(t, err, &verr, "should return ValidationError so API-layer can HTTP 400")
		assert.Contains(t, err.Error(), "EnableNATGateway=false",
			"error should name the offending knob")
	})

	t.Run("EnableNATGateway=false with RDS returns ValidationError", func(t *testing.T) {
		comps := &Components{AWSRDS: boolPtr(true)}
		cfg := cfgWithAWSVPC(nil, boolPtr(false), nil)
		_, err := m.BuildModuleValues(KeyAWSVPC, comps, cfg, "test", "us-east-1")
		require.Error(t, err)
	})


	t.Run("EnableNATGateway=false without downstream components is allowed", func(t *testing.T) {
		comps := &Components{} // no private-subnet consumers
		cfg := cfgWithAWSVPC(nil, boolPtr(false), nil)
		vals, err := m.BuildModuleValues(KeyAWSVPC, comps, cfg, "test", "us-east-1")
		require.NoError(t, err, "public-only VPC with NAT disabled is valid")
		assert.False(t, vals["enable_nat_gateway"].(bool))
	})

	t.Run("AZCount=0 returns ValidationError", func(t *testing.T) {
		cfg := cfgWithAWSVPC(nil, nil, intPtr(0))
		_, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, cfg, "test", "us-east-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AZCount")
	})

	t.Run("AZCount=-1 returns ValidationError", func(t *testing.T) {
		cfg := cfgWithAWSVPC(nil, nil, intPtr(-1))
		_, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, cfg, "test", "us-east-1")
		require.Error(t, err)
	})

	t.Run("AZCount=1 is allowed (HCL default >= 1)", func(t *testing.T) {
		cfg := cfgWithAWSVPC(nil, nil, intPtr(1))
		vals, err := m.BuildModuleValues(KeyAWSVPC, &Components{}, cfg, "test", "us-east-1")
		require.NoError(t, err)
		assert.Equal(t, 1, vals["az_count"].(int))
	})
}

// TestBuildModuleValues_VPC_MapperHCLContract protects against typos in the
// mapper's output keys. A mutation renaming a vals[...] key in mapper.go to
// something not declared in aws/vpc/variables.tf previously passed every unit
// test (the composer's variable-discovery step silently drops unknown keys).
// Reads the actual preset variables.tf via InspectPreset and asserts every
// key the mapper writes for KeyAWSVPC with all AWSVPC knobs set is declared.
func TestBuildModuleValues_VPC_MapperHCLContract(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	intPtr := func(v int) *int { return &v }

	mod, err := InspectPreset("aws/vpc")
	require.NoError(t, err)
	declaredSet := make(map[string]bool, len(mod.Variables))
	for name := range mod.Variables {
		declaredSet[name] = true
	}

	// Exercise every AWSVPC knob so the full set of mapper-written keys is
	// present in vals.
	cfg := cfgWithAWSVPC(boolPtr(false), boolPtr(true), intPtr(3))
	vals, err := DefaultMapper{}.BuildModuleValues(
		KeyAWSVPC, &Components{AWSVPC: "Private VPC"}, cfg, "test", "us-east-1",
	)
	require.NoError(t, err)

	for k := range vals {
		assert.True(t, declaredSet[k],
			"mapper wrote key %q but aws/vpc/variables.tf does not declare it; the composer would silently drop it",
			k)
	}
}

func TestBuildModuleValues_CloudWatchLogs_Retention(t *testing.T) {
	m := DefaultMapper{}

	t.Run("retention days integer set directly", func(t *testing.T) {
		cfg := &Config{
			AWSCloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 7,
			},
		}
		vals, err := m.BuildModuleValues(KeyAWSCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 7, vals["retention_in_days"])
		_, hasOldKey := vals["retention"]
		assert.False(t, hasOldKey, "should not emit old 'retention' key")
	})

	t.Run("90 days retention", func(t *testing.T) {
		cfg := &Config{
			AWSCloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 90,
			},
		}
		vals, err := m.BuildModuleValues(KeyAWSCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 90, vals["retention_in_days"])
	})

	t.Run("AWSCloudWatchLogs RetentionDays maps to retention_in_days", func(t *testing.T) {
		cfg := &Config{
			AWSCloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 365,
			},
		}
		vals, err := m.BuildModuleValues(KeyAWSCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 365, vals["retention_in_days"])
	})

	t.Run("zero retention does not set key", func(t *testing.T) {
		cfg := &Config{
			AWSCloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 0,
			},
		}
		vals, err := m.BuildModuleValues(KeyAWSCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasKey := vals["retention_in_days"]
		assert.False(t, hasKey)
	})
}

func TestBuildModuleValues_Cloudfront_OriginPath(t *testing.T) {
	m := DefaultMapper{}

	t.Run("originPath maps to origin_path", func(t *testing.T) {
		path := "/assets"
		cfg := &Config{
			AWSCloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{OriginPath: &path},
		}
		vals, err := m.BuildModuleValues(KeyAWSCloudfront, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "/assets", vals["origin_path"])
	})

	t.Run("defaultTtl maps to default_ttl_seconds (number)", func(t *testing.T) {
		// The CloudFront module variable is default_ttl_seconds (number).
		// Earlier mapper versions emitted default_ttl (string), which the
		// module never declared, so the user pick was silently dropped.
		// See upstream issue #131.
		ttl := "3600"
		cfg := &Config{
			AWSCloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{DefaultTtl: &ttl},
		}
		vals, err := m.BuildModuleValues(KeyAWSCloudfront, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 3600, vals["default_ttl_seconds"])
		_, hasOld := vals["default_ttl"]
		assert.False(t, hasOld, "default_ttl (string) is the pre-fix key — must not be emitted")
	})
}

// Legacy→prefixed migration of the AWSCloudfront.CachePaths deprecation
// and related sub-config field moves is covered by
// TestConfig_Normalize_CachePathsMigration (pure Normalize) and
// TestComposeStack_NormalizesLegacyConfig (integration at the compose
// boundary). The mapper now only reads AWS-prefixed Config fields, so
// mapper-level coverage is above (direct AWSCloudfront/AWSCloudWatchLogs
// etc. reads) and in
// TestBuildModuleValues_IgnoresUnnormalizedLegacyConfig (negative
// regression).

// TestConfig_Normalize_CachePathsMigration pins the within-AWSCloudfront
// deprecation: AWSCloudfront.CachePaths is a deprecated sub-field and must
// migrate to OriginPath during Normalize. Distinct from the legacy
// Cloudfront→AWSCloudfront promotion (deleted in Phase 4).
func TestConfig_Normalize_CachePathsMigration(t *testing.T) {
	t.Run("AWSCloudfront CachePaths migrates to OriginPath and is cleared", func(t *testing.T) {
		path := "/old"
		cfg := Config{
			Cloud: "AWS",
			AWSCloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{CachePaths: &path},
		}
		cfg.Normalize()
		require.NotNil(t, cfg.AWSCloudfront)
		require.NotNil(t, cfg.AWSCloudfront.OriginPath)
		assert.Equal(t, "/old", *cfg.AWSCloudfront.OriginPath)
		assert.Nil(t, cfg.AWSCloudfront.CachePaths, "CachePaths should be cleared after migration")
	})

	t.Run("OriginPath already set is not overwritten by CachePaths", func(t *testing.T) {
		newPath := "/new"
		oldPath := "/old"
		cfg := Config{
			Cloud: "AWS",
			AWSCloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{OriginPath: &newPath, CachePaths: &oldPath},
		}
		cfg.Normalize()
		require.NotNil(t, cfg.AWSCloudfront)
		require.NotNil(t, cfg.AWSCloudfront.OriginPath)
		assert.Equal(t, "/new", *cfg.AWSCloudfront.OriginPath)
	})
}

func TestBuildModuleValues_AWSECS_Defaults(t *testing.T) {
	m := DefaultMapper{}

	t.Run("ECS with no config provides stubs", func(t *testing.T) {
		comps := &Components{AWSECS: ptrBool(true)}
		vals, err := m.BuildModuleValues(KeyAWSECS, comps, nil, "demo", "us-east-1")
		require.NoError(t, err)

		assert.Equal(t, "", vals["vpc_id"])
		assert.Equal(t, []any{}, vals["private_subnet_ids"])
		assert.Equal(t, []any{}, vals["public_subnet_ids"])

		// ECS must NOT get EKS-specific variables
		_, hasLogTypes := vals["cluster_enabled_log_types"]
		assert.False(t, hasLogTypes, "ECS should not have cluster_enabled_log_types")
	})

	t.Run("ECS with config produces ECS-specific values", func(t *testing.T) {
		comps := &Components{AWSECS: ptrBool(true)}
		cfg := configWithAWSECS(awsECSCfgInput{EnableContainerInsights: ptrBool(true)})
		vals, err := m.BuildModuleValues(KeyAWSECS, comps, cfg, "demo", "us-east-1")
		require.NoError(t, err)

		// ECS config should produce ECS-specific values
		assert.Equal(t, true, vals["enable_container_insights"])

		// EKS with same config should NOT get ECS values (EKS ignores AWSECS config)
		valsEKS, err := m.BuildModuleValues(KeyAWSEKS, comps, cfg, "demo", "us-east-1")
		require.NoError(t, err)
		_, hasInsights := valsEKS["enable_container_insights"]
		assert.False(t, hasInsights, "EKS should not have ECS config fields")
	})
}

func TestBuildModuleValues_AWSECS_WithConfig(t *testing.T) {
	m := DefaultMapper{}

	comps := &Components{AWSECS: ptrBool(true)}
	cfg := configWithAWSECS(awsECSCfgInput{
		EnableContainerInsights: ptrBool(true),
		CapacityProviders:       []string{"FARGATE", "FARGATE_SPOT"},
		DefaultCapacityProvider: "FARGATE_SPOT",
		EnableServiceConnect:    ptrBool(false),
	})

	vals, err := m.BuildModuleValues(KeyAWSECS, comps, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	assert.Equal(t, true, vals["enable_container_insights"])
	assert.Equal(t, []any{"FARGATE", "FARGATE_SPOT"}, vals["capacity_providers"])
	assert.Equal(t, "FARGATE_SPOT", vals["default_capacity_provider"])
	assert.Equal(t, false, vals["enable_service_connect"])
}

func TestBuildModuleValues_AWSECS_PartialConfig(t *testing.T) {
	m := DefaultMapper{}

	// Only CapacityProviders set; bool pointers left nil to exercise nil guards.
	comps := &Components{AWSECS: ptrBool(true)}
	cfg := configWithAWSECS(awsECSCfgInput{CapacityProviders: []string{"FARGATE"}})

	vals, err := m.BuildModuleValues(KeyAWSECS, comps, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	// CapacityProviders should be set
	assert.Equal(t, []any{"FARGATE"}, vals["capacity_providers"])

	// Nil bool pointers should NOT produce keys in the output
	_, hasInsights := vals["enable_container_insights"]
	assert.False(t, hasInsights, "nil EnableContainerInsights should not appear")
	_, hasConnect := vals["enable_service_connect"]
	assert.False(t, hasConnect, "nil EnableServiceConnect should not appear")
	// Empty string should not appear
	_, hasDefault := vals["default_capacity_provider"]
	assert.False(t, hasDefault, "empty DefaultCapacityProvider should not appear")
}

// TestBuildModuleValues_AWSEC2_CpuArchPrecedence locks the precedence rule
// documented on the deprecated Components.CpuArch field: per-component AWSEC2
// wins; CpuArch is only consulted as a fallback. See issue #86.
func TestBuildModuleValues_AWSEC2_CpuArchPrecedence(t *testing.T) {
	m := DefaultMapper{}

	t.Run("per-component AWSEC2 wins over deprecated CpuArch", func(t *testing.T) {
		comps := &Components{CpuArch: "Intel", AWSEC2: "ARM"}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, "arm64", vals["arch"], "AWSEC2=ARM must win over CpuArch=Intel")
	})

	t.Run("AWSEC2 arch match is case-insensitive (locks EqualFold)", func(t *testing.T) {
		// Lowercase variants of "ARM" must still map to arm64 — guards against
		// a careless switch to == that would silently emit x86_64 instead.
		comps := &Components{AWSEC2: "arm"}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, "arm64", vals["arch"], "AWSEC2='arm' must be matched case-insensitively")
	})

	t.Run("deprecated CpuArch=ARM used as fallback when AWSEC2 empty", func(t *testing.T) {
		comps := &Components{CpuArch: "ARM"}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, "arm64", vals["arch"], "CpuArch fallback should emit arm64")
		// t4g.medium is the arm64 default instance type; see the fallback
		// block around mapper.go:309-314 ("Default instance type based on
		// architecture if not explicitly configured").
		assert.Equal(t, "t4g.medium", vals["instance_type"], "arm64 fallback should default to t4g.medium")
	})

	t.Run("deprecated CpuArch=Intel used as fallback when AWSEC2 empty", func(t *testing.T) {
		comps := &Components{CpuArch: "Intel"}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		assert.Equal(t, "x86_64", vals["arch"], "CpuArch fallback should emit x86_64")
	})

	t.Run("no arch set anywhere leaves arch unset", func(t *testing.T) {
		comps := &Components{}
		vals, err := m.BuildModuleValues(KeyAWSEC2, comps, nil, "", "")
		require.NoError(t, err)
		_, hasArch := vals["arch"]
		assert.False(t, hasArch, "no arch hint should leave arch unset")
	})
}

// TestBuildModuleValues_Postgres_RDSConfig pins the cfg.AWSRDS → module.rds
// mapping. Previously the kitchen-sink integration test exercised these
// branches but asserted nothing on the mapper output; the fixture rename
// in #122 (`awsKitchenSinkCfgWithReadReplicas`) made the gap visible.
// Legacy→prefixed RDS migration is covered by types/Normalize tests and
// TestComposeSingle_NormalizesLegacyConfig (integration at the compose
// boundary); this mapper test reads AWS-prefixed fields only.
func TestBuildModuleValues_Postgres_RDSConfig(t *testing.T) {
	m := DefaultMapper{}

	// The RDS module's actual variable names are instance_class /
	// read_replica_count / allocated_storage. Earlier versions emitted
	// node_cpu_size / num_read_nodes / storage_size — none of which the
	// module declared, so user picks were silently dropped. See upstream
	// issue #131.

	t.Run("ReadReplicas set emits read_replica_count (number)", func(t *testing.T) {
		cfg := &Config{AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{ReadReplicas: "2"}}
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 2, vals["read_replica_count"])
		_, hasOld := vals["num_read_nodes"]
		assert.False(t, hasOld, "num_read_nodes is the pre-fix key — must not be emitted")
	})

	t.Run("ReadReplicas unset leaves read_replica_count unset", func(t *testing.T) {
		cfg := &Config{AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.2xlarge"}}
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasKey := vals["read_replica_count"]
		assert.False(t, hasKey, "unset ReadReplicas should not emit read_replica_count")
	})

	t.Run("CPUSize and StorageSize map to instance_class and allocated_storage", func(t *testing.T) {
		cfg := &Config{AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "db.m7i.2xlarge", StorageSize: "20"}}
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "db.m7i.2xlarge", vals["instance_class"])
		assert.Equal(t, 20, vals["allocated_storage"])
		_, hasOldCPU := vals["node_cpu_size"]
		_, hasOldStorage := vals["storage_size"]
		assert.False(t, hasOldCPU, "node_cpu_size is the pre-fix key — must not be emitted")
		assert.False(t, hasOldStorage, "storage_size is the pre-fix key — RDS module variable is allocated_storage")
	})
}

// TestBuildModuleValues_IgnoresAWSCloudfrontCachePathsFallback pins that
// the mapper does not read the deprecated AWSCloudfront.CachePaths sub-field.
// The within-AWSCloudfront CachePaths→OriginPath migration lives in
// Config.Normalize only (see TestConfig_Normalize_CachePathsMigration).
func TestBuildModuleValues_IgnoresAWSCloudfrontCachePathsFallback(t *testing.T) {
	m := DefaultMapper{}
	path := "/legacy"
	vals, err := m.BuildModuleValues(
		KeyAWSCloudfront,
		nil,
		&Config{AWSCloudfront: &struct {
			DefaultTtl *string `json:"defaultTtl,omitempty"`
			OriginPath *string `json:"originPath,omitempty"`
			CachePaths *string `json:"cachePaths,omitempty"`
		}{CachePaths: &path}},
		"", "",
	)
	require.NoError(t, err)
	_, present := vals["origin_path"]
	assert.False(t, present,
		"unnormalized AWSCloudfront.CachePaths must not reach mapper output as origin_path")
}

// TestBuildModuleValues_AWSBackups_DefaultRule pins the mapper's
// cfg.AWSBackups.{EC2,RDS,ElastiCache,DynamoDB,S3} → default_rule mapping
// after the Phase 3b rewrite from map-iteration to typed sub-struct reads.
// Covers: cron rank precedence (1h > 4h > 24h), maxRetention aggregation,
// comps-gating (cfg detail ignored when component disabled), 30-day
// retention fallback, and the daily-at-03:00-UTC schedule fallback.
func TestBuildModuleValues_AWSBackups_DefaultRule(t *testing.T) {
	m := DefaultMapper{}

	// Local struct-literal helpers keep the table below readable — every
	// sub-struct on Config.AWSBackups has the same three fields.
	type det = struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	}

	ec2Det := func(f, r int) *det { return &det{FrequencyHours: f, RetentionDays: r} }

	t.Run("single EC2 detail emits hourly cron and retention", func(t *testing.T) {
		comps := &Components{AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{EC2: ptrBool(true)}}
		cfg := &Config{AWSBackups: &struct {
			EC2         *det `json:"aws_ec2,omitempty"`
			RDS         *det `json:"aws_rds,omitempty"`
			ElastiCache *det `json:"aws_elasticache,omitempty"`
			DynamoDB    *det `json:"aws_dynamodb,omitempty"`
			S3          *det `json:"aws_s3,omitempty"`
		}{EC2: ec2Det(1, 7)}}
		vals, err := m.BuildModuleValues(KeyAWSBackups, comps, cfg, "", "")
		require.NoError(t, err)

		rule, ok := vals["default_rule"].(map[string]any)
		require.True(t, ok, "default_rule must be a map")
		assert.Equal(t, "cron(0 0 * * ? *)", rule["schedule_expression"],
			"frequency=1h must produce hourly cron")
		assert.Equal(t, 7, rule["retention_days"])
		assert.Equal(t, 0, rule["cold_storage_after_days"])
	})

	t.Run("highest-frequency service wins (1h beats 4h beats 24h)", func(t *testing.T) {
		// Retention is highest on the 24h service; schedule tracks the
		// 1h service. The test asserts the two reductions are independent.
		comps := &Components{AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{EC2: ptrBool(true), RDS: ptrBool(true), S3: ptrBool(true)}}
		cfg := &Config{AWSBackups: &struct {
			EC2         *det `json:"aws_ec2,omitempty"`
			RDS         *det `json:"aws_rds,omitempty"`
			ElastiCache *det `json:"aws_elasticache,omitempty"`
			DynamoDB    *det `json:"aws_dynamodb,omitempty"`
			S3          *det `json:"aws_s3,omitempty"`
		}{
			EC2: ec2Det(1, 7),   // best schedule
			RDS: ec2Det(4, 14),  // middling
			S3:  ec2Det(24, 90), // worst schedule, best retention
		}}
		vals, err := m.BuildModuleValues(KeyAWSBackups, comps, cfg, "", "")
		require.NoError(t, err)

		rule := vals["default_rule"].(map[string]any)
		assert.Equal(t, "cron(0 0 * * ? *)", rule["schedule_expression"],
			"1h EC2 must win the cron-rank contest")
		assert.Equal(t, 90, rule["retention_days"],
			"retention_days must be max across enabled services")
	})

	t.Run("disabled component with populated detail is ignored", func(t *testing.T) {
		// RDS detail exists in cfg but comps.AWSBackups.RDS is unset —
		// the mapper must not let the detail affect default_rule.
		comps := &Components{AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{EC2: ptrBool(true)}} // only EC2 enabled
		cfg := &Config{AWSBackups: &struct {
			EC2         *det `json:"aws_ec2,omitempty"`
			RDS         *det `json:"aws_rds,omitempty"`
			ElastiCache *det `json:"aws_elasticache,omitempty"`
			DynamoDB    *det `json:"aws_dynamodb,omitempty"`
			S3          *det `json:"aws_s3,omitempty"`
		}{
			EC2: ec2Det(24, 10),
			RDS: ec2Det(1, 365), // must be ignored — RDS comp disabled
		}}
		vals, err := m.BuildModuleValues(KeyAWSBackups, comps, cfg, "", "")
		require.NoError(t, err)

		rule := vals["default_rule"].(map[string]any)
		assert.Equal(t, "cron(0 3 * * ? *)", rule["schedule_expression"],
			"disabled RDS detail must not influence schedule — expected EC2's 24h cron")
		assert.Equal(t, 10, rule["retention_days"],
			"disabled RDS detail must not influence retention")
	})

	t.Run("no cfg.AWSBackups yields fallback schedule and 30-day retention", func(t *testing.T) {
		comps := &Components{AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{EC2: ptrBool(true)}}
		vals, err := m.BuildModuleValues(KeyAWSBackups, comps, &Config{}, "", "")
		require.NoError(t, err)

		rule := vals["default_rule"].(map[string]any)
		assert.Equal(t, "cron(0 3 * * ? *)", rule["schedule_expression"],
			"no cfg.AWSBackups must fall back to daily 03:00 UTC cron")
		assert.Equal(t, 30, rule["retention_days"],
			"no retention detail must fall back to 30 days")
	})

	t.Run("all five services covered", func(t *testing.T) {
		// Mutation guard: ensure each service's detail is read. Assign a
		// unique frequency to every service (all rank-0 unknowns except
		// one rank-3 winner per subcase) so a deleted service branch
		// leaves the wrong retention.
		for _, svc := range []string{"EC2", "RDS", "ElastiCache", "DynamoDB", "S3"} {
			t.Run(svc, func(t *testing.T) {
				b := &struct {
					EC2         *bool `json:"aws_ec2,omitempty"`
					RDS         *bool `json:"aws_rds,omitempty"`
					ElastiCache *bool `json:"aws_elasticache,omitempty"`
					DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
					S3          *bool `json:"aws_s3,omitempty"`
				}{}
				c := &struct {
					EC2         *det `json:"aws_ec2,omitempty"`
					RDS         *det `json:"aws_rds,omitempty"`
					ElastiCache *det `json:"aws_elasticache,omitempty"`
					DynamoDB    *det `json:"aws_dynamodb,omitempty"`
					S3          *det `json:"aws_s3,omitempty"`
				}{}
				// Assign a distinctive retention (77) to only this service.
				switch svc {
				case "EC2":
					b.EC2 = ptrBool(true)
					c.EC2 = ec2Det(24, 77)
				case "RDS":
					b.RDS = ptrBool(true)
					c.RDS = ec2Det(24, 77)
				case "ElastiCache":
					b.ElastiCache = ptrBool(true)
					c.ElastiCache = ec2Det(24, 77)
				case "DynamoDB":
					b.DynamoDB = ptrBool(true)
					c.DynamoDB = ec2Det(24, 77)
				case "S3":
					b.S3 = ptrBool(true)
					c.S3 = ec2Det(24, 77)
				}
				vals, err := m.BuildModuleValues(
					KeyAWSBackups,
					&Components{AWSBackups: b},
					&Config{AWSBackups: c},
					"", "",
				)
				require.NoError(t, err)
				rule := vals["default_rule"].(map[string]any)
				assert.Equal(t, 77, rule["retention_days"],
					"%s branch of AWSBackups mapper must be live", svc)
			})
		}
	})
}
