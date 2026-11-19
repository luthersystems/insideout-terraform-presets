package reverseimport

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/normalize"
)

// legacyDefaultNetworkACLAttributes is the legacy Attributes map of a
// stored default NACL, as it decodes from the stored JSON. Discovery
// re-types a VPC's default NACL from aws_network_acl to
// aws_default_network_acl (awsdiscover/network_acl_post_discover.go), so
// the ACL is named by default_network_acl_id, not vpc_id. Before #947 the
// type had no typed model, and reliable's attrs-backfill skipped every
// such row with "no typed model for aws_default_network_acl".
const legacyDefaultNetworkACLAttributes = `{
  "arn": "arn:aws:ec2:us-east-1:123456789012:network-acl/acl-0abc1234def567890",
  "default_network_acl_id": "acl-0abc1234def567890",
  "id": "acl-0abc1234def567890",
  "owner_id": "123456789012",
  "vpc_id": "vpc-0123456789abcdef0",
  "ingress": [
    {"action": "allow", "cidr_block": "0.0.0.0/0", "from_port": 0, "icmp_code": 0, "icmp_type": 0, "ipv6_cidr_block": "", "protocol": "-1", "rule_no": 100, "to_port": 0}
  ],
  "egress": [
    {"action": "allow", "cidr_block": "0.0.0.0/0", "from_port": 0, "icmp_code": 0, "icmp_type": 0, "ipv6_cidr_block": "", "protocol": "-1", "rule_no": 100, "to_port": 0}
  ],
  "tags": {"Name": "default", "Project": "core"}
}`

// TestDefaultNetworkACL_LegacyAttributesRoundTripTypedModel pins #947:
// a stored default-NACL Attributes map converts to typed Attrs through the
// shared wire shape (WrapPlanAttrValue, the helper reliable's backfill
// calls), decodes into the generated model, and emits valid HCL through
// the composer's typed path, with ingress and egress as blocks.
func TestDefaultNetworkACL_LegacyAttributesRoundTripTypedModel(t *testing.T) {
	t.Parallel()

	var legacy map[string]any
	require.NoError(t, json.Unmarshal([]byte(legacyDefaultNetworkACLAttributes), &legacy))

	attrs, err := json.Marshal(WrapPlanAttrValue(legacy))
	require.NoError(t, err)

	typedAny, err := generated.UnmarshalAttrs("aws_default_network_acl", attrs)
	require.NoError(t, err, "legacy Attributes must decode into the typed model")
	typed, ok := typedAny.(*generated.AWSDefaultNetworkACL)
	require.Truef(t, ok, "UnmarshalAttrs returned %T, want *generated.AWSDefaultNetworkACL", typedAny)

	require.NotNil(t, typed.DefaultNetworkACLID)
	assert.Equal(t, "acl-0abc1234def567890", *typed.DefaultNetworkACLID.Literal)
	require.Len(t, typed.Ingress, 1)
	require.Len(t, typed.Egress, 1)
	assert.Equal(t, "allow", *typed.Ingress[0].Action.Literal)
	assert.Equal(t, "-1", *typed.Ingress[0].Protocol.Literal)
	assert.InDelta(t, 100, *typed.Ingress[0].RuleNo.Literal, 0)
	assert.Equal(t, "0.0.0.0/0", *typed.Egress[0].CIDRBlock.Literal)
	require.Contains(t, typed.Tags, "Project")
	assert.Equal(t, "core", *typed.Tags["Project"].Literal)

	// The typed Attrs survive a re-marshal unchanged in meaning.
	again, err := json.Marshal(typed)
	require.NoError(t, err)
	_, err = generated.UnmarshalAttrs("aws_default_network_acl", again)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_default_network_acl",
			Address:  "aws_default_network_acl.acl_0abc1234def567890",
			ImportID: "acl-0abc1234def567890",
			Region:   "us-east-1",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: again,
	}
	out, _, issues := composer.EmitImportedTFWithIssues("aws", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.Emptyf(t, issues, "emit issues: %+v", issues)
	require.NotNil(t, out)
	s := string(out)

	file, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.Falsef(t, diags.HasErrors(), "imported.tf must parse: %s\n%s", diags.Error(), s)

	var res *hclsyntax.Block
	for _, blk := range file.Body.(*hclsyntax.Body).Blocks {
		if blk.Type == "resource" && len(blk.Labels) == 2 && blk.Labels[0] == "aws_default_network_acl" {
			res = blk
		}
	}
	require.NotNilf(t, res, "no aws_default_network_acl resource block:\n%s", s)

	// ingress / egress are nested blocks of aws_default_network_acl, not
	// attributes: emitted as `ingress { ... }`, never `ingress = [...]`.
	var ingress, egress int
	for _, blk := range res.Body.Blocks {
		switch blk.Type {
		case "ingress":
			ingress++
			assert.Contains(t, blk.Body.Attributes, "rule_no")
			assert.Contains(t, blk.Body.Attributes, "action")
		case "egress":
			egress++
		}
	}
	assert.Equalf(t, 1, ingress, "want one ingress block:\n%s", s)
	assert.Equalf(t, 1, egress, "want one egress block:\n%s", s)
	assert.NotContains(t, res.Body.Attributes, "ingress", "ingress must not be an attribute:\n%s", s)
	assert.NotContains(t, res.Body.Attributes, "egress", "egress must not be an attribute:\n%s", s)

	// default_network_acl_id and tags are configurable and must emit; the
	// computed id / arn / owner_id / vpc_id must not.
	assert.Contains(t, res.Body.Attributes, "default_network_acl_id")
	assert.Contains(t, res.Body.Attributes, "tags")
	for _, computed := range []string{"id", "arn", "owner_id", "vpc_id"} {
		assert.NotContainsf(t, res.Body.Attributes, computed, "computed %q must not emit:\n%s", computed, s)
	}

	// Every compose runs NormalizeImportedHCL after emit. It must still pin
	// the rule blocks and subnet_ids next to the newly emitted blocks, so
	// an apply never changes the live default NACL's rules or subnets.
	normalized, err := normalize.NormalizeImportedHCL(out)
	require.NoError(t, err)
	assert.Regexpf(t, `ignore_changes\s*=\s*\[egress,\s+ingress,\s+subnet_ids\]`, string(normalized),
		"normalized imported.tf must pin the rules and subnets:\n%s", normalized)
}
