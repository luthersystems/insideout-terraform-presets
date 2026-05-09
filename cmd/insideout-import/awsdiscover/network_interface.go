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

// networkInterfaceClient is the narrow subset of the EC2 SDK the
// network-interface (ENI) discoverer uses. The describe response
// carries the tag map inline (`TagSet`) so no separate DescribeTags
// round-trip is required (Bundle 4 / #323).
type networkInterfaceClient interface {
	DescribeNetworkInterfaces(ctx context.Context, in *ec2.DescribeNetworkInterfacesInput, opts ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error)
}

type networkInterfaceDiscoverer struct {
	new func(region string) networkInterfaceClient
}

func newNetworkInterfaceDiscoverer(cfg aws.Config) Discoverer {
	return &networkInterfaceDiscoverer{new: func(region string) networkInterfaceClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *networkInterfaceDiscoverer) ResourceType() string { return "aws_network_interface" }

// Discover lists ENIs whose Project tag matches args.Project. EC2
// supports server-side `tag:Key` filters on DescribeNetworkInterfaces,
// so we never have to download every ENI in the account just to filter
// client-side. When args.Project is empty (admin path) the request omits
// the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.NetworkInterface (TagSet
// field) — no per-resource DescribeTags fetch is needed.
//
// NOTE: ENIs are mostly side-effects of higher-level resources (NAT
// gateways, Lambdas in VPC, ELBs, RDS instances). The InsideOut composer
// rarely creates aws_network_interface resources directly — they are
// owned and managed by the parent resource. The discoverer is included
// for completeness so the picker UI can surface project-tagged ENIs
// (e.g. for inspection). Operators may want to filter ENIs out of
// generated.tf post-discovery; a follow-up may add a category or
// skip-list flag.
//
// Import ID for aws_network_interface is the ENI ID (eni-XXXXXXXX).
func (d *networkInterfaceDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "network_interface"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeNetworkInterfacesInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		enis, err := paginateDescribeNetworkInterfaces(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeNetworkInterfaces (region=%s): %w", region, err)
		}

		// Sort by ENI ID so the emitted manifest is deterministic across runs.
		sort.Slice(enis, func(i, j int) bool {
			return aws.ToString(enis[i].NetworkInterfaceId) < aws.ToString(enis[j].NetworkInterfaceId)
		})

		for i := range enis {
			eni := &enis[i]
			id := aws.ToString(eni.NetworkInterfaceId)
			// ENI tags live on TagSet (not Tags) — distinct field name
			// vs every other EC2 describe response shape, but the same
			// underlying []ec2types.Tag type, so ec2TagsToMap and
			// vpcName apply directly.
			tags := ec2TagsToMap(eni.TagSet)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(eni.TagSet, id)
			native := map[string]string{
				"network_interface_id": id,
				"vpc_id":               aws.ToString(eni.VpcId),
				"subnet_id":            aws.ToString(eni.SubnetId),
				"interface_type":       string(eni.InterfaceType),
			}
			out = append(out, makeImportedResource(
				book,
				"aws_network_interface",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_network_interface", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves an ENI by ENI ID (eni-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:network-interface/eni-XXXXXXXX). Issues
// a single DescribeNetworkInterfaces call with the ENI ID to verify
// existence.
func (d *networkInterfaceDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	eniID, err := networkInterfaceIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{NetworkInterfaceIds: []string{eniID}})
	if err != nil {
		// EC2 surfaces "InvalidNetworkInterfaceID.NotFound" as a
		// smithy.APIError; inspect via errors.As.
		if isEC2APIErrorCode(err, "InvalidNetworkInterfaceID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_network_interface %q: %w", eniID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeNetworkInterfaces: %w", err)
	}
	if len(out.NetworkInterfaces) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_network_interface %q: %w", eniID, ErrNotFound)
	}
	eni := &out.NetworkInterfaces[0]
	name := vpcName(eni.TagSet, eniID)
	native := map[string]string{
		"network_interface_id": eniID,
		"vpc_id":               aws.ToString(eni.VpcId),
		"subnet_id":            aws.ToString(eni.SubnetId),
		"interface_type":       string(eni.InterfaceType),
	}
	return makeImportedResource(
		addressBook{},
		"aws_network_interface",
		name,
		eniID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// networkInterfaceIDFromID extracts the ENI ID (eni-XXXXXXXX) from one of
// two accepted inputs: the bare ENI ID, or an EC2 ARN of the shape
// arn:aws:ec2:<region>:<account>:network-interface/eni-XXXXXXXX. Anything
// else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func networkInterfaceIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("network_interface: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("network_interface: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("network_interface: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "network-interface" || !strings.HasPrefix(parts[1], "eni-") {
			return "", fmt.Errorf("network_interface: arn resource %q is not network-interface/eni-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "eni-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("network_interface: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeNetworkInterfaces walks all NextToken pages.
func paginateDescribeNetworkInterfaces(ctx context.Context, client networkInterfaceClient, input *ec2.DescribeNetworkInterfacesInput) ([]ec2types.NetworkInterface, error) {
	var all []ec2types.NetworkInterface
	for {
		out, err := client.DescribeNetworkInterfaces(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.NetworkInterfaces...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
