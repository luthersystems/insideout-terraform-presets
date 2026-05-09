package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// vpcClient is the narrow subset of the EC2 SDK the VPC discoverer uses.
// The describe response carries the tag map inline so no separate
// DescribeTags round-trip is required (Bundle 4 / #319).
type vpcClient interface {
	DescribeVpcs(ctx context.Context, in *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

type vpcDiscoverer struct {
	new func(region string) vpcClient
}

func newVPCDiscoverer(cfg aws.Config) Discoverer {
	return &vpcDiscoverer{new: func(region string) vpcClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *vpcDiscoverer) ResourceType() string { return "aws_vpc" }

// Discover lists VPCs whose Project tag matches args.Project. EC2 supports
// server-side `tag:Key` filters on DescribeVpcs, so we never have to
// download every VPC in the account just to filter client-side. When
// args.Project is empty (admin path) the request omits the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.Vpc — no per-resource
// DescribeTags fetch is needed.
//
// Import ID for aws_vpc is the VPC ID (vpc-XXXXXXXX).
func (d *vpcDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "vpc"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeVpcsInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		vpcs, err := paginateDescribeVpcs(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeVpcs (region=%s): %w", region, err)
		}

		// Sort by VPC ID so the emitted manifest is deterministic across runs.
		sort.Slice(vpcs, func(i, j int) bool {
			return aws.ToString(vpcs[i].VpcId) < aws.ToString(vpcs[j].VpcId)
		})

		for i := range vpcs {
			v := &vpcs[i]
			id := aws.ToString(v.VpcId)
			tags := ec2TagsToMap(v.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(v.Tags, id)
			cidr := aws.ToString(v.CidrBlock)
			out = append(out, makeImportedResource(
				book,
				"aws_vpc",
				name,
				id,
				region,
				args.AccountID,
				map[string]string{"vpc_id": id, "cidr_block": cidr},
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_vpc", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a VPC by VPC ID (vpc-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:vpc/vpc-XXXXXXXX). Issues a single
// DescribeVpcs call with the VPC ID to verify existence.
func (d *vpcDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	vpcID, err := vpcIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		// EC2 surfaces "InvalidVpcID.NotFound" as a smithy.GenericAPIError,
		// not a typed exception. Inspect the API code via errors.As.
		if isEC2APIErrorCode(err, "InvalidVpcID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_vpc %q: %w", vpcID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeVpcs: %w", err)
	}
	if len(out.Vpcs) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_vpc %q: %w", vpcID, ErrNotFound)
	}
	v := &out.Vpcs[0]
	cidr := aws.ToString(v.CidrBlock)
	name := vpcName(v.Tags, vpcID)
	return makeImportedResource(
		addressBook{},
		"aws_vpc",
		name,
		vpcID,
		region,
		accountID,
		map[string]string{"vpc_id": vpcID, "cidr_block": cidr},
		nil,
	), nil
}

// vpcIDFromID extracts the VPC ID (vpc-XXXXXXXX) from one of two accepted
// inputs: the bare VPC ID, or an EC2 ARN of the shape
// arn:aws:ec2:<region>:<account>:vpc/vpc-XXXXXXXX. Anything else returns
// ErrNotSupported so dep-chase routes it to its unresolvable bucket.
func vpcIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("vpc: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("vpc: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("vpc: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource is "vpc/vpc-XXXXXXXX".
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "vpc" || !strings.HasPrefix(parts[1], "vpc-") {
			return "", fmt.Errorf("vpc: arn resource %q is not vpc/vpc-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "vpc-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("vpc: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeVpcs walks all NextToken pages. The SDK returns at most
// 1000 VPCs per page, but it's cheaper to be paranoid than to silently
// drop the tail of an account that happens to have crossed the threshold.
func paginateDescribeVpcs(ctx context.Context, client vpcClient, input *ec2.DescribeVpcsInput) ([]ec2types.Vpc, error) {
	var all []ec2types.Vpc
	for {
		out, err := client.DescribeVpcs(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.Vpcs...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}

// ec2TagsToMap converts the EC2 SDK's []Tag slice into a string-keyed map.
// Returns a non-nil empty map (not nil) so the filter+persist contract
// holds: nil = "didn't fetch", empty = "fetched, no tags".
func ec2TagsToMap(in []ec2types.Tag) map[string]string {
	out := make(map[string]string, len(in))
	for _, t := range in {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}

// vpcName picks the most useful human-readable name for a VPC: the `Name`
// tag if set, otherwise the bare VPC ID. Mirrors how the AWS console
// labels VPCs in its picker.
func vpcName(tags []ec2types.Tag, fallback string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == "Name" {
			if v := aws.ToString(t.Value); v != "" {
				return v
			}
		}
	}
	return fallback
}

// isEC2APIErrorCode reports whether err is a smithy.APIError whose
// ErrorCode matches one of the supplied codes. EC2 surfaces "Invalid…
// .NotFound"-shaped errors as smithy.GenericAPIError values rather than
// typed exceptions, so callers must inspect the API code rather than
// substring-match on err.Error() (which is locale-/SDK-version-fragile
// and rejects wrap chains that hide the smithy error behind layers).
func isEC2APIErrorCode(err error, codes ...string) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	got := apiErr.ErrorCode()
	for _, want := range codes {
		if got == want {
			return true
		}
	}
	return false
}
