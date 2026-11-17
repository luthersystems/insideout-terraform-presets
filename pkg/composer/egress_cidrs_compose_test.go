package composer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// egressCIDRsComponents is every component with an EgressCIDRs field
// (#907), with the setter that puts a value on its Config sub-block.
var egressCIDRsComponents = []struct {
	key   ComponentKey
	field string
	set   func(*Config, []string)
}{
	{KeyAWSALB, "aws_alb.egressCidrs", func(c *Config, v []string) { c.AWSALB = &AWSALBConfig{EgressCIDRs: v} }},
	{KeyAWSRDS, "aws_rds.egressCidrs", func(c *Config, v []string) { c.AWSRDS = &AWSRDSConfig{EgressCIDRs: v} }},
	{KeyAWSElastiCache, "aws_elasticache.egressCidrs", func(c *Config, v []string) { c.AWSElastiCache = &AWSElastiCacheConfig{EgressCIDRs: v} }},
	{KeyAWSMSK, "aws_msk.egressCidrs", func(c *Config, v []string) { c.AWSMSK = &AWSMSKConfig{EgressCIDRs: v} }},
}

// TestMapper_EgressCIDRs pins the opt-in contract of #907: with no value
// the mapper leaves egress_cidrs out, so the preset default (open egress)
// applies and running stacks see no change; with a value it is emitted,
// trimmed.
func TestMapper_EgressCIDRs(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}

	for _, tc := range egressCIDRsComponents {
		t.Run(string(tc.key), func(t *testing.T) {
			t.Parallel()

			vals, err := m.BuildModuleValues(tc.key, nil, nil, "", "")
			require.NoError(t, err)
			assert.NotContains(t, vals, "egress_cidrs", "no config must keep the preset default")

			cfg := &Config{}
			tc.set(cfg, nil)
			vals, err = m.BuildModuleValues(tc.key, nil, cfg, "", "")
			require.NoError(t, err)
			assert.NotContains(t, vals, "egress_cidrs", "a sub-block without EgressCIDRs must keep the preset default")

			tc.set(cfg, []string{" 10.1.0.0/16 ", "192.168.4.0/24"})
			vals, err = m.BuildModuleValues(tc.key, nil, cfg, "", "")
			require.NoError(t, err)
			assert.Equal(t, []any{"10.1.0.0/16", "192.168.4.0/24"}, vals["egress_cidrs"])
		})
	}
}

// TestValidate_EgressCIDRs pins that Validate judges EgressCIDRs with the
// preset's own rule, including an empty list, which the mapper emits and
// the preset rejects.
func TestValidate_EgressCIDRs(t *testing.T) {
	t.Parallel()

	for _, tc := range egressCIDRsComponents {
		t.Run(string(tc.key), func(t *testing.T) {
			t.Parallel()

			cfg := &Config{}
			tc.set(cfg, []string{"10.1.0.0/16"})
			assert.NotContains(t, issuesByField(Validate(nil, cfg)), tc.field)

			for _, bad := range [][]string{{"10.1.0.1/16"}, {"2001:db8::/32"}, {}} {
				tc.set(cfg, bad)
				issue, ok := issuesByField(Validate(nil, cfg))[tc.field]
				require.True(t, ok, "EgressCIDRs=%v must be an issue", bad)
				assert.NotEmpty(t, issue.Reason)
			}
		})
	}
}

// TestComposeStack_EgressCIDRs pins the end-to-end path: an opted-in
// component gets <key>_egress_cidrs in its tfvars, and the others in the
// same stack do not.
func TestComposeStack_EgressCIDRs(t *testing.T) {
	t.Parallel()

	enabled := true
	cfg := &Config{
		AWSRDS: &AWSRDSConfig{EgressCIDRs: []string{"10.1.0.0/16"}},
		AWSMSK: &AWSMSKConfig{EgressCIDRs: []string{"10.1.0.0/16"}},
	}
	out, err := newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB, KeyAWSRDS, KeyAWSElastiCache, KeyAWSMSK},
		Comps: &Components{
			Cloud:          "AWS",
			AWSVPC:         "Private VPC",
			AWSALB:         &enabled,
			AWSRDS:         &enabled,
			AWSElastiCache: &enabled,
			AWSMSK:         &enabled,
		},
		Cfg:     cfg,
		Project: "demo",
		Region:  "us-west-2",
	})
	require.NoError(t, err)

	for _, key := range []string{"aws_rds", "aws_msk"} {
		tfvars, ok := out["/"+key+".auto.tfvars"]
		require.True(t, ok, "expected %s.auto.tfvars", key)
		requireTfvarAssignment(t, string(tfvars), key+"_egress_cidrs", `["10.1.0.0/16"]`,
			"an opted-in component must carry its egress_cidrs")
	}
	for _, key := range []string{"aws_alb", "aws_elasticache"} {
		tfvars, ok := out["/"+key+".auto.tfvars"]
		require.True(t, ok, "expected %s.auto.tfvars", key)
		requireNoTfvarAssignment(t, string(tfvars), key+"_egress_cidrs",
			"a component that did not opt in must keep the preset default")
	}
}

// TestComputePresetDefaults_NoEgressCIDRsBackfill pins the
// composer:"nobackfill" tag: the open default must not be copied into a
// caller's config, where it would be stored as a user choice and pin open
// egress on the stack.
func TestComputePresetDefaults_NoEgressCIDRsBackfill(t *testing.T) {
	t.Parallel()

	overlay, err := newTestClient().ComputePresetDefaults(Config{}, &Components{Cloud: "aws"},
		[]ComponentKey{KeyAWSALB, KeyAWSRDS, KeyAWSElastiCache, KeyAWSMSK})
	require.NoError(t, err)
	if overlay.AWSALB != nil {
		assert.Nil(t, overlay.AWSALB.EgressCIDRs)
	}
	if overlay.AWSRDS != nil {
		assert.Nil(t, overlay.AWSRDS.EgressCIDRs)
	}
	if overlay.AWSElastiCache != nil {
		assert.Nil(t, overlay.AWSElastiCache.EgressCIDRs)
	}
	if overlay.AWSMSK != nil {
		assert.Nil(t, overlay.AWSMSK.EgressCIDRs)
	}

	// The checks above pass trivially if no overlay is built, so check
	// the tag itself: the same default fills an untagged field and skips
	// the tagged one.
	defaults := map[string]any{"egress_cidrs": []any{"0.0.0.0/0"}}
	var tagged AWSRDSConfig
	filled := overlayStructFromDefaults(reflect.ValueOf((*AWSRDSConfig)(nil)), reflect.ValueOf(&tagged).Elem(), defaults)
	assert.False(t, filled)
	assert.Nil(t, tagged.EgressCIDRs)

	var untagged struct {
		EgressCIDRs []string `json:"egressCidrs,omitempty"`
	}
	type untaggedT = struct {
		EgressCIDRs []string `json:"egressCidrs,omitempty"`
	}
	filled = overlayStructFromDefaults(reflect.ValueOf((*untaggedT)(nil)), reflect.ValueOf(&untagged).Elem(), defaults)
	assert.True(t, filled)
	assert.Equal(t, []string{"0.0.0.0/0"}, untagged.EgressCIDRs)
}
