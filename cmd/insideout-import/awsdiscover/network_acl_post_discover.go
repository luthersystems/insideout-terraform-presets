package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Network ACL PostDiscover follow-up — default-NACL re-typing.
//
// A VPC's DEFAULT network ACL is discovered via Cloud Control as an
// AWS::EC2::NetworkAcl, the same as a custom NACL, and the discoverer
// emits it with Identity.Type == "aws_network_acl". But the AWS provider
// REFUSES to import a default NACL as aws_network_acl: it errors with
//
//	Error: use the `aws_default_network_acl` resource instead
//
// so `terraform plan -generate-config-out` produces NO body and the
// resource is silently dropped as no_generated_config — the last
// silent-drop in the whole-account reverse-import. (Confirmed live
// against cust1: every NACL in the account is its VPC's default, and
// declaring acl-… as aws_network_acl errored exactly as above while
// declaring it as aws_default_network_acl produced a clean body.)
//
// The fix is lossless: re-type the DEFAULT NACL instance to
// aws_default_network_acl. That sibling resource imports by the same
// acl-… id and `generate-config-out` renders a full body for it. Custom
// NACLs are left as aws_network_acl (still importable). The two resource
// families share the import ID, so this is a pure per-instance TFType
// reclassification — no separate discoverer, registry entry, or enricher
// is needed (aws_default_network_acl is never independently discoverable;
// it is only ever this reclassification of a default aws_network_acl).
//
// Why a PostDiscover SDK call: the Cloud Control AWS::EC2::NetworkAcl
// schema exposes only {VpcId, Id, Tags} — it does NOT surface IsDefault —
// so the discoverer's property extractor cannot tell default from custom.
// networkACLPostDiscover issues one ec2:DescribeNetworkAcls per discovered
// NACL, stamps NativeIDs["is_default"], and for a default NACL rewrites
// Identity.Type to aws_default_network_acl (regenerating Identity.Address
// so the type prefix matches the emitted import {} block and resource
// body). Soft-fails (returns an error the discoverer logs) without
// clobbering the IR: a DescribeNetworkAcls miss leaves the NACL as
// aws_network_acl, the same posture as the genconfig orphan-prune backstop.

// defaultNetworkACLTFType is the Terraform resource type a default network
// ACL must be imported as. The AWS provider rejects a default NACL declared
// as aws_network_acl.
const defaultNetworkACLTFType = "aws_default_network_acl"

// networkACLDescriber is the narrow subset of the EC2 API the PostDiscover
// hook issues. Real *ec2.Client and in-test fakes satisfy it; the
// production hook constructs the real client per region.
type networkACLDescriber interface {
	DescribeNetworkAcls(ctx context.Context, in *ec2.DescribeNetworkAclsInput, opts ...func(*ec2.Options)) (*ec2.DescribeNetworkAclsOutput, error)
}

// newNetworkACLDescriber is the production factory; tests swap it (or call
// networkACLPostDiscoverWithClient directly) to inject a fake.
var newNetworkACLDescriber = func(awsCfg aws.Config, region string) networkACLDescriber {
	return ec2.NewFromConfig(awsCfg, func(o *ec2.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// networkACLPostDiscover is the cloudControlConfig.PostDiscover hook for
// aws_network_acl. It resolves IsDefault via ec2:DescribeNetworkAcls and,
// for a default NACL, re-types the IR to aws_default_network_acl.
func networkACLPostDiscover(ctx context.Context, awsCfg aws.Config, region string, ir *imported.ImportedResource) error {
	return networkACLPostDiscoverWithClient(ctx, newNetworkACLDescriber(awsCfg, region), ir)
}

func networkACLPostDiscoverWithClient(ctx context.Context, client networkACLDescriber, ir *imported.ImportedResource) error {
	if ir == nil {
		return nil
	}
	aclID := networkACLIDForDescribe(&ir.Identity)
	if aclID == "" {
		return fmt.Errorf("network_acl: cannot derive acl id from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	out, err := client.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{
		NetworkAclIds: []string{aclID},
	})
	if err != nil {
		return fmt.Errorf("network_acl %q: DescribeNetworkAcls: %w", aclID, err)
	}
	if out == nil || len(out.NetworkAcls) == 0 {
		return fmt.Errorf("network_acl %q: DescribeNetworkAcls returned no ACL", aclID)
	}
	acl := out.NetworkAcls[0]
	isDefault := acl.IsDefault != nil && *acl.IsDefault
	if ir.Identity.NativeIDs == nil {
		ir.Identity.NativeIDs = map[string]string{}
	}
	if isDefault {
		ir.Identity.NativeIDs["is_default"] = "true"
		retypeAsDefaultNetworkACL(ir)
	} else {
		ir.Identity.NativeIDs["is_default"] = "false"
	}
	return nil
}

// retypeAsDefaultNetworkACL rewrites a default NACL's Identity.Type to
// aws_default_network_acl and regenerates Identity.Address so the type
// prefix on the emitted import {} block and resource body matches. The
// label portion is unchanged (it derives from the same NameHint), so the
// address is stable across re-types: aws_network_acl.<acl> becomes
// aws_default_network_acl.<acl>. Collision avoidance uses a nil exists
// predicate — a VPC has exactly one default NACL and the label carries the
// unique acl-… id, so the freshly-typed address cannot collide.
func retypeAsDefaultNetworkACL(ir *imported.ImportedResource) {
	ir.Identity.Type = defaultNetworkACLTFType
	ir.Identity.Address = imported.GenerateAddress(ir.Identity, nil)
}

// networkACLIDForDescribe resolves a DescribeNetworkAcls-acceptable acl-…
// id from the identity the aws_network_acl discoverer populates. ImportID
// is the Cloud Control primary identifier (the bare acl-… id, passthrough);
// NameHint and NativeIDs["name"] carry the same value as fallbacks.
func networkACLIDForDescribe(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	if id.NativeIDs["name"] != "" {
		return id.NativeIDs["name"]
	}
	return id.NameHint
}
