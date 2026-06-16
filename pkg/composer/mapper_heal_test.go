package composer

// Regression tests for the "heal frozen values" decision: PR #805 fixed the
// DEFAULTING path so new stacks get good values, but existing stack snapshots
// froze two known-always-invalid values into their stored config, which
// reliable composes verbatim (no re-derive). The mapper now COERCES those two
// values instead of erroring, healing existing sessions at compose time:
//
//  1. enable_nat_gateway=false on a stack that needs private subnets -> NAT on
//  2. a bare-integer Lambda timeout ("30") -> unit-suffixed ("30s")
//
// These tests pin the new coercion contract (and reverse the #805 fail-fast —
// see the inverted tests in coherence_test.go / mapper_test.go /
// mapper_audit_test.go).

import (
	"bytes"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildModuleValues_VPC_NATHeal covers every needs-private component shape
// (RDS plus a table over EKS/ECS/ElastiCache/OpenSearch/EC2): an explicit
// Config EnableNATGateway=false must be HEALED to enable_nat_gateway=true AND
// enable_private_subnets=true, with no error.
func TestBuildModuleValues_VPC_NATHeal(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}

	cases := []struct {
		name  string
		comps *Components
	}{
		{"RDS", &Components{AWSVPC: "Public VPC", AWSRDS: boolPtr(true)}},
		{"EKS", &Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}},
		{"ECS", &Components{AWSVPC: "Public VPC", AWSECS: boolPtr(true)}},
		{"ElastiCache", &Components{AWSVPC: "Public VPC", AWSElastiCache: boolPtr(true)}},
		{"OpenSearch", &Components{AWSVPC: "Public VPC", AWSOpenSearch: boolPtr(true)}},
		{"EC2 node group", &Components{AWSVPC: "Public VPC", AWSEC2: "Intel"}},
		{"Private VPC + RDS", &Components{AWSVPC: "Private VPC", AWSRDS: boolPtr(true)}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := cfgWithAWSVPC(nil, boolPtr(false), nil) // frozen NAT=false
			vals, err := m.BuildModuleValues(KeyAWSVPC, tc.comps, cfg, "test", "us-east-1")
			require.NoError(t, err,
				"%s: frozen EnableNATGateway=false must be healed, not rejected", tc.name)
			assert.Equal(t, true, vals["enable_nat_gateway"],
				"%s: mapper must coerce enable_nat_gateway=true", tc.name)
			assert.Equal(t, true, vals["enable_private_subnets"],
				"%s: mapper must pin enable_private_subnets=true so the outputs.tf NAT invariant cannot trip", tc.name)
		})
	}
}

// TestBuildModuleValues_Lambda_TimeoutHeal pins the bare-integer normalization:
// a bare integer ("30"/"3") is coerced to the unit-suffixed seconds form;
// already-valid values pass through unchanged; genuinely-invalid values
// ("abc") still error.
func TestBuildModuleValues_Lambda_TimeoutHeal(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}

	t.Run("coerces and passes through", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			in     string
			wantT  int
			reason string
		}{
			{"30", 30, `bare "30" -> 30s`},
			{"3", 3, `bare "3" -> 3s`},
			{"30s", 30, `already-valid "30s" stays 30s`},
			{"15m", 900, `already-valid "15m" stays 900s`},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.in, func(t *testing.T) {
				t.Parallel()
				vals, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", tc.in), "", "")
				require.NoError(t, err, tc.reason)
				assert.Equal(t, tc.wantT, vals["timeout"], tc.reason)
			})
		}
	})

	t.Run("non-numeric timeout still errors", func(t *testing.T) {
		t.Parallel()
		_, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", "abc"), "", "")
		require.Error(t, err, `"abc" is not a bare integer and is not a valid duration; must still error`)
		var verr *ValidationError
		assert.ErrorAs(t, err, &verr, "must remain a ValidationError")
	})
}

