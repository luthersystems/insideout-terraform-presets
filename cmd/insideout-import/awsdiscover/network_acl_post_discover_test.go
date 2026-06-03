package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeNetworkACLDescriber is a minimal networkACLDescriber stub for the
// PostDiscover hook tests.
type fakeNetworkACLDescriber struct {
	out      *ec2.DescribeNetworkAclsOutput
	err      error
	gotACLID string
}

func (f *fakeNetworkACLDescriber) DescribeNetworkAcls(_ context.Context, in *ec2.DescribeNetworkAclsInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkAclsOutput, error) {
	if len(in.NetworkAclIds) > 0 {
		f.gotACLID = in.NetworkAclIds[0]
	}
	return f.out, f.err
}

// newNACLIR builds the aws_network_acl IR shape the discoverer produces —
// ImportID / NameHint / NativeIDs["name"] all carry the acl-… id (the CC
// primary identifier, passthrough) and Address is the generated
// aws_network_acl.<acl> form.
func newNACLIR(aclID string) *imported.ImportedResource {
	id := imported.ResourceIdentity{
		Type:      "aws_network_acl",
		Region:    "us-east-1",
		ImportID:  aclID,
		NameHint:  aclID,
		NativeIDs: map[string]string{"name": aclID},
	}
	id.Address = imported.GenerateAddress(id, nil)
	return &imported.ImportedResource{Identity: id}
}

// TestNetworkACLPostDiscover_DefaultRetyped is the core regression: a VPC's
// DEFAULT network ACL (acl-07053f2bb73b58ba6, the live cust1 us-east-1
// default — IsDefault=true) is resolved at DISCOVER time via
// DescribeNetworkAcls and re-typed to aws_default_network_acl. Verified
// live: the same acl-… id bodies cleanly via `terraform plan
// -generate-config-out` as aws_default_network_acl but errors ("use the
// `aws_default_network_acl` resource instead") as aws_network_acl, so this
// re-type is the difference between a clean import and a silent
// no_generated_config drop.
func TestNetworkACLPostDiscover_DefaultRetyped(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLDescriber{
		out: &ec2.DescribeNetworkAclsOutput{NetworkAcls: []ec2types.NetworkAcl{{
			NetworkAclId: aws.String("acl-07053f2bb73b58ba6"),
			VpcId:        aws.String("vpc-044b75ab41c527a20"),
			IsDefault:    aws.Bool(true),
		}}},
	}
	ir := newNACLIR("acl-07053f2bb73b58ba6")
	require.NoError(t, networkACLPostDiscoverWithClient(context.Background(), fake, ir))

	assert.Equal(t, "acl-07053f2bb73b58ba6", fake.gotACLID, "the acl-… id must drive DescribeNetworkAcls")
	assert.Equal(t, "true", ir.Identity.NativeIDs["is_default"], "is_default must be stamped")
	assert.Equal(t, "aws_default_network_acl", ir.Identity.Type, "a default NACL must be re-typed to aws_default_network_acl")
	assert.Equal(t, "aws_default_network_acl.acl_07053f2bb73b58ba6", ir.Identity.Address,
		"Address must be regenerated so the type prefix matches the emitted import {} block")
	// Import ID is unchanged: aws_default_network_acl imports by the same acl-… id.
	assert.Equal(t, "acl-07053f2bb73b58ba6", ir.Identity.ImportID)
}

// TestNetworkACLPostDiscover_CustomStaysImportable is the custom-NACL guard:
// a non-default (custom) NACL keeps Identity.Type == aws_network_acl and
// its original address, so it still imports as aws_network_acl
// (generate-config-out bodies it cleanly). Only is_default=false is
// stamped.
func TestNetworkACLPostDiscover_CustomStaysImportable(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLDescriber{
		out: &ec2.DescribeNetworkAclsOutput{NetworkAcls: []ec2types.NetworkAcl{{
			NetworkAclId: aws.String("acl-0custom00000000000"),
			VpcId:        aws.String("vpc-0fdd604b65acd3f87"),
			IsDefault:    aws.Bool(false),
		}}},
	}
	ir := newNACLIR("acl-0custom00000000000")
	wantAddr := ir.Identity.Address
	require.NoError(t, networkACLPostDiscoverWithClient(context.Background(), fake, ir))

	assert.Equal(t, "false", ir.Identity.NativeIDs["is_default"])
	assert.Equal(t, "aws_network_acl", ir.Identity.Type, "a custom NACL must stay aws_network_acl")
	assert.Equal(t, wantAddr, ir.Identity.Address, "a custom NACL's address must not be rewritten")
	assert.Equal(t, "aws_network_acl.acl_0custom00000000000", ir.Identity.Address)
}

