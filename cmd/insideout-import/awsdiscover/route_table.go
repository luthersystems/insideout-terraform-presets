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

// routeTableClient is the narrow subset of the EC2 SDK the route-table
// discoverer uses. The describe response carries the tag map inline so
// no separate DescribeTags round-trip is required (Bundle 4 / #323).
type routeTableClient interface {
	DescribeRouteTables(ctx context.Context, in *ec2.DescribeRouteTablesInput, opts ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
}

type routeTableDiscoverer struct {
	new func(region string) routeTableClient
}

func newRouteTableDiscoverer(cfg aws.Config) Discoverer {
	return &routeTableDiscoverer{new: func(region string) routeTableClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *routeTableDiscoverer) ResourceType() string { return "aws_route_table" }

// Discover lists route tables whose Project tag matches args.Project. EC2
// supports server-side `tag:Key` filters on DescribeRouteTables, so we
// never have to download every route table in the account just to filter
// client-side. When args.Project is empty (admin path) the request omits
// the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.RouteTable — no per-resource
// DescribeTags fetch is needed.
//
// Note: the "main" route table that AWS auto-creates with each VPC is
// untagged unless the operator explicitly tags it. Untagged main route
// tables are dropped by the server-side `tag:Project` filter — that is
// the correct behavior for project-scoped imports, since the main
// route table cannot be imported as a separate aws_route_table resource
// (it must use aws_main_route_table_association instead).
//
// Import ID for aws_route_table is the route-table ID (rtb-XXXXXXXX).
func (d *routeTableDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "route_table"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeRouteTablesInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		rts, err := paginateDescribeRouteTables(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeRouteTables (region=%s): %w", region, err)
		}

		// Sort by route-table ID so the emitted manifest is deterministic across runs.
		sort.Slice(rts, func(i, j int) bool {
			return aws.ToString(rts[i].RouteTableId) < aws.ToString(rts[j].RouteTableId)
		})

		for i := range rts {
			rt := &rts[i]
			id := aws.ToString(rt.RouteTableId)
			tags := ec2TagsToMap(rt.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(rt.Tags, id)
			native := map[string]string{
				"route_table_id": id,
				"vpc_id":         aws.ToString(rt.VpcId),
			}
			out = append(out, makeImportedResource(
				book,
				"aws_route_table",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_route_table", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a route table by route-table ID (rtb-XXXXXXXX) or
// ARN (arn:aws:ec2:<region>:<account>:route-table/rtb-XXXXXXXX). Issues a
// single DescribeRouteTables call with the route-table ID to verify
// existence.
func (d *routeTableDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	rtbID, err := routeTableIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{RouteTableIds: []string{rtbID}})
	if err != nil {
		// EC2 surfaces "InvalidRouteTableID.NotFound" as a generic API
		// error, not a typed exception. Match by code substring.
		if strings.Contains(err.Error(), "InvalidRouteTableID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_route_table %q: %w", rtbID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeRouteTables: %w", err)
	}
	if len(out.RouteTables) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_route_table %q: %w", rtbID, ErrNotFound)
	}
	rt := &out.RouteTables[0]
	name := vpcName(rt.Tags, rtbID)
	native := map[string]string{
		"route_table_id": rtbID,
		"vpc_id":         aws.ToString(rt.VpcId),
	}
	return makeImportedResource(
		addressBook{},
		"aws_route_table",
		name,
		rtbID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// routeTableIDFromID extracts the route-table ID (rtb-XXXXXXXX) from one
// of two accepted inputs: the bare route-table ID, or an EC2 ARN of the
// shape arn:aws:ec2:<region>:<account>:route-table/rtb-XXXXXXXX. Anything
// else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func routeTableIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("route_table: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("route_table: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("route_table: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "route-table" || !strings.HasPrefix(parts[1], "rtb-") {
			return "", fmt.Errorf("route_table: arn resource %q is not route-table/rtb-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "rtb-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("route_table: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeRouteTables walks all NextToken pages.
func paginateDescribeRouteTables(ctx context.Context, client routeTableClient, input *ec2.DescribeRouteTablesInput) ([]ec2types.RouteTable, error) {
	var all []ec2types.RouteTable
	for {
		out, err := client.DescribeRouteTables(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.RouteTables...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
