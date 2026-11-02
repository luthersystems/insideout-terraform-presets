package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
	cty "github.com/zclconf/go-cty/cty"
)

// TestEgressCidrsValidationMatchesTerraform pins that the composer's offline
// validator judges egress_cidrs (#907) the way Terraform does. The rule
// compares cidrhost(c, 0) with the address part of the CIDR, so a cidrhost
// stub that returns a placeholder would reject every valid CIDR except
// 0.0.0.0/0, and a missing function would error. Both would fail a composed
// stack at compose time that Terraform accepts (found by
// TestPresetDefaultsSatisfyValidations on PR #913).
func TestEgressCidrsValidationMatchesTerraform(t *testing.T) {
	t.Parallel()

	reg, err := defaultValidationRegistry()
	require.NoError(t, err)

	cases := []struct {
		name string
		val  []any
		ok   bool
	}{
		{"default", []any{"0.0.0.0/0"}, true},
		{"vpc cidr", []any{"10.0.0.0/16"}, true},
		{"two cidrs", []any{"10.0.0.0/16", "192.168.4.0/24"}, true},
		{"host bits set", []any{"10.0.0.1/16"}, false},
		{"ipv6", []any{"2001:db8::/32"}, false},
		{"not a cidr", []any{"not-a-cidr"}, false},
		{"empty", []any{}, false},
	}
	for _, key := range []ComponentKey{KeyAWSALB, KeyAWSRDS, KeyAWSElastiCache, KeyAWSMSK} {
		for _, tc := range cases {
			fail, ok := reg.validate(key, "egress_cidrs", tc.val)
			require.Equal(t, tc.ok, ok, "%s egress_cidrs=%v (%s): %s", key, tc.val, tc.name, fail.reason)
		}
	}
}

// TestCidrhostFuncMatchesTerraform pins cidrhost against examples from the
// Terraform documentation, so a rule that compares its result is judged
// the same way at compose time and at plan time.
func TestCidrhostFuncMatchesTerraform(t *testing.T) {
	t.Parallel()

	fn := cidrhostFunc()
	cases := []struct {
		prefix string
		host   int64
		want   string
	}{
		{"10.12.112.0/20", 16, "10.12.112.16"},
		{"10.12.112.0/20", 268, "10.12.113.12"},
		{"10.0.0.1/16", 0, "10.0.0.0"},
		{"10.0.0.0/24", -1, "10.0.0.255"},
		{"fd00:fd12:3456:7890:00a2::/72", 34, "fd00:fd12:3456:7890::22"},
	}
	for _, tc := range cases {
		got, err := fn.Call([]cty.Value{cty.StringVal(tc.prefix), cty.NumberIntVal(tc.host)})
		require.NoError(t, err, "%s %d", tc.prefix, tc.host)
		require.Equal(t, tc.want, got.AsString(), "cidrhost(%q, %d)", tc.prefix, tc.host)
	}
	_, err := fn.Call([]cty.Value{cty.StringVal("10.0.0.0/30"), cty.NumberIntVal(4)})
	require.Error(t, err, "a host number past the end of the prefix must fail, as in Terraform")
}
