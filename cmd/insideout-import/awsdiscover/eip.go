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

// eipClient is the narrow subset of the EC2 SDK the EIP discoverer uses.
// The describe response carries the tag map inline so no separate
// DescribeTags round-trip is required (Bundle 4 / #321).
//
// Note: DescribeAddresses does NOT paginate — the EC2 API returns the
// full set of Elastic IPs in a single response per region. The
// discoverer therefore makes exactly one DescribeAddresses call per
// region (no NextToken loop).
type eipClient interface {
	DescribeAddresses(ctx context.Context, in *ec2.DescribeAddressesInput, opts ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error)
}

type eipDiscoverer struct {
	new func(region string) eipClient
}

func newEIPDiscoverer(cfg aws.Config) Discoverer {
	return &eipDiscoverer{new: func(region string) eipClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *eipDiscoverer) ResourceType() string { return "aws_eip" }

// Discover lists Elastic IPs whose Project tag matches args.Project. EC2
// supports server-side `tag:Key` filters on DescribeAddresses, so we
// never have to download every EIP in the account just to filter
// client-side. When args.Project is empty (admin path) the request
// omits the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.Address — no per-resource
// DescribeTags fetch is needed.
//
// Import ID for aws_eip is the allocation ID (eipalloc-XXXXXXXX).
func (d *eipDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "eip"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeAddressesInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		resp, err := client.DescribeAddresses(ctx, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeAddresses (region=%s): %w", region, err)
		}
		eips := resp.Addresses

		// Sort by allocation ID so the emitted manifest is deterministic
		// across runs.
		sort.Slice(eips, func(i, j int) bool {
			return aws.ToString(eips[i].AllocationId) < aws.ToString(eips[j].AllocationId)
		})

		for i := range eips {
			e := &eips[i]
			id := aws.ToString(e.AllocationId)
			if id == "" {
				// EC2-Classic EIPs have no AllocationId — they're
				// addressed by PublicIp only and aws_eip's import ID
				// must be an allocation ID, so skip.
				continue
			}
			tags := ec2TagsToMap(e.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			name := vpcName(e.Tags, id)
			native := map[string]string{
				"allocation_id": id,
				"domain":        string(e.Domain),
			}
			if v := aws.ToString(e.PublicIp); v != "" {
				native["public_ip"] = v
			}
			if v := aws.ToString(e.AssociationId); v != "" {
				native["association_id"] = v
			}
			out = append(out, makeImportedResource(
				book,
				"aws_eip",
				name,
				id,
				region,
				args.AccountID,
				native,
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_eip", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves an Elastic IP by allocation ID
// (eipalloc-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:elastic-ip/eipalloc-XXXXXXXX). Issues
// a single DescribeAddresses call with the allocation ID to verify
// existence.
func (d *eipDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	allocID, err := eipIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{AllocationIds: []string{allocID}})
	if err != nil {
		// EC2 surfaces "InvalidAllocationID.NotFound" as a
		// smithy.APIError; inspect via errors.As.
		if isEC2APIErrorCode(err, "InvalidAllocationID.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_eip %q: %w", allocID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeAddresses: %w", err)
	}
	if len(out.Addresses) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_eip %q: %w", allocID, ErrNotFound)
	}
	e := &out.Addresses[0]
	name := vpcName(e.Tags, allocID)
	native := map[string]string{
		"allocation_id": allocID,
		"domain":        string(e.Domain),
	}
	if v := aws.ToString(e.PublicIp); v != "" {
		native["public_ip"] = v
	}
	if v := aws.ToString(e.AssociationId); v != "" {
		native["association_id"] = v
	}
	return makeImportedResource(
		addressBook{},
		"aws_eip",
		name,
		allocID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// eipIDFromID extracts the allocation ID (eipalloc-XXXXXXXX) from one of
// two accepted inputs: the bare allocation ID, or an EC2 ARN of the
// shape arn:aws:ec2:<region>:<account>:elastic-ip/eipalloc-XXXXXXXX.
// Anything else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func eipIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("eip: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("eip: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("eip: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "elastic-ip" || !strings.HasPrefix(parts[1], "eipalloc-") {
			return "", fmt.Errorf("eip: arn resource %q is not elastic-ip/eipalloc-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "eipalloc-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("eip: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
