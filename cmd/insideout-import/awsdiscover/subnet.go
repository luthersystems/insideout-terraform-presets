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

// subnetClient is the narrow subset of the EC2 SDK the subnet discoverer
// uses. The describe response carries the tag map inline so no separate
// DescribeTags round-trip is required (Bundle 4 / #319).
type subnetClient interface {
	DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

type subnetDiscoverer struct {
	new func(region string) subnetClient
}

func newSubnetDiscoverer(cfg aws.Config) Discoverer {
	return &subnetDiscoverer{new: func(region string) subnetClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *subnetDiscoverer) ResourceType() string { return "aws_subnet" }

// Discover lists subnets whose Project tag matches args.Project. EC2
// supports server-side `tag:Key` filters on DescribeSubnets, so we never
// have to download every subnet in the account just to filter
// client-side. When args.Project is empty (admin path) the request omits
// the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.Subnet — no per-resource
// DescribeTags fetch is needed.
//
// Import ID for aws_subnet is the subnet ID (subnet-XXXXXXXX).
func (d *subnetDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "subnet"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeSubnetsInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		subnets, err := paginateDescribeSubnets(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeSubnets (region=%s): %w", region, err)
		}

		// Sort by subnet ID so the emitted manifest is deterministic across runs.
		sort.Slice(subnets, func(i, j int) bool {
			return aws.ToString(subnets[i].SubnetId) < aws.ToString(subnets[j].SubnetId)
		})

		for i := range subnets {
			s := &subnets[i]
			id := aws.ToString(s.SubnetId)
			tags := ec2TagsToMap(s.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(s.Tags, id) // reuse the Name-tag-or-fallback helper
			out = append(out, makeImportedResource(
				book,
				"aws_subnet",
				name,
				id,
				region,
				args.AccountID,
				map[string]string{
					"subnet_id":         id,
					"vpc_id":            aws.ToString(s.VpcId),
					"availability_zone": aws.ToString(s.AvailabilityZone),
					"cidr_block":        aws.ToString(s.CidrBlock),
				},
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_subnet", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a subnet by subnet ID (subnet-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:subnet/subnet-XXXXXXXX). Issues a
// single DescribeSubnets call with the subnet ID to verify existence.
func (d *subnetDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	subnetID, err := subnetIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}})
	if err != nil {
		// EC2 surfaces "InvalidSubnetID.NotFound" as a generic API error,
		// not a typed exception. Match by code substring (mirror s3.go).
		if strings.Contains(err.Error(), "InvalidSubnetID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_subnet %q: %w", subnetID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeSubnets: %w", err)
	}
	if len(out.Subnets) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_subnet %q: %w", subnetID, ErrNotFound)
	}
	s := &out.Subnets[0]
	name := vpcName(s.Tags, subnetID)
	return makeImportedResource(
		addressBook{},
		"aws_subnet",
		name,
		subnetID,
		region,
		accountID,
		map[string]string{
			"subnet_id":         subnetID,
			"vpc_id":            aws.ToString(s.VpcId),
			"availability_zone": aws.ToString(s.AvailabilityZone),
			"cidr_block":        aws.ToString(s.CidrBlock),
		},
		nil,
	), nil
}

// subnetIDFromID extracts the subnet ID (subnet-XXXXXXXX) from one of two
// accepted inputs: the bare subnet ID, or an EC2 ARN of the shape
// arn:aws:ec2:<region>:<account>:subnet/subnet-XXXXXXXX. Anything else
// returns ErrNotSupported so dep-chase routes it to its unresolvable bucket.
func subnetIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("subnet: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("subnet: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("subnet: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "subnet" || !strings.HasPrefix(parts[1], "subnet-") {
			return "", fmt.Errorf("subnet: arn resource %q is not subnet/subnet-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "subnet-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("subnet: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeSubnets walks all NextToken pages.
func paginateDescribeSubnets(ctx context.Context, client subnetClient, input *ec2.DescribeSubnetsInput) ([]ec2types.Subnet, error) {
	var all []ec2types.Subnet
	for {
		out, err := client.DescribeSubnets(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.Subnets...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
