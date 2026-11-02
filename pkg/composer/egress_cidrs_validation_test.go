package composer

import (
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

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

// TestEgressCidrsValidationIsIdentical is a drift guard for the
// egress_cidrs validation, which is copied into four presets because
// Terraform has no shared validation (#907). A fix to one copy (for
// example the host-bit or IPv6 rule from the #913 review) that misses
// another would leave presets that accept different values. The
// validation blocks must be byte-identical; the descriptions differ on
// purpose (each names its own preset's caveat).
func TestEgressCidrsValidationIsIdentical(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	var want, wantFrom string
	for _, preset := range []string{"aws/alb", "aws/rds", "aws/elasticache", "aws/msk"} {
		files, err := c.GetPresetFiles(preset)
		require.NoError(t, err, "GetPresetFiles(%s)", preset)

		got := ""
		for name, content := range files {
			if !strings.HasSuffix(name, ".tf") {
				continue
			}
			f, diags := hclsyntax.ParseConfig(content, name, hcl.InitialPos)
			require.False(t, diags.HasErrors(), "parse %s%s: %s", preset, name, diags.Error())
			for _, b := range f.Body.(*hclsyntax.Body).Blocks {
				if b.Type != "variable" || len(b.Labels) != 1 || b.Labels[0] != "egress_cidrs" {
					continue
				}
				for _, inner := range b.Body.Blocks {
					if inner.Type == "validation" {
						got += string(inner.Range().SliceBytes(content))
					}
				}
			}
		}
		require.NotEmpty(t, got, "%s must declare variable egress_cidrs with a validation block", preset)
		if want == "" {
			want, wantFrom = got, preset
			continue
		}
		require.Equal(t, want, got,
			"the egress_cidrs validation in %s differs from %s; keep the four copies identical", preset, wantFrom)
	}
}
