package composer

// Tests for the exported effective-VPC-networking decision.
//
// Motivation (downstream defect, luthersystems/reliable, verified live
// 2026-07-26): the NAT decision was implicit in DefaultMapper's KeyAWSVPC
// case, so reliable re-derived it and quoted "$0.00 — no NAT Gateway" for a
// stack the composer emitted with enable_nat_gateway=true +
// single_nat_gateway=false — 2 NAT gateways, ~$64.80/mo unpriced.
//
// Two layers of coverage:
//
//  1. TestEffectiveVPCNetworking — the rules themselves, across every shape
//     (each private-subnet component alone, explicit enable/disable, the
//     #805/#806 heal, the #389 stale coercion, single-NAT / AZ-count variants).
//  2. TestVPCNetworkingMapperParity — the CAN'T-DIVERGE guard: for each shape,
//     the real BuildModuleValues(KeyAWSVPC, …) output must agree with the
//     exported decision, accounting for keys the mapper deliberately omits
//     (those defer to the preset's HCL default, which the decision reports).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// privateSubnetComponentShapes returns one Components per component that
// triggers stackNeedsPrivateSubnets, each ALONE, so a future edit that drops
// one from the private-subnet set fails here rather than silently under-
// pricing that stack. Kept in sync with stackNeedsPrivateSubnets by
// TestPrivateSubnetShapesCoverStackNeedsPrivateSubnets below.
func privateSubnetComponentShapes(vpcLabel string) map[string]*Components {
	return map[string]*Components{
		"EKS":         {AWSVPC: vpcLabel, AWSEKS: boolPtr(true)},
		"ECS":         {AWSVPC: vpcLabel, AWSECS: boolPtr(true)},
		"RDS":         {AWSVPC: vpcLabel, AWSRDS: boolPtr(true)},
		"ElastiCache": {AWSVPC: vpcLabel, AWSElastiCache: boolPtr(true)},
		"OpenSearch":  {AWSVPC: vpcLabel, AWSOpenSearch: boolPtr(true)},
		"EC2":         {AWSVPC: vpcLabel, AWSEC2: "Intel"},
	}
}

