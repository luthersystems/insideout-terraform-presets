package composer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapper_BastionAdminCIDRs pins the three states of #908: nil keeps the
// preset's open SSH rule (admin_cidrs not emitted), an empty list emits
// admin_cidrs = [] (SSM only, no SSH rule), and a list emits it trimmed.
func TestMapper_BastionAdminCIDRs(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyAWSBastion, nil, nil, "", "")
	require.NoError(t, err)
	assert.NotContains(t, vals, "admin_cidrs", "no config must keep the preset default")

	vals, err = m.BuildModuleValues(KeyAWSBastion, nil, &Config{AWSBastion: &AWSBastionConfig{}}, "", "")
	require.NoError(t, err)
	assert.NotContains(t, vals, "admin_cidrs", "nil AdminCIDRs must keep the preset default")

	empty := []string{}
	vals, err = m.BuildModuleValues(KeyAWSBastion, nil, &Config{AWSBastion: &AWSBastionConfig{AdminCIDRs: &empty}}, "", "")
	require.NoError(t, err)
	assert.Equal(t, []any{}, vals["admin_cidrs"], "an empty list is the SSM-only opt-in and must be emitted")

	tight := []string{" 203.0.113.0/24 "}
	vals, err = m.BuildModuleValues(KeyAWSBastion, nil, &Config{AWSBastion: &AWSBastionConfig{AdminCIDRs: &tight}}, "", "")
	require.NoError(t, err)
	assert.Equal(t, []any{"203.0.113.0/24"}, vals["admin_cidrs"])
}

// TestAWSBastionConfig_AdminCIDRsJSON pins that the empty list survives a
// JSON round trip, which is why AdminCIDRs is a pointer: with a plain
// slice, omitempty would drop [] and turn SSM-only back into open SSH.
func TestAWSBastionConfig_AdminCIDRsJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"aws_bastion":{"adminCidrs":[]}}`), &cfg))
	require.NotNil(t, cfg.AWSBastion)
	require.NotNil(t, cfg.AWSBastion.AdminCIDRs)
	assert.Empty(t, *cfg.AWSBastion.AdminCIDRs)

	out, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.JSONEq(t, `{"aws_bastion":{"adminCidrs":[]}}`, string(out))

	var unset Config
	require.NoError(t, json.Unmarshal([]byte(`{"aws_bastion":{}}`), &unset))
	assert.Nil(t, unset.AWSBastion.AdminCIDRs)
}

// TestValidate_BastionAdminCIDRs pins that Validate uses the preset rule:
// an empty list and IPv4 network addresses pass; IPv6 and host bits fail.
func TestValidate_BastionAdminCIDRs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		val []string
		ok  bool
	}{
		{[]string{}, true},
		{[]string{"0.0.0.0/0"}, true},
		{[]string{"203.0.113.0/24", "10.1.0.0/16"}, true},
		{[]string{"203.0.113.7/24"}, false},
		{[]string{"2001:db8::/32"}, false},
		{[]string{"not-a-cidr"}, false},
	} {
		v := tc.val
		cfg := &Config{AWSBastion: &AWSBastionConfig{AdminCIDRs: &v}}
		_, bad := issuesByField(Validate(nil, cfg))["aws_bastion.adminCidrs"]
		assert.Equal(t, !tc.ok, bad, "AdminCIDRs=%v", tc.val)
	}
}

// TestComposeStack_BastionSSMOnly pins the composed output: the SSM-only
// opt-in reaches the bastion tfvars as an empty list.
func TestComposeStack_BastionSSMOnly(t *testing.T) {
	t.Parallel()

	enabled := true
	empty := []string{}
	out, err := newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSBastion},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC", AWSBastion: &enabled},
		Cfg:          &Config{AWSBastion: &AWSBastionConfig{AdminCIDRs: &empty}},
		Project:      "demo",
		Region:       "us-west-2",
	})
	require.NoError(t, err)
	tfvars, ok := out["/aws_bastion.auto.tfvars"]
	require.True(t, ok, "expected aws_bastion.auto.tfvars")
	requireTfvarAssignment(t, string(tfvars), "aws_bastion_admin_cidrs", `[]`,
		"the SSM-only opt-in must emit an empty admin_cidrs")

	out, err = newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSBastion},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC", AWSBastion: &enabled},
		Project:      "demo",
		Region:       "us-west-2",
	})
	require.NoError(t, err)
	requireNoTfvarAssignment(t, string(out["/aws_bastion.auto.tfvars"]), "aws_bastion_admin_cidrs",
		"a bastion that did not opt in must keep the preset default")
}
