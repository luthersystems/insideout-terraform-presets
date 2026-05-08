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

// securityGroupClient is the narrow subset of the EC2 SDK the security
// group discoverer uses. The describe response carries the tag map inline
// so no separate DescribeTags round-trip is required (Bundle 4 / #319).
type securityGroupClient interface {
	DescribeSecurityGroups(ctx context.Context, in *ec2.DescribeSecurityGroupsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
}

type securityGroupDiscoverer struct {
	new func(region string) securityGroupClient
}

func newSecurityGroupDiscoverer(cfg aws.Config) Discoverer {
	return &securityGroupDiscoverer{new: func(region string) securityGroupClient {
		return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *securityGroupDiscoverer) ResourceType() string { return "aws_security_group" }

// Discover lists security groups whose Project tag matches args.Project.
// EC2 supports server-side `tag:Key` filters on DescribeSecurityGroups,
// so we never have to download every SG in the account just to filter
// client-side. When args.Project is empty (admin path) the request omits
// the filter.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Tags are inline on each ec2types.SecurityGroup — no
// per-resource DescribeTags fetch is needed.
//
// Import ID for aws_security_group is the group ID (sg-XXXXXXXX).
//
// Note: the AWS-managed default security group ("default" GroupName) on
// every VPC is currently included in the result set. Skip-list handling
// is deferred to Bundle 4 PR 3 (per #319's "out of scope" note).
func (d *securityGroupDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "security_group"
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)
		input := &ec2.DescribeSecurityGroupsInput{}
		if args.Project != "" {
			input.Filters = []ec2types.Filter{{
				Name:   aws.String("tag:Project"),
				Values: []string{args.Project},
			}}
		}

		groups, err := paginateDescribeSecurityGroups(ctx, client, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeSecurityGroups (region=%s): %w", region, err)
		}

		// Sort by group ID so the emitted manifest is deterministic across runs.
		sort.Slice(groups, func(i, j int) bool {
			return aws.ToString(groups[i].GroupId) < aws.ToString(groups[j].GroupId)
		})

		for i := range groups {
			g := &groups[i]
			id := aws.ToString(g.GroupId)
			tags := ec2TagsToMap(g.Tags)
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			// Prefer the SDK GroupName (always set) over a Name tag — for
			// security groups the GroupName is the canonical human-readable
			// label and is what the AWS console surfaces in pickers.
			name := aws.ToString(g.GroupName)
			if name == "" {
				name = id
			}
			out = append(out, makeImportedResource(
				book,
				"aws_security_group",
				name,
				id,
				region,
				args.AccountID,
				map[string]string{
					"group_id":   id,
					"group_name": aws.ToString(g.GroupName),
					"vpc_id":     aws.ToString(g.VpcId),
				},
				tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_security_group", id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// DiscoverByID resolves a security group by group ID (sg-XXXXXXXX) or ARN
// (arn:aws:ec2:<region>:<account>:security-group/sg-XXXXXXXX). Issues a
// single DescribeSecurityGroups call with the group ID to verify existence.
func (d *securityGroupDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	groupID, err := securityGroupIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{groupID}})
	if err != nil {
		// EC2 surfaces "InvalidGroup.NotFound" as a generic API error,
		// not a typed exception. Match by code substring (mirror s3.go).
		if strings.Contains(err.Error(), "InvalidGroup.NotFound") {
			return imported.ImportedResource{}, fmt.Errorf("aws_security_group %q: %w", groupID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeSecurityGroups: %w", err)
	}
	if len(out.SecurityGroups) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_security_group %q: %w", groupID, ErrNotFound)
	}
	g := &out.SecurityGroups[0]
	name := aws.ToString(g.GroupName)
	if name == "" {
		name = groupID
	}
	return makeImportedResource(
		addressBook{},
		"aws_security_group",
		name,
		groupID,
		region,
		accountID,
		map[string]string{
			"group_id":   groupID,
			"group_name": aws.ToString(g.GroupName),
			"vpc_id":     aws.ToString(g.VpcId),
		},
		nil,
	), nil
}

// securityGroupIDFromID extracts the group ID (sg-XXXXXXXX) from one of
// two accepted inputs: the bare group ID, or an EC2 ARN of the shape
// arn:aws:ec2:<region>:<account>:security-group/sg-XXXXXXXX. Anything
// else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func securityGroupIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("security_group: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("security_group: parse arn: %w", err)
		}
		if parsed.Service != "ec2" {
			return "", fmt.Errorf("security_group: not an ec2 arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "security-group" || !strings.HasPrefix(parts[1], "sg-") {
			return "", fmt.Errorf("security_group: arn resource %q is not security-group/sg-…: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if !strings.HasPrefix(id, "sg-") || strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("security_group: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}

// paginateDescribeSecurityGroups walks all NextToken pages.
func paginateDescribeSecurityGroups(ctx context.Context, client securityGroupClient, input *ec2.DescribeSecurityGroupsInput) ([]ec2types.SecurityGroup, error) {
	var all []ec2types.SecurityGroup
	for {
		out, err := client.DescribeSecurityGroups(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.SecurityGroups...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