func TestEffectiveVPCNetworking(t *testing.T) {
	t.Parallel()

	// --- Each private-subnet component alone turns NAT on, for both labels ---
	t.Run("each private-subnet component alone forces NAT on", func(t *testing.T) {
		t.Parallel()
		for _, label := range []string{"Public VPC", "Private VPC", ""} {
			for name, comps := range privateSubnetComponentShapes(label) {
				t.Run(name+"/"+label, func(t *testing.T) {
					d := EffectiveVPCNetworking(comps, nil)
					assert.True(t, d.NeedsPrivateSubnets, "%s must need private subnets", name)
					assert.True(t, d.EnableNATGateway, "%s must get NAT on", name)
					assert.True(t, d.EnablePrivateSubnets, "%s must get private subnets", name)
					assert.Equal(t, VPCNATReasonPrivateWorkloadDefault, d.Reason,
						"no authored enable_nat_gateway + private workload => #393 default-on")
					// az_count default 2, single_nat default false => 2 NATs.
					// This is exactly the shape reliable priced at $0.00.
					assert.Equal(t, 2, d.NATGatewayCount,
						"default topology is one NAT per AZ across az_count=2 — the ~$64.80/mo reliable missed")
					assert.False(t, d.OverrodeExplicitSetting(), "nothing explicit to override")
				})
			}
		}
	})

	// --- Explicit false + private workload => healed on (#805/#806) ---
	t.Run("explicit false + private workload is healed on", func(t *testing.T) {
		t.Parallel()
		for name, comps := range privateSubnetComponentShapes("Public VPC") {
			t.Run(name, func(t *testing.T) {
				d := EffectiveVPCNetworking(comps, cfgWithAWSVPC(nil, boolPtr(false), nil))
				assert.True(t, d.EnableNATGateway, "%s: frozen false must heal to on", name)
				assert.True(t, d.EnablePrivateSubnets,
					"%s: heal pins private subnets on so the outputs.tf NAT invariant holds", name)
				assert.Equal(t, VPCNATReasonFrozenNATHealed, d.Reason)
				assert.Equal(t, 2, d.NATGatewayCount, "healed NAT still costs money")
				assert.True(t, d.OverrodeExplicitSetting(), "the user's explicit false was overridden")
			})
		}
	})

	// --- Explicit false + NO private workload => off ---
	t.Run("explicit false without a private workload stays off", func(t *testing.T) {
		t.Parallel()
		for _, label := range []string{"Public VPC", "Private VPC", ""} {
			t.Run(label, func(t *testing.T) {
				d := EffectiveVPCNetworking(&Components{AWSVPC: label}, cfgWithAWSVPC(nil, boolPtr(false), nil))
				assert.False(t, d.EnableNATGateway)
				assert.Equal(t, 0, d.NATGatewayCount, "NAT off must price as zero gateways")
				assert.Equal(t, VPCNATReasonExplicitDisable, d.Reason)
				assert.False(t, d.OverrodeExplicitSetting())
			})
		}
	})

	// --- Explicit true ---
	t.Run("explicit true on a stack that can carry NAT turns it on", func(t *testing.T) {
		t.Parallel()
		// Private VPC label, no private workload: NAT is legal (private
		// subnets default on), so the explicit true is honored.
		d := EffectiveVPCNetworking(&Components{AWSVPC: "Private VPC"}, cfgWithAWSVPC(nil, boolPtr(true), nil))
		assert.True(t, d.EnableNATGateway)
		assert.Equal(t, VPCNATReasonExplicitEnable, d.Reason)
		assert.Equal(t, 2, d.NATGatewayCount)

		// With a private workload the explicit true is also honored, and the
		// reason stays explicit (not the #393 default).
		d2 := EffectiveVPCNetworking(&Components{AWSVPC: "Private VPC", AWSRDS: boolPtr(true)},
			cfgWithAWSVPC(nil, boolPtr(true), nil))
		assert.True(t, d2.EnableNATGateway)
		assert.Equal(t, VPCNATReasonExplicitEnable, d2.Reason)
	})

	// --- #389: explicit true on a public-only VPC is coerced off ---
	t.Run("explicit true on a Public VPC with no private workload is coerced off", func(t *testing.T) {
		t.Parallel()
		d := EffectiveVPCNetworking(&Components{AWSVPC: "Public VPC"}, cfgWithAWSVPC(nil, boolPtr(true), nil))
		assert.False(t, d.EnableNATGateway,
			"NAT routes would attach to an empty private route table (#389)")
		assert.False(t, d.EnablePrivateSubnets, "public-only VPC has no private subnets")
		assert.Equal(t, 0, d.NATGatewayCount)
		assert.Equal(t, VPCNATReasonStaleNATCoercedOff, d.Reason)
		assert.True(t, d.OverrodeExplicitSetting(), "the user's explicit true was overridden")
	})

	// --- No VPC / no compute / nil inputs => off ---
	t.Run("no private workload and no opinion leaves NAT off", func(t *testing.T) {
		t.Parallel()
		cases := map[string]struct {
			comps *Components
			cfg   *Config
		}{
			"nil comps and cfg":     {nil, nil},
			"empty comps":           {&Components{}, nil},
			"public VPC only":       {&Components{AWSVPC: "Public VPC"}, nil},
			"private VPC only":      {&Components{AWSVPC: "Private VPC"}, nil},
			"S3 only (no compute)":  {&Components{AWSS3: boolPtr(true)}, nil},
			"cfg with empty AWSVPC": {&Components{}, cfgWithAWSVPC(nil, nil, nil)},
			"explicitly-false EKS":  {&Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(false)}, nil},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				d := EffectiveVPCNetworking(tc.comps, tc.cfg)
				assert.False(t, d.NeedsPrivateSubnets)
				assert.False(t, d.EnableNATGateway)
				assert.Equal(t, 0, d.NATGatewayCount)
				assert.Equal(t, VPCNATReasonNoPrivateWorkload, d.Reason)
			})
		}
	})

	// --- Topology knobs: single NAT and AZ count drive NATGatewayCount ---
	t.Run("single_nat_gateway and az_count drive the gateway count", func(t *testing.T) {
		t.Parallel()
		privateWorkload := &Components{AWSVPC: "Private VPC", AWSRDS: boolPtr(true)}

		cases := []struct {
			name      string
			single    *bool
			az        *int
			wantCount int
			wantAZ    int
			wantSolo  bool
		}{
			{"defaults (one per AZ, az=2)", nil, nil, 2, 2, false},
			{"single NAT collapses to 1", boolPtr(true), nil, 1, 2, true},
			{"single NAT ignores az_count", boolPtr(true), intPtr(4), 1, 4, true},
			{"one per AZ with az=3", boolPtr(false), intPtr(3), 3, 3, false},
			{"one per AZ with az=1", nil, intPtr(1), 1, 1, false},
			{"one per AZ with az=6", nil, intPtr(6), 6, 6, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				d := EffectiveVPCNetworking(privateWorkload, cfgWithAWSVPC(tc.single, nil, tc.az))
				require.True(t, d.EnableNATGateway, "private workload keeps NAT on")
				assert.Equal(t, tc.wantCount, d.NATGatewayCount)
				assert.Equal(t, tc.wantAZ, d.AZCount)
				assert.Equal(t, tc.wantSolo, d.SingleNATGateway)
			})
		}

		// Topology knobs are reported even when NAT is off, but the count is 0.
		off := EffectiveVPCNetworking(&Components{AWSVPC: "Public VPC"}, cfgWithAWSVPC(boolPtr(false), nil, intPtr(3)))
		assert.False(t, off.EnableNATGateway)
		assert.Equal(t, 3, off.AZCount, "az_count is still the effective AZ span")
		assert.Equal(t, 0, off.NATGatewayCount, "no gateways to price when NAT is off")
	})

	// --- Normalize is the caller's job; the decision reads what it is given ---
	t.Run("respects Normalize's opposite-cloud clearing", func(t *testing.T) {
		t.Parallel()
		// Before Normalize, the stray AWS RDS flag reads as a private workload.
		comps := &Components{Cloud: "GCP", AWSVPC: "Private VPC", AWSRDS: boolPtr(true)}
		assert.True(t, EffectiveVPCNetworking(comps, nil).EnableNATGateway,
			"un-normalized input is taken at face value")

		// ComposeStack/ComposeSingle Normalize at entry, which clears the
		// opposite cloud's components — and the decision follows.
		comps.Normalize()
		d := EffectiveVPCNetworking(comps, nil)
		assert.False(t, d.NeedsPrivateSubnets, "Normalize cleared the AWS components on a GCP stack")
		assert.False(t, d.EnableNATGateway)
		assert.Equal(t, 0, d.NATGatewayCount)
	})
}

