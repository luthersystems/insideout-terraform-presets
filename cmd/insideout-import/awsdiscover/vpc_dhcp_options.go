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

// vpcDHCPOptionsClient is the narrow subset of the EC2 SDK the
// VPC-DHCP-options discoverer uses. The describe response carries the
// tag map inline so no separate DescribeTags round-trip is required
// (Bundle 4 / #323).
type vpcDHCPOptionsClient interface {
	DescribeDhcpOptions(ctx context.Context, in *ec2.DescribeDhcpOptionsInput, opts ...func(*ec2.Options)) (*ec2.DescribeDhcpOptionsOutput, error)
}

type vpcDHCPOptionsDiscoverer struct {
	new func(region string) vpcDHCPOptionsClient
}

func newVPCDHCPOptionsDiscoverer(cfg aws.Config) Discoverer {
	return &vpcDHCPOptionsDiscoverer{new: func(region string) vpcDHCPOptionsClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *vpcDHCPOptionsDiscoverer) ResourceType() string { return "aws_vpc_dhcp_options" }

// Discover lists DHCP options sets whose Project tag matches args.Project.
// EC2 supports server-side `tag:Key` filters on DescribeDhcpOptions, so
// we never have to download every options set in the account just to
// filter client-side. When args.Project is empty (admin path) the
// request omits the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.DhcpOptions — no per-resource
// DescribeTags fetch is needed.
//
// Note: every region carries an auto-created default DHCP options set
// (untagged). The default is dropped by the server-side `tag:Project`
// filter — that is the correct behavior for project-scoped imports,
// since the default DHCP options set is a singleton AWS asset that
// cannot be imported as a project resource.
//
// Import ID for aws_vpc_dhcp_options is the DHCP-options ID
// (dopt-XXXXXXXX).
func (d *vpcDHCPOptionsDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "vpc_dhcp_options"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeDhcpOptionsInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		dopts, err := paginateDescribeDhcpOptions(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeDhcpOptions (region=%s): %w", region, err)
		}

		// Sort by DHCP-options ID so the emitted manifest is deterministic across runs.
		sort.Slice(dopts, func(i, j int) bool {
			return aws.ToString(dopts[i].DhcpOptionsId) < aws.ToString(dopts[j].DhcpOptionsId)
		})

		for i := range dopts {
			dopt := &dopts[i]
			id := aws.ToString(dopt.DhcpOptionsId)
			tags := ec2TagsToMap(dopt.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(dopt.Tags, id)
			native := map[string]string{
				"dhcp_options_id": id,
			}
			out = append(out, makeImportedResource(
				book,
				"aws_vpc_dhcp_options",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_vpc_dhcp_options", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a DHCP options set by DHCP-options ID
// (dopt-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:dhcp-options/dopt-XXXXXXXX). Issues a
// single DescribeDhcpOptions call with the DHCP-options ID to verify
// existence.
func (d *vpcDHCPOptionsDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	doptID, err := vpcDHCPOptionsIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{DhcpOptionsIds: []string{doptID}})
	if err != nil {
		// EC2 surfaces "InvalidDhcpOptionID.NotFound" as a generic API
		// error, not a typed exception. Match by code substring.
		if strings.Contains(err.Error(), "InvalidDhcpOptionID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_vpc_dhcp_options %q: %w", doptID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeDhcpOptions: %w", err)
	}
	if len(out.DhcpOptions) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_vpc_dhcp_options %q: %w", doptID, ErrNotFound)
	}
	dopt := &out.DhcpOptions[0]
	name := vpcName(dopt.Tags, doptID)
	native := map[string]string{
		"dhcp_options_id": doptID,
	}
	return makeImportedResource(
		addressBook{},
		"aws_vpc_dhcp_options",
		name,
		doptID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// vpcDHCPOptionsIDFromID extracts the DHCP-options ID (dopt-XXXXXXXX)
// from one of two accepted inputs: the bare DHCP-options ID, or an EC2
// ARN of the shape arn:aws:ec2:<region>:<account>:dhcp-options/dopt-XXXXXXXX.
// Anything else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func vpcDHCPOptionsIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("vpc_dhcp_options: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("vpc_dhcp_options: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("vpc_dhcp_options: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "dhcp-options" || !strings.HasPrefix(parts[1], "dopt-") {
			return "", fmt.Errorf("vpc_dhcp_options: arn resource %q is not dhcp-options/dopt-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "dopt-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("vpc_dhcp_options: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeDhcpOptions walks all NextToken pages.
func paginateDescribeDhcpOptions(ctx context.Context, client vpcDHCPOptionsClient, input *ec2.DescribeDhcpOptionsInput) ([]ec2types.DhcpOptions, error) {
	var all []ec2types.DhcpOptions
	for {
		out, err := client.DescribeDhcpOptions(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.DhcpOptions...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
