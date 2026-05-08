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

// vpcEndpointClient is the narrow subset of the EC2 SDK the VPC-endpoint
// discoverer uses. The describe response carries the tag map inline so
// no separate DescribeTags round-trip is required (Bundle 4 / #323).
type vpcEndpointClient interface {
	DescribeVpcEndpoints(ctx context.Context, in *ec2.DescribeVpcEndpointsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error)
}

type vpcEndpointDiscoverer struct {
	new func(region string) vpcEndpointClient
}

func newVPCEndpointDiscoverer(cfg aws.Config) Discoverer {
	return &vpcEndpointDiscoverer{new: func(region string) vpcEndpointClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *vpcEndpointDiscoverer) ResourceType() string { return "aws_vpc_endpoint" }

// Discover lists VPC endpoints whose Project tag matches args.Project.
// EC2 supports server-side `tag:Key` filters on DescribeVpcEndpoints, so
// we never have to download every endpoint in the account just to filter
// client-side. When args.Project is empty (admin path) the request omits
// the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.VpcEndpoint — no per-resource
// DescribeTags fetch is needed.
//
// Skip-list: VPC endpoints in State="deleted" or State="deleting" are
// tombstones — AWS keeps them visible in DescribeVpcEndpoints briefly
// after deletion but they cannot be imported (terraform import rejects
// them with "InvalidVpcEndpoint.NotFound"), so emitting them would
// produce a manifest entry the operator cannot resolve. The skip mirrors
// the NAT-gateway skip-state pattern from PR #322.
//
// Import ID for aws_vpc_endpoint is the VPC-endpoint ID (vpce-XXXXXXXX).
func (d *vpcEndpointDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "vpc_endpoint"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeVpcEndpointsInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		vpces, err := paginateDescribeVpcEndpoints(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeVpcEndpoints (region=%s): %w", region, err)
		}

		// Sort by VPC-endpoint ID so the emitted manifest is deterministic across runs.
		sort.Slice(vpces, func(i, j int) bool {
			return aws.ToString(vpces[i].VpcEndpointId) < aws.ToString(vpces[j].VpcEndpointId)
		})

		for i := range vpces {
			vpce := &vpces[i]
			// Skip tombstones — see header comment.
			if state := string(vpce.State); state == "deleted" || state == "deleting" {
				continue
			}
			id := aws.ToString(vpce.VpcEndpointId)
			tags := ec2TagsToMap(vpce.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(vpce.Tags, id)
			native := map[string]string{
				"vpc_endpoint_id":   id,
				"vpc_id":            aws.ToString(vpce.VpcId),
				"service_name":      aws.ToString(vpce.ServiceName),
				"vpc_endpoint_type": string(vpce.VpcEndpointType),
			}
			out = append(out, makeImportedResource(
				book,
				"aws_vpc_endpoint",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_vpc_endpoint", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a VPC endpoint by VPC-endpoint ID (vpce-XXXXXXXX)
// or ARN (arn:aws:ec2:<region>:<account>:vpc-endpoint/vpce-XXXXXXXX).
// Issues a single DescribeVpcEndpoints call with the VPC-endpoint ID to
// verify existence.
func (d *vpcEndpointDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	vpceID, err := vpcEndpointIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{VpcEndpointIds: []string{vpceID}})
	if err != nil {
		// EC2 surfaces "InvalidVpcEndpointId.NotFound" / "InvalidVpcEndpoint.NotFound"
		// as generic API errors. Match by code substring.
		if strings.Contains(err.Error(), "InvalidVpcEndpointId.NotFound") || strings.Contains(err.Error(), "InvalidVpcEndpoint.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_vpc_endpoint %q: %w", vpceID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeVpcEndpoints: %w", err)
	}
	if len(out.VpcEndpoints) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_vpc_endpoint %q: %w", vpceID, ErrNotFound)
	}
	vpce := &out.VpcEndpoints[0]
	name := vpcName(vpce.Tags, vpceID)
	native := map[string]string{
		"vpc_endpoint_id":   vpceID,
		"vpc_id":            aws.ToString(vpce.VpcId),
		"service_name":      aws.ToString(vpce.ServiceName),
		"vpc_endpoint_type": string(vpce.VpcEndpointType),
	}
	return makeImportedResource(
		addressBook{},
		"aws_vpc_endpoint",
		name,
		vpceID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// vpcEndpointIDFromID extracts the VPC-endpoint ID (vpce-XXXXXXXX) from
// one of two accepted inputs: the bare VPC-endpoint ID, or an EC2 ARN of
// the shape arn:aws:ec2:<region>:<account>:vpc-endpoint/vpce-XXXXXXXX.
// Anything else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func vpcEndpointIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("vpc_endpoint: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("vpc_endpoint: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("vpc_endpoint: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "vpc-endpoint" || !strings.HasPrefix(parts[1], "vpce-") {
			return "", fmt.Errorf("vpc_endpoint: arn resource %q is not vpc-endpoint/vpce-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "vpce-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("vpc_endpoint: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeVpcEndpoints walks all NextToken pages.
func paginateDescribeVpcEndpoints(ctx context.Context, client vpcEndpointClient, input *ec2.DescribeVpcEndpointsInput) ([]ec2types.VpcEndpoint, error) {
	var all []ec2types.VpcEndpoint
	for {
		out, err := client.DescribeVpcEndpoints(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.VpcEndpoints...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
