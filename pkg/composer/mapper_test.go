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
		{"EKS (V2 field)", &Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}},
		{"RDS (V2 field)", &Components{AWSVPC: "Public VPC", AWSRDS: boolPtr(true)}},
		{"ElastiCache (V2 field)", &Components{AWSVPC: "Public VPC", AWSElastiCache: boolPtr(true)}},
		{"OpenSearch (V2 field)", &Components{AWSVPC: "Public VPC", AWSOpenSearch: boolPtr(true)}},
		{"EC2 node group", &Components{AWSVPC: "Public VPC", AWSEC2: "Intel"}},
		{"legacy Postgres field", &Components{AWSVPC: "Public VPC", Postgres: boolPtr(true)}},
		{"legacy ElastiCache field", &Components{AWSVPC: "Public VPC", ElastiCache: boolPtr(true)}},
		{"legacy OpenSearch field", &Components{AWSVPC: "Public VPC", OpenSearch: boolPtr(true)}},
		{"ECS (V2 field)", &Components{AWSVPC: "Public VPC", AWSECS: boolPtr(true)}},
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
		vals, err := m.BuildModuleValues(KeyVPC, comps, nil, "test", "us-east-1")
		require.NoError(t, err)
		assertVPCCaseRan(t, vals)
		_, hasPrivate := vals["enable_private_subnets"]
		_, hasNat := vals["enable_nat_gateway"]
		assert.False(t, hasPrivate)
		assert.False(t, hasNat)
	})

	t.Run("legacy KeyVPC with Public VPC also works", func(t *testing.T) {
		comps := &Components{AWSVPC: "Public VPC"}
		vals, err := m.BuildModuleValues(KeyVPC, comps, nil, "test", "us-east-1")
		require.NoError(t, err)
		assertVPCCaseRan(t, vals)
		assert.Equal(t, false, vals["enable_private_subnets"])
		assert.Equal(t, false, vals["enable_nat_gateway"])
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
	assert.True(t, stackNeedsPrivateSubnets(&Components{Postgres: boolPtr(true)}), "legacy Postgres")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSElastiCache: boolPtr(true)}), "AWSElastiCache")
	assert.True(t, stackNeedsPrivateSubnets(&Components{ElastiCache: boolPtr(true)}), "legacy ElastiCache")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSOpenSearch: boolPtr(true)}), "AWSOpenSearch")
	assert.True(t, stackNeedsPrivateSubnets(&Components{OpenSearch: boolPtr(true)}), "legacy OpenSearch")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSEC2: "Intel"}), "AWSEC2 Intel")
	assert.True(t, stackNeedsPrivateSubnets(&Components{AWSEC2: "ARM"}), "AWSEC2 ARM")
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

	t.Run("EnableNATGateway=false with legacy Postgres returns ValidationError", func(t *testing.T) {
		comps := &Components{Postgres: boolPtr(true)}
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
// Reads the actual preset variables.tf via DiscoverModuleVars and asserts every
// key the mapper writes for KeyAWSVPC with all AWSVPC knobs set is declared.
func TestBuildModuleValues_VPC_MapperHCLContract(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	intPtr := func(v int) *int { return &v }

	presets, err := newTestClient().GetPresetFiles("aws/vpc")
	require.NoError(t, err)
	declared, err := DiscoverModuleVars(presets)
	require.NoError(t, err)
	declaredSet := make(map[string]bool, len(declared))
	for _, v := range declared {
		declaredSet[v.Name] = true
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

// TestBuildModuleValues_V2KeyNormalization verifies that calling BuildModuleValues
// with a V2 key (e.g., KeyAWSWAF) produces the same output as calling with the
// legacy key (e.g., KeyWAF). This catches missing case arms in the normalization switch.
func TestBuildModuleValues_V2KeyNormalization(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}
	for legacy, v2 := range LegacyToV2Key {
		t.Run(string(v2), func(t *testing.T) {
			t.Parallel()
			valsLegacy, err := m.BuildModuleValues(legacy, &Components{}, &Config{}, "test", "us-east-1")
			require.NoError(t, err)
			valsV2, err := m.BuildModuleValues(v2, &Components{}, &Config{}, "test", "us-east-1")
			require.NoError(t, err)
			assert.Equal(t, valsLegacy, valsV2,
				"V2 key %s should produce same values as legacy key %s", v2, legacy)
		})
	}
}

func TestBuildModuleValues_CloudWatchLogs_Retention(t *testing.T) {
	m := DefaultMapper{}

	t.Run("retention days integer set directly", func(t *testing.T) {
		cfg := &Config{
			CloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 7,
			},
		}
		vals, err := m.BuildModuleValues(KeyCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 7, vals["retention_in_days"])
		_, hasOldKey := vals["retention"]
		assert.False(t, hasOldKey, "should not emit old 'retention' key")
	})

	t.Run("90 days retention", func(t *testing.T) {
		cfg := &Config{
			CloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 90,
			},
		}
		vals, err := m.BuildModuleValues(KeyCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 90, vals["retention_in_days"])
	})

	t.Run("V2 AWSCloudWatchLogs config is also used", func(t *testing.T) {
		cfg := &Config{
			AWSCloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 365,
			},
		}
		vals, err := m.BuildModuleValues(KeyCloudWatchLogs, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, 365, vals["retention_in_days"])
	})

	t.Run("zero retention does not set key", func(t *testing.T) {
		cfg := &Config{
			CloudWatchLogs: &struct {
				RetentionDays int `json:"retentionDays,omitempty"`
			}{
				RetentionDays: 0,
			},
		}
		vals, err := m.BuildModuleValues(KeyCloudWatchLogs, nil, cfg, "", "")
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
			Cloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{OriginPath: &path},
		}
		vals, err := m.BuildModuleValues(KeyCloudfront, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "/assets", vals["origin_path"])
	})

	t.Run("deprecated cachePaths falls back to origin_path", func(t *testing.T) {
		path := "/legacy"
		cfg := &Config{
			Cloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{CachePaths: &path},
		}
		vals, err := m.BuildModuleValues(KeyCloudfront, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "/legacy", vals["origin_path"])
	})

	t.Run("originPath takes precedence over cachePaths", func(t *testing.T) {
		newPath := "/new"
		oldPath := "/old"
		cfg := &Config{
			Cloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{OriginPath: &newPath, CachePaths: &oldPath},
		}
		vals, err := m.BuildModuleValues(KeyCloudfront, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "/new", vals["origin_path"])
	})
}

func TestConfig_Normalize_CachePathsMigration(t *testing.T) {
	t.Run("legacy Cloudfront CachePaths migrates to AWSCloudfront OriginPath", func(t *testing.T) {
		path := "/legacy"
		cfg := Config{
			Cloud: "AWS",
			Cloudfront: &struct {
				DefaultTtl *string `json:"defaultTtl,omitempty"`
				OriginPath *string `json:"originPath,omitempty"`
				CachePaths *string `json:"cachePaths,omitempty"`
			}{CachePaths: &path},
		}
		cfg.Normalize()
		// Normalize migrates legacy Cloudfront → AWSCloudfront, then clears legacy fields
		require.NotNil(t, cfg.AWSCloudfront)
		require.NotNil(t, cfg.AWSCloudfront.OriginPath)
		assert.Equal(t, "/legacy", *cfg.AWSCloudfront.OriginPath)
		assert.Nil(t, cfg.Cloudfront, "legacy Cloudfront should be nil after Normalize")
	})

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
		// AWSCloudfront.CachePaths → Cloudfront.OriginPath (reverse sync), then cleared
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
			Cloudfront: &struct {
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