// TestBuildModuleValues_Heal_EmitsWarning verifies both coercions log a
// "[composer/mapper] ... heal" warning via the package's log.Printf idiom.
//
// Serial (no t.Parallel): it swaps the global log output, which Go's test
// runner guarantees does not overlap any t.Parallel test (parallel tests are
// paused until all serial tests at this level finish). The mapper is
// synchronous with no background goroutines, so a plain buffer is race-safe.
func TestBuildModuleValues_Heal_EmitsWarning(t *testing.T) {
	m := DefaultMapper{}

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	// NAT heal warning.
	buf.Reset()
	_, err := m.BuildModuleValues(KeyAWSVPC, &Components{AWSVPC: "Public VPC", AWSRDS: boolPtr(true)},
		cfgWithAWSVPC(nil, boolPtr(false), nil), "test", "us-east-1")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "[composer/mapper] AWSVPC heal",
		"NAT coercion must emit a [composer/mapper] AWSVPC heal warning")

	// Lambda heal warning.
	buf.Reset()
	_, err = m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", "30"), "", "")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "[composer/mapper] AWSLambda heal",
		"bare-integer timeout coercion must emit a [composer/mapper] AWSLambda heal warning")

	// Negative control: a valid timeout must NOT emit a heal warning.
	buf.Reset()
	_, err = m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", "30s"), "", "")
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "heal",
		"an already-valid value must not emit any heal warning")
}

// --- Third frozen-value heal: out-of-enum "<N> vCPU" sizing (reliable#2097) ---
//
// A sizing value like "2 vCPU" was once compose-valid under a permissive legacy
// TS mapper but the strict Go composer hard-errors (the IR enum is {1,4,8}
// vCPU). That frozen value lives in stored config reliable composes verbatim,
// so the mapper now HEALS it: snap UP to the nearest valid tier (rounding up),
// snap above-max to the max, and leave valid tiers + concrete provider
// instance/node types untouched. Genuinely-malformed labels still error.
// Mirrors the #805/#806 heal style.

// TestSnapVCPUTier pins the round-up-to-nearest-tier rule directly, including
// the "only the <integer> vCPU shape matches" guard that keeps malformed values
// erroring at the call sites.
func TestSnapVCPUTier(t *testing.T) {
	t.Parallel()

	t.Run("snaps up to the nearest valid tier", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			in   string
			want string
		}{
			{"1 vCPU", "1 vCPU"},     // exact tier, unchanged
			{"2 vCPU", "4 vCPU"},     // round up across the 1->4 gap
			{"3 vCPU", "4 vCPU"},     // round up
			{"4 vCPU", "4 vCPU"},     // exact tier, unchanged
			{"5 vCPU", "8 vCPU"},     // round up across the 4->8 gap
			{"7 vCPU", "8 vCPU"},     // round up
			{"8 vCPU", "8 vCPU"},     // exact max tier, unchanged
			{"16 vCPU", "8 vCPU"},    // above max -> snap to max
			{"  2  vCPU ", "4 vCPU"}, // whitespace tolerated
			{"2 vcpu", "4 vCPU"},     // case-insensitive
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.in, func(t *testing.T) {
				t.Parallel()
				got, ok := snapVCPUTier(tc.in)
				require.True(t, ok, "%q must be recognized as a vCPU label", tc.in)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("does not match non-vCPU / malformed shapes", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"abc", "2.5 vCPU", "vCPU", "2 cpu", "cache.r6g.large", "db.m7i.large", ""} {
			_, ok := snapVCPUTier(in)
			assert.False(t, ok, "%q must NOT be treated as a healable vCPU label", in)
		}
	})
}

