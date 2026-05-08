package awsdiscover

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// networkACLClient is the narrow subset of the EC2 SDK the network-ACL
// discoverer uses. The describe response carries the tag map inline so
// no separate DescribeTags round-trip is required (Bundle 4 / #323).
type networkACLClient interface {
	DescribeNetworkAcls(ctx context.Context, in *ec2.DescribeNetworkAclsInput, opts ...func(*ec2.Options)) (*ec2.DescribeNetworkAclsOutput, error)
}

type networkACLDiscoverer struct {
	new func(region string) networkACLClient
}

func newNetworkACLDiscoverer(cfg aws.Config) Discoverer {
	return &networkACLDiscoverer{new: func(region string) networkACLClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *networkACLDiscoverer) ResourceType() string { return "aws_network_acl" }

// Discover lists network ACLs whose Project tag matches args.Project. EC2
// supports server-side `tag:Key` filters on DescribeNetworkAcls, so we
// never have to download every NACL in the account just to filter
// client-side. When args.Project is empty (admin path) the request omits
// the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.NetworkAcl — no per-resource
// DescribeTags fetch is needed.
//
// Note: each VPC carries one auto-created default NACL (IsDefault=true).
// Untagged defaults are dropped by the server-side `tag:Project` filter,
// which is correct: a default NACL cannot be imported standalone — the
// operator imports rules onto it via aws_network_acl_rule. Project-tagged
// defaults still come through so the picker surfaces them; the operator
// can then choose whether to import them.
//
// Import ID for aws_network_acl is the network-ACL ID (acl-XXXXXXXX).
func (d *networkACLDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "network_acl"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeNetworkAclsInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		nacls, err := paginateDescribeNetworkAcls(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeNetworkAcls (region=%s): %w", region, err)
		}

		// Sort by NACL ID so the emitted manifest is deterministic across runs.
		sort.Slice(nacls, func(i, j int) bool {
			return aws.ToString(nacls[i].NetworkAclId) < aws.ToString(nacls[j].NetworkAclId)
		})

		for i := range nacls {
			nacl := &nacls[i]
			id := aws.ToString(nacl.NetworkAclId)
			tags := ec2TagsToMap(nacl.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(nacl.Tags, id)
			native := map[string]string{
				"network_acl_id": id,
				"vpc_id":         aws.ToString(nacl.VpcId),
				// is_default is coerced to a string because NativeIDs is
				// map[string]string. The picker reads "true"/"false".
				"is_default": fmt.Sprintf("%v", aws.ToBool(nacl.IsDefault)),
			}
			out = append(out, makeImportedResource(
				book,
				"aws_network_acl",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_network_acl", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a network ACL by network-ACL ID (acl-XXXXXXXX) or
// ARN (arn:aws:ec2:<region>:<account>:network-acl/acl-XXXXXXXX). Issues a
// single DescribeNetworkAcls call with the NACL ID to verify existence.
func (d *networkACLDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	naclID, err := networkACLIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeNetworkAcls(ctx, &ec2.DescribeNetworkAclsInput{NetworkAclIds: []string{naclID}})
	if err != nil {
		// EC2 surfaces "InvalidNetworkAclID.NotFound" as a generic API
		// error, not a typed exception. Match by code substring.
		if strings.Contains(err.Error(), "InvalidNetworkAclID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_network_acl %q: %w", naclID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeNetworkAcls: %w", err)
	}
	if len(out.NetworkAcls) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_network_acl %q: %w", naclID, ErrNotFound)
	}
	nacl := &out.NetworkAcls[0]
	name := vpcName(nacl.Tags, naclID)
	native := map[string]string{
		"network_acl_id": naclID,
		"vpc_id":         aws.ToString(nacl.VpcId),
		"is_default":     fmt.Sprintf("%v", aws.ToBool(nacl.IsDefault)),
	}
	return makeImportedResource(
		addressBook{},
		"aws_network_acl",
		name,
		naclID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// networkACLIDFromID extracts the NACL ID (acl-XXXXXXXX) from one of two
// accepted inputs: the bare NACL ID, or an EC2 ARN of the shape
// arn:aws:ec2:<region>:<account>:network-acl/acl-XXXXXXXX. Anything else
// returns ErrNotSupported so dep-chase routes it to its unresolvable
// bucket.
func networkACLIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("network_acl: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("network_acl: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("network_acl: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "network-acl" || !strings.HasPrefix(parts[1], "acl-") {
			return "", fmt.Errorf("network_acl: arn resource %q is not network-acl/acl-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "acl-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("network_acl: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeNetworkAcls walks all NextToken pages.
func paginateDescribeNetworkAcls(ctx context.Context, client networkACLClient, input *ec2.DescribeNetworkAclsInput) ([]ec2types.NetworkAcl, error) {
	var all []ec2types.NetworkAcl
	for {
		out, err := client.DescribeNetworkAcls(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.NetworkAcls...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