// TestPrivateSubnetShapesCoverStackNeedsPrivateSubnets is a drift guard: every
// component the fixture claims triggers private subnets really does, and the
// negative control really does not. If someone adds a seventh private-subnet
// component to stackNeedsPrivateSubnets without adding it to the fixture, the
// mapper-parity and decision tables above would silently stop covering it —
// this pins the intent at least for the six known today.
func TestPrivateSubnetShapesCoverStackNeedsPrivateSubnets(t *testing.T) {
	t.Parallel()
	for name, comps := range privateSubnetComponentShapes("Public VPC") {
		assert.True(t, stackNeedsPrivateSubnets(comps),
			"%s is listed as a private-subnet shape but stackNeedsPrivateSubnets disagrees", name)
	}
	assert.False(t, stackNeedsPrivateSubnets(&Components{AWSVPC: "Public VPC", AWSS3: boolPtr(true)}),
		"S3 must not pull in private subnets")
}

// vpcParityShapes enumerates the (Components, Config) pairs exercised by both
// the decision table and the mapper-parity guard.
func vpcParityShapes(t *testing.T) map[string]struct {
	comps *Components
	cfg   *Config
} {
	t.Helper()
	shapes := map[string]struct {
		comps *Components
		cfg   *Config
	}{
		"nil comps, nil cfg":                        {nil, nil},
		"empty comps":                               {&Components{}, nil},
		"public VPC alone":                          {&Components{AWSVPC: "Public VPC"}, nil},
		"private VPC alone":                         {&Components{AWSVPC: "Private VPC"}, nil},
		"public VPC + explicit NAT true (#389)":     {&Components{AWSVPC: "Public VPC"}, cfgWithAWSVPC(nil, boolPtr(true), nil)},
		"public VPC + explicit NAT false":           {&Components{AWSVPC: "Public VPC"}, cfgWithAWSVPC(nil, boolPtr(false), nil)},
		"private VPC + explicit NAT true":           {&Components{AWSVPC: "Private VPC"}, cfgWithAWSVPC(nil, boolPtr(true), nil)},
		"private VPC + explicit NAT false":          {&Components{AWSVPC: "Private VPC"}, cfgWithAWSVPC(nil, boolPtr(false), nil)},
		"single NAT + az 3, no workload":            {&Components{AWSVPC: "Private VPC"}, cfgWithAWSVPC(boolPtr(true), nil, intPtr(3))},
		"one-per-AZ + az 4 + explicit true":         {&Components{AWSVPC: "Private VPC"}, cfgWithAWSVPC(boolPtr(false), boolPtr(true), intPtr(4))},
		"private workload + single NAT":             {&Components{AWSVPC: "Private VPC", AWSRDS: boolPtr(true)}, cfgWithAWSVPC(boolPtr(true), nil, nil)},
		"private workload + az 3":                   {&Components{AWSVPC: "Private VPC", AWSRDS: boolPtr(true)}, cfgWithAWSVPC(nil, nil, intPtr(3))},
		"private workload + explicit true":          {&Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}, cfgWithAWSVPC(nil, boolPtr(true), nil)},
		"private workload + frozen false (#805)":    {&Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}, cfgWithAWSVPC(nil, boolPtr(false), nil)},
		"private workload + frozen false + az 3":    {&Components{AWSVPC: "Public VPC", AWSRDS: boolPtr(true)}, cfgWithAWSVPC(boolPtr(false), boolPtr(false), intPtr(3))},
		"private workload, no VPC label":            {&Components{AWSEC2: "ARM"}, nil},
		"S3 only":                                   {&Components{AWSVPC: "Public VPC", AWSS3: boolPtr(true)}, nil},
		"empty AWSVPC config block":                 {&Components{AWSVPC: "Private VPC"}, cfgWithAWSVPC(nil, nil, nil)},
		"private workload + empty AWSVPC cfg block": {&Components{AWSVPC: "Private VPC", AWSECS: boolPtr(true)}, cfgWithAWSVPC(nil, nil, nil)},
	}
	// Every private-subnet component alone, with no config.
	for name, comps := range privateSubnetComponentShapes("Public VPC") {
		shapes["public VPC + "+name+" alone"] = struct {
			comps *Components
			cfg   *Config
		}{comps, nil}
	}
	return shapes
}