// TestBuildModuleValues_VCPUSizingHeal exercises the heal through the mapper for
// both doubly-affected fields (the reliable#2097 session has BOTH
// AWSElastiCache.NodeSize="2 vCPU" and AWSRDS.CPUSize="2 vCPU"): out-of-enum
// snaps up, valid tiers and concrete provider types pass through unchanged, and
// a genuinely-malformed value still errors.
func TestBuildModuleValues_VCPUSizingHeal(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}

	t.Run("ElastiCache NodeSize 2 vCPU heals to 4 vCPU node type", func(t *testing.T) {
		t.Parallel()
		vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("2 vCPU", "", ""), "", "")
		require.NoError(t, err, "frozen out-of-enum NodeSize must heal, not error")
		assert.Equal(t, "cache.r6g.xlarge", vals["node_type"], `"2 vCPU" snaps up to the 4 vCPU tier`)
	})

	t.Run("RDS CPUSize 2 vCPU heals to 4 vCPU instance class", func(t *testing.T) {
		t.Parallel()
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("2 vCPU", "", ""), "", "")
		require.NoError(t, err, "frozen out-of-enum CPUSize must heal, not error")
		assert.Equal(t, "db.m7i.xlarge", vals["instance_class"], `"2 vCPU" snaps up to the 4 vCPU tier`)
	})

	t.Run("above-max snaps to the max tier", func(t *testing.T) {
		t.Parallel()
		ecVals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("16 vCPU", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "cache.r6g.2xlarge", ecVals["node_type"], `"16 vCPU" snaps to the 8 vCPU max tier`)

		rdsVals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("16 vCPU", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "db.m7i.2xlarge", rdsVals["instance_class"], `"16 vCPU" snaps to the 8 vCPU max tier`)
	})

	t.Run("valid tiers are preserved (no heal)", func(t *testing.T) {
		t.Parallel()
		ec := map[string]string{"1 vCPU": "cache.t3.medium", "4 vCPU": "cache.r6g.xlarge", "8 vCPU": "cache.r6g.2xlarge"}
		for in, want := range ec {
			vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg(in, "", ""), "", "")
			require.NoError(t, err)
			assert.Equal(t, want, vals["node_type"], "%s must map to its canonical node type unchanged", in)
		}
		rds := map[string]string{"1 vCPU": "db.t3.medium", "4 vCPU": "db.m7i.xlarge", "8 vCPU": "db.m7i.2xlarge"}
		for in, want := range rds {
			vals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg(in, "", ""), "", "")
			require.NoError(t, err)
			assert.Equal(t, want, vals["instance_class"], "%s must map to its canonical instance class unchanged", in)
		}
	})

	t.Run("concrete provider types pass through untouched", func(t *testing.T) {
		t.Parallel()
		ecVals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("cache.m6g.large", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "cache.m6g.large", ecVals["node_type"], "a concrete cache.* type must not be healed")

		rdsVals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("db.r6g.4xlarge", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "db.r6g.4xlarge", rdsVals["instance_class"], "a concrete db.* type must not be healed")
	})

	t.Run("genuinely-malformed sizing still errors", func(t *testing.T) {
		t.Parallel()
		_, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("ginormous", "", ""), "", "")
		var ecVerr *ValidationError
		require.ErrorAs(t, err, &ecVerr, "a non-vCPU NodeSize must still fail fast as ValidationError")

		_, err = m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("2.5 vCPU", "", ""), "", "")
		var rdsVerr *ValidationError
		require.ErrorAs(t, err, &rdsVerr, `"2.5 vCPU" is not the integer-vCPU shape and must still error`)
	})
}

// TestBuildModuleValues_VCPUSizingHeal_EmitsWarning verifies the sizing heal
// logs a "[composer/mapper] ... heal" warning, and that a valid tier does not.
// Serial (no t.Parallel) for the same reason as TestBuildModuleValues_Heal_EmitsWarning.
func TestBuildModuleValues_VCPUSizingHeal_EmitsWarning(t *testing.T) {
	m := DefaultMapper{}

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	buf.Reset()
	_, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("2 vCPU", "", ""), "", "")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "[composer/mapper] AWSElastiCache heal",
		"out-of-enum NodeSize must emit a [composer/mapper] AWSElastiCache heal warning")

	buf.Reset()
	_, err = m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("2 vCPU", "", ""), "", "")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "[composer/mapper] AWSRDS heal",
		"out-of-enum CPUSize must emit a [composer/mapper] AWSRDS heal warning")

	// Negative control: a valid tier must NOT emit a heal warning.
	buf.Reset()
	_, err = m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("4 vCPU", "", ""), "", "")
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "heal",
		"an already-valid tier must not emit any heal warning")
}
