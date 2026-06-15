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