// TestVPCNetworkingMapperParity is the can't-diverge guard between the
// exported decision and what the mapper actually emits. For each shape it runs
// the REAL DefaultMapper.BuildModuleValues(KeyAWSVPC, …) and asserts that the
// three networking tfvars agree with EffectiveVPCNetworking — treating an
// ABSENT key as the preset's HCL default, because that is precisely the case
// where a consumer reading only the tfvars would draw the wrong conclusion.
//
// Without this test, someone could tweak the mapper's switch case (or the
// decision function) alone and reintroduce the reliable pricing defect: tfvars
// saying "2 NAT gateways" while the exported decision says "none".
func TestVPCNetworkingMapperParity(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}

	for name, shape := range vpcParityShapes(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := EffectiveVPCNetworking(shape.comps, shape.cfg)
			vals, err := m.BuildModuleValues(KeyAWSVPC, shape.comps, shape.cfg, "test", "us-east-1")
			require.NoError(t, err, "shape must compose cleanly")

			// boolFromVals reads an emitted bool tfvar, falling back to the
			// preset's HCL default when the mapper omitted the key. The type
			// assertion is deliberate: a stringified bool must fail loudly.
			boolFromVals := func(key string, def bool) bool {
				raw, ok := vals[key]
				if !ok {
					return def
				}
				b, isBool := raw.(bool)
				require.True(t, isBool, "%s must be emitted as a bool, got %T", key, raw)
				return b
			}
			intFromVals := func(key string, def int) int {
				raw, ok := vals[key]
				if !ok {
					return def
				}
				i, isInt := raw.(int)
				require.True(t, isInt, "%s must be emitted as an int, got %T", key, raw)
				return i
			}

			gotNAT := boolFromVals("enable_nat_gateway", defaultVPCEnableNATGateway)
			gotSingle := boolFromVals("single_nat_gateway", defaultVPCSingleNATGateway)
			gotPrivate := boolFromVals("enable_private_subnets", defaultVPCEnablePrivateSubnets)
			gotAZ := intFromVals("az_count", defaultVPCAZCount)

			assert.Equal(t, want.EnableNATGateway, gotNAT,
				"enable_nat_gateway in the emitted tfvars must equal the exported decision")
			assert.Equal(t, want.SingleNATGateway, gotSingle,
				"single_nat_gateway in the emitted tfvars must equal the exported decision")
			assert.Equal(t, want.EnablePrivateSubnets, gotPrivate,
				"enable_private_subnets in the emitted tfvars must equal the exported decision")
			assert.Equal(t, want.AZCount, gotAZ,
				"az_count in the emitted tfvars must equal the exported decision")

			// And the derived count the pricing path consumes.
			wantCount := 0
			if gotNAT {
				if gotSingle {
					wantCount = 1
				} else {
					wantCount = gotAZ
				}
			}
			assert.Equal(t, wantCount, want.NATGatewayCount,
				"NATGatewayCount must be derivable from the emitted tfvars")

			// The NAT invariant the aws/vpc outputs.tf enforces.
			if gotNAT {
				assert.True(t, gotPrivate,
					"enable_nat_gateway=true requires enable_private_subnets=true")
			}
		})
	}
}