// TestNetworkACLPostDiscover_NilIsDefaultTreatedAsCustom proves a NACL whose
// IsDefault field is nil (absent) is treated as custom — it stays
// aws_network_acl. The re-type is gated on a definitive IsDefault==true, so
// a missing discriminator never mis-routes a custom NACL into the default
// resource (which would then fail its own import).
func TestNetworkACLPostDiscover_NilIsDefaultTreatedAsCustom(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLDescriber{
		out: &ec2.DescribeNetworkAclsOutput{NetworkAcls: []ec2types.NetworkAcl{{
			NetworkAclId: aws.String("acl-0nil0000000000000"),
			IsDefault:    nil,
		}}},
	}
	ir := newNACLIR("acl-0nil0000000000000")
	require.NoError(t, networkACLPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "false", ir.Identity.NativeIDs["is_default"])
	assert.Equal(t, "aws_network_acl", ir.Identity.Type)
}

// TestNetworkACLPostDiscover_SoftFailsOnError proves a DescribeNetworkAcls
// failure is surfaced as an error (the discoverer logs it via ServiceWarn)
// without clobbering the IR — the NACL is still emitted as aws_network_acl,
// matching the genconfig orphan-prune backstop posture. A default NACL that
// soft-fails here drops as before, but a soft-fail never WORSENS the result
// for a custom NACL.
func TestNetworkACLPostDiscover_SoftFailsOnError(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLDescriber{err: errors.New("AccessDenied")}
	ir := newNACLIR("acl-0err0000000000000")
	wantAddr := ir.Identity.Address
	err := networkACLPostDiscoverWithClient(context.Background(), fake, ir)
	require.Error(t, err)
	_, stamped := ir.Identity.NativeIDs["is_default"]
	assert.False(t, stamped, "no is_default stamped when DescribeNetworkAcls fails")
	assert.Equal(t, "aws_network_acl", ir.Identity.Type, "the IR is not clobbered on soft-fail")
	assert.Equal(t, wantAddr, ir.Identity.Address)
}

// TestNetworkACLPostDiscover_EmptyResultSoftFails proves DescribeNetworkAcls
// returning zero ACLs (e.g. the ACL was deleted between discover and the
// follow-up) is a soft-fail, not a panic or a bogus re-type.
func TestNetworkACLPostDiscover_EmptyResultSoftFails(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLDescriber{out: &ec2.DescribeNetworkAclsOutput{}}
	ir := newNACLIR("acl-0gone000000000000")
	require.Error(t, networkACLPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "aws_network_acl", ir.Identity.Type)
}

// TestNetworkACLPostDiscover_EmptyIdentity proves an IR with no derivable
// acl-… id returns an error rather than calling DescribeNetworkAcls with an
// empty id.
func TestNetworkACLPostDiscover_EmptyIdentity(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLDescriber{}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "aws_network_acl"}}
	require.Error(t, networkACLPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Empty(t, fake.gotACLID, "DescribeNetworkAcls must not be called with an empty id")
}

// TestNetworkACLConfig_WiresPostDiscover guards the registration: the
// aws_network_acl cloudControlConfig must carry the PostDiscover hook, or
// default NACLs silently regress to dropping as no_generated_config.
func TestNetworkACLConfig_WiresPostDiscover(t *testing.T) {
	t.Parallel()
	var found bool
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.TFType == "aws_network_acl" {
			found = true
			require.NotNil(t, cfg.PostDiscover, "aws_network_acl must wire PostDiscover for discover-time IsDefault resolution")
		}
	}
	require.True(t, found, "aws_network_acl config not found")
}
