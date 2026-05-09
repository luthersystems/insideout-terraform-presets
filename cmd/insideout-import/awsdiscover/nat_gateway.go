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

// natGatewayClient is the narrow subset of the EC2 SDK the NAT-gateway
// discoverer uses. The describe response carries the tag map inline so
// no separate DescribeTags round-trip is required (Bundle 4 / #321).
type natGatewayClient interface {
	DescribeNatGateways(ctx context.Context, in *ec2.DescribeNatGatewaysInput, opts ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error)
}

type natGatewayDiscoverer struct {
	new func(region string) natGatewayClient
}

func newNatGatewayDiscoverer(cfg aws.Config) Discoverer {
	return &natGatewayDiscoverer{new: func(region string) natGatewayClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *natGatewayDiscoverer) ResourceType() string { return "aws_nat_gateway" }

// Discover lists NAT gateways whose Project tag matches args.Project.
// EC2 supports server-side `tag:Key` filters on DescribeNatGateways
// (note: the SDK input field is `Filter` singular, not `Filters`), so
// we never have to download every NAT in the account just to filter
// client-side. When args.Project is empty (admin path) the request
// omits the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.NatGateway — no per-resource
// DescribeTags fetch is needed.
//
// Skip-list: NAT gateways in State="deleted" or State="deleting" are
// tombstones — AWS keeps them visible in DescribeNatGateways for ~1
// hour after deletion but they cannot be imported (terraform import
// rejects them with "NatGatewayNotFound"), so emitting them would
// produce a manifest entry the operator cannot resolve. The State
// filter is applied client-side after the SDK call because
// DescribeNatGateways does support a server-side `state` filter, but
// applying it server-side would suppress the tombstones for every
// caller — including Stage 2c3 dep-chase, which wants to surface a
// "this NAT was deleted" warning for the operator. Filtering
// client-side keeps the SDK contract narrow.
//
// Import ID for aws_nat_gateway is the NAT-gateway ID (nat-XXXXXXXX).
func (d *natGatewayDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "nat_gateway"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeNatGatewaysInput{}
		if args.Project != "" {
			input.Filter = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		nats, err := paginateDescribeNatGateways(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeNatGateways (region=%s): %w", region, err)
		}

		// Sort by NAT-gateway ID so the emitted manifest is deterministic across runs.
		sort.Slice(nats, func(i, j int) bool {
			return aws.ToString(nats[i].NatGatewayId) < aws.ToString(nats[j].NatGatewayId)
		})

		for i := range nats {
			n := &nats[i]
			// Skip tombstones — see header comment.
			if n.State == ec2types.NatGatewayStateDeleted || n.State == ec2types.NatGatewayStateDeleting {
				continue
			}
			id := aws.ToString(n.NatGatewayId)
			tags := ec2TagsToMap(n.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(n.Tags, id)
			native := map[string]string{
				"nat_gateway_id": id,
				"vpc_id":         aws.ToString(n.VpcId),
				"subnet_id":      aws.ToString(n.SubnetId),
			}
			if len(n.NatGatewayAddresses) > 0 {
				if v := aws.ToString(n.NatGatewayAddresses[0].PublicIp); v != "" {
					native["public_ip"] = v
				}
			}
			out = append(out, makeImportedResource(
				book,
				"aws_nat_gateway",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_nat_gateway", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a NAT gateway by NAT-gateway ID (nat-XXXXXXXX)
// or ARN (arn:aws:ec2:<region>:<account>:natgateway/nat-XXXXXXXX).
// Issues a single DescribeNatGateways call with the NAT-gateway ID to
// verify existence.
func (d *natGatewayDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	natID, err := natGatewayIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{NatGatewayIds: []string{natID}})
	if err != nil {
		// EC2 surfaces "NatGatewayNotFound" as a smithy.APIError;
		// inspect via errors.As.
		if isEC2APIErrorCode(err, "NatGatewayNotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_nat_gateway %q: %w", natID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeNatGateways: %w", err)
	}
	if len(out.NatGateways) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_nat_gateway %q: %w", natID, ErrNotFound)
	}
	n := &out.NatGateways[0]
	name := vpcName(n.Tags, natID)
	native := map[string]string{
		"nat_gateway_id": natID,
		"vpc_id":         aws.ToString(n.VpcId),
		"subnet_id":      aws.ToString(n.SubnetId),
	}
	if len(n.NatGatewayAddresses) > 0 {
		if v := aws.ToString(n.NatGatewayAddresses[0].PublicIp); v != "" {
			native["public_ip"] = v
		}
	}
	return makeImportedResource(
		addressBook{},
		"aws_nat_gateway",
		name,
		natID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// natGatewayIDFromID extracts the NAT-gateway ID (nat-XXXXXXXX) from one
// of two accepted inputs: the bare NAT-gateway ID, or an EC2 ARN of the
// shape arn:aws:ec2:<region>:<account>:natgateway/nat-XXXXXXXX.
// Anything else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func natGatewayIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("nat_gateway: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("nat_gateway: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("nat_gateway: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "natgateway" || !strings.HasPrefix(parts[1], "nat-") {
			return "", fmt.Errorf("nat_gateway: arn resource %q is not natgateway/nat-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "nat-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("nat_gateway: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeNatGateways walks all NextToken pages.
func paginateDescribeNatGateways(ctx context.Context, client natGatewayClient, input *ec2.DescribeNatGatewaysInput) ([]ec2types.NatGateway, error) {
	var all []ec2types.NatGateway
	for {
		out, err := client.DescribeNatGateways(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.NatGateways...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