// TestVPCNetworkingDefaultsMatchPreset is the drift guard on the mirrored HCL
// defaults: EffectiveVPCNetworking reports effective values for knobs the
// mapper deliberately omits from the tfvars, so these constants MUST track
// aws/vpc/variables.tf. Change the preset default and this test tells you to
// change the constant (otherwise the exported decision would quietly start
// lying about a stack it never wrote a tfvar for).
func TestVPCNetworkingDefaultsMatchPreset(t *testing.T) {
	t.Parallel()

	mod, err := InspectPreset(GetPresetPath("aws", KeyAWSVPC, nil))
	require.NoError(t, err)

	cases := []struct {
		variable string
		want     any
	}{
		{"enable_private_subnets", defaultVPCEnablePrivateSubnets},
		{"enable_nat_gateway", defaultVPCEnableNATGateway},
		{"single_nat_gateway", defaultVPCSingleNATGateway},
		{"az_count", defaultVPCAZCount},
	}
	for _, tc := range cases {
		t.Run(tc.variable, func(t *testing.T) {
			v, ok := mod.Variables[tc.variable]
			require.True(t, ok, "aws/vpc must declare var.%s", tc.variable)
			require.NotNil(t, v.Default, "var.%s must have an HCL default", tc.variable)

			switch want := tc.want.(type) {
			case bool:
				got, isBool := v.Default.(bool)
				require.True(t, isBool, "var.%s default must be a bool, got %T", tc.variable, v.Default)
				assert.Equal(t, want, got,
					"aws/vpc var.%s default drifted from the mirrored constant in vpc_networking.go", tc.variable)
			case int:
				// tfconfig decodes HCL numbers as json.Number-ish float64.
				got, isFloat := v.Default.(float64)
				require.True(t, isFloat, "var.%s default must be a number, got %T", tc.variable, v.Default)
				assert.Equal(t, float64(want), got,
					"aws/vpc var.%s default drifted from the mirrored constant in vpc_networking.go", tc.variable)
			}
		})
	}
}

