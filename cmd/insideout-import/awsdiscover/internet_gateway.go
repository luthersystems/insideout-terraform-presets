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

// internetGatewayClient is the narrow subset of the EC2 SDK the IGW
// discoverer uses. The describe response carries the tag map inline so
// no separate DescribeTags round-trip is required (Bundle 4 / #321).
type internetGatewayClient interface {
	DescribeInternetGateways(ctx context.Context, in *ec2.DescribeInternetGatewaysInput, opts ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error)
}

type internetGatewayDiscoverer struct {
	new func(region string) internetGatewayClient
}

func newInternetGatewayDiscoverer(cfg aws.Config) Discoverer {
	return &internetGatewayDiscoverer{new: func(region string) internetGatewayClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *internetGatewayDiscoverer) ResourceType() string { return "aws_internet_gateway" }

// Discover lists internet gateways whose Project tag matches args.Project.
// EC2 supports server-side `tag:Key` filters on
// DescribeInternetGateways, so we never have to download every IGW in
// the account just to filter client-side. When args.Project is empty
// (admin path) the request omits the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.InternetGateway — no
// per-resource DescribeTags fetch is needed.
//
// Import ID for aws_internet_gateway is the IGW ID (igw-XXXXXXXX).
func (d *internetGatewayDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "internet_gateway"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeInternetGatewaysInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		igws, err := paginateDescribeInternetGateways(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeInternetGateways (region=%s): %w", region, err)
		}

		// Sort by IGW ID so the emitted manifest is deterministic across runs.
		sort.Slice(igws, func(i, j int) bool {
			return aws.ToString(igws[i].InternetGatewayId) < aws.ToString(igws[j].InternetGatewayId)
		})

		for i := range igws {
			g := &igws[i]
			id := aws.ToString(g.InternetGatewayId)
			tags := ec2TagsToMap(g.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(g.Tags, id) // reuse Name-tag-or-fallback helper
			native := map[string]string{"internet_gateway_id": id}
			if len(g.Attachments) > 0 {
				if v := aws.ToString(g.Attachments[0].VpcId); v != "" {
					native["vpc_id"] = v
				}
			}
			out = append(out, makeImportedResource(
				book,
				"aws_internet_gateway",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_internet_gateway", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves an IGW by IGW ID (igw-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:internet-gateway/igw-XXXXXXXX). Issues
// a single DescribeInternetGateways call with the IGW ID to verify
// existence.
func (d *internetGatewayDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	igwID, err := internetGatewayIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{InternetGatewayIds: []string{igwID}})
	if err != nil {
		// EC2 surfaces "InvalidInternetGatewayID.NotFound" as a generic API
		// error, not a typed exception. Match by code substring.
		if strings.Contains(err.Error(), "InvalidInternetGatewayID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_internet_gateway %q: %w", igwID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeInternetGateways: %w", err)
	}
	if len(out.InternetGateways) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_internet_gateway %q: %w", igwID, ErrNotFound)
	}
	g := &out.InternetGateways[0]
	name := vpcName(g.Tags, igwID)
	native := map[string]string{"internet_gateway_id": igwID}
	if len(g.Attachments) > 0 {
		if v := aws.ToString(g.Attachments[0].VpcId); v != "" {
			native["vpc_id"] = v
		}
	}
	return makeImportedResource(
		addressBook{},
		"aws_internet_gateway",
		name,
		igwID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// internetGatewayIDFromID extracts the IGW ID (igw-XXXXXXXX) from one of
// two accepted inputs: the bare IGW ID, or an EC2 ARN of the shape
// arn:aws:ec2:<region>:<account>:internet-gateway/igw-XXXXXXXX. Anything
// else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func internetGatewayIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("internet_gateway: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("internet_gateway: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("internet_gateway: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "internet-gateway" || !strings.HasPrefix(parts[1], "igw-") {
			return "", fmt.Errorf("internet_gateway: arn resource %q is not internet-gateway/igw-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "igw-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("internet_gateway: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeInternetGateways walks all NextToken pages.
func paginateDescribeInternetGateways(ctx context.Context, client internetGatewayClient, input *ec2.DescribeInternetGatewaysInput) ([]ec2types.InternetGateway, error) {
	var all []ec2types.InternetGateway
	for {
		out, err := client.DescribeInternetGateways(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.InternetGateways...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