// TestValidateAWSVPCNATHealed pins the informational issue that surfaces the
// #805/#806 heal to upstream callers: the user's stored enable_nat_gateway is
// NOT what got deployed, and NAT gateways are in the bill.
func TestValidateAWSVPCNATHealed(t *testing.T) {
	t.Parallel()

	t.Run("fires on the heal shape", func(t *testing.T) {
		t.Parallel()
		issues := ValidateAWSVPCNATHealed("aws",
			&Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)},
			cfgWithAWSVPC(nil, boolPtr(false), nil))
		require.Len(t, issues, 1)
		assert.Equal(t, "aws_vpc_nat_gateway_healed", issues[0].Code)
		assert.Equal(t, "cfg.aws_vpc.enable_nat_gateway", issues[0].Field)
		assert.Equal(t, "false", issues[0].Value)
		assert.NotEmpty(t, issues[0].Suggestion)
	})

	t.Run("silent on every non-heal shape", func(t *testing.T) {
		t.Parallel()
		for name, shape := range vpcParityShapes(t) {
			d := EffectiveVPCNetworking(shape.comps, shape.cfg)
			if d.Reason == VPCNATReasonFrozenNATHealed {
				continue
			}
			assert.Empty(t, ValidateAWSVPCNATHealed("aws", shape.comps, shape.cfg),
				"%s (reason %s) must not raise the heal issue", name, d.Reason)
		}
	})

	t.Run("no-op for non-AWS clouds", func(t *testing.T) {
		t.Parallel()
		comps := &Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}
		cfg := cfgWithAWSVPC(nil, boolPtr(false), nil)
		assert.Empty(t, ValidateAWSVPCNATHealed("gcp", comps, cfg))
		assert.Empty(t, ValidateAWSVPCNATHealed("", comps, cfg))
	})

	t.Run("reaches ValidateAll", func(t *testing.T) {
		t.Parallel()
		issues := ValidateAll(
			&Components{Cloud: "aws", AWSVPC: "Public VPC", AWSEKS: boolPtr(true)},
			cfgWithAWSVPC(nil, boolPtr(false), nil),
			nil, nil, nil, nil,
		)
		found := false
		for _, iss := range issues {
			if iss.Code == "aws_vpc_nat_gateway_healed" {
				found = true
			}
		}
		assert.True(t, found, "ValidateAll must aggregate the heal issue")
	})
}

// TestComposeSurfacesVPCNetworkingDecision pins the compose-result surface:
// a stack (or single module) carrying an aws/vpc module reports the decision,
// and a stack without one reports nil rather than a misleading zero value.
func TestComposeSurfacesVPCNetworkingDecision(t *testing.T) {
	t.Parallel()

	c := New()

	t.Run("stack with an implicit VPC reports the decision", func(t *testing.T) {
		t.Parallel()
		// RDS pulls in the VPC via ResolveDependenciesForCompose — the exact
		// shape reliable mispriced.
		res, err := c.ComposeStackWithIssues(ComposeStackOpts{
			Cloud:        "aws",
			SelectedKeys: []ComponentKey{KeyAWSRDS},
			Comps:        &Components{Cloud: "aws", AWSVPC: "Public VPC", AWSRDS: boolPtr(true)},
			Project:      "test",
			Region:       "us-east-1",
		})
		require.NoError(t, err)
		require.NotNil(t, res.VPCNetworking, "an aws/vpc-bearing stack must report its NAT decision")
		assert.True(t, res.VPCNetworking.EnableNATGateway)
		assert.Equal(t, 2, res.VPCNetworking.NATGatewayCount,
			"the composed stack really provisions 2 NAT gateways — pricing must see this")
		assert.Equal(t, VPCNATReasonPrivateWorkloadDefault, res.VPCNetworking.Reason)
	})

	t.Run("single aws_vpc compose reports the decision", func(t *testing.T) {
		t.Parallel()
		res, err := c.ComposeSingleWithIssues(ComposeSingleOpts{
			Cloud:   "aws",
			Key:     KeyAWSVPC,
			Comps:   &Components{Cloud: "aws", AWSVPC: "Public VPC"},
			Project: "test",
			Region:  "us-east-1",
		})
		require.NoError(t, err)
		require.NotNil(t, res.VPCNetworking)
		assert.False(t, res.VPCNetworking.EnableNATGateway)
		assert.Equal(t, 0, res.VPCNetworking.NATGatewayCount)
		assert.Equal(t, VPCNATReasonNoPrivateWorkload, res.VPCNetworking.Reason)
	})

	t.Run("VPC-less stack reports nil, not a zero decision", func(t *testing.T) {
		t.Parallel()
		res, err := c.ComposeSingleWithIssues(ComposeSingleOpts{
			Cloud:   "aws",
			Key:     KeyAWSS3,
			Comps:   &Components{Cloud: "aws", AWSS3: boolPtr(true)},
			Project: "test",
			Region:  "us-east-1",
		})
		require.NoError(t, err)
		assert.Nil(t, res.VPCNetworking,
			"nil means 'no VPC decision to report' — never conflate it with 'NAT off'")
	})
}
