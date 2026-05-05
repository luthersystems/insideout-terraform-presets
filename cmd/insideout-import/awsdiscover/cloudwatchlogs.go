package awsdiscover

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// cwlClient is the narrow subset of the CloudWatch Logs SDK we consume.
type cwlClient interface {
	DescribeLogGroups(ctx context.Context, in *cloudwatchlogs.DescribeLogGroupsInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
}

type cwlDiscoverer struct {
	new func() cwlClient
}

func newCloudWatchLogsDiscoverer(cfg aws.Config) Discoverer {
	return &cwlDiscoverer{new: func() cwlClient { return cloudwatchlogs.NewFromConfig(cfg) }}
}

func (d *cwlDiscoverer) ResourceType() string { return "aws_cloudwatch_log_group" }

// Discover finds log groups whose name *contains* the project name. We
// use the CWL API's LogGroupNamePattern (server-side case-sensitive
// substring match), not LogGroupNamePrefix — Lambda-emitted log groups
// are named `/aws/lambda/<fn>` and inspector-style log groups
// (`/<project>-...`) start with `/`, so a strict prefix match would miss
// them. Substring match keeps the filter server-side without losing
// either of those two common shapes.
//
// Import ID for aws_cloudwatch_log_group is the log group name.
func (d *cwlDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()
	input := &cloudwatchlogs.DescribeLogGroupsInput{}
	if project != "" {
		p := project
		input.LogGroupNamePattern = &p
	}

	type group struct {
		name string
		arn  string
	}
	var groups []group

	for {
		out, err := client.DescribeLogGroups(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("DescribeLogGroups: %w", err)
		}
		for _, lg := range out.LogGroups {
			groups = append(groups, group{
				name: aws.ToString(lg.LogGroupName),
				arn:  aws.ToString(lg.Arn),
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}

	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(groups))
	for _, g := range groups {
		imps = append(imps, makeImportedResource(
			book,
			"aws_cloudwatch_log_group",
			g.name,
			g.name,
			region,
			accountID,
			map[string]string{"arn": g.arn},
		))
	}
	return imps, nil
}

// DiscoverByID resolves a CloudWatch Logs log group by ARN
// (arn:aws:logs:<region>:<account>:log-group:<name>:*) or bare log
// group name. Walks DescribeLogGroups via NextToken until either the
// exact-name match is found or the prefix iterator is exhausted.
//
// Pagination is required because LogGroupNamePrefix returns *every*
// group whose name starts with the probe; on accounts with many
// prefix-collision siblings (e.g. `/aws/lambda/svc` and
// `/aws/lambda/svc-extra`) the exact match can land on page 2+. An
// empty/exhausted page set is treated as ErrNotFound (CWL returns a
// successful empty list rather than a typed error for missing groups).
func (d *cwlDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := cwlNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new()
	input := &cloudwatchlogs.DescribeLogGroupsInput{LogGroupNamePrefix: aws.String(name)}
	for {
		out, err := client.DescribeLogGroups(ctx, input)
		if err != nil {
			return imported.ImportedResource{}, fmt.Errorf("DescribeLogGroups: %w", err)
		}
		for _, lg := range out.LogGroups {
			if aws.ToString(lg.LogGroupName) == name {
				arn := aws.ToString(lg.Arn)
				return makeImportedResource(
					addressBook{},
					"aws_cloudwatch_log_group",
					name,
					name,
					region,
					accountID,
					map[string]string{"arn": arn},
				), nil
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			return imported.ImportedResource{}, fmt.Errorf("aws_cloudwatch_log_group %q: %w", name, ErrNotFound)
		}
		input.NextToken = out.NextToken
	}
}

// cwlNameFromID extracts the log group name from an ARN
// (arn:aws:logs:<region>:<account>:log-group:<name>[:*]) or bare name.
// CWL ARNs use a colon as the separator after "log-group" rather than a
// slash, and may carry a trailing ":*" wildcard. We normalize both.
func cwlNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("cloudwatchlogs: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("cloudwatchlogs: parse arn: %w", err)
		}
		if parsed.Service != "logs" {
			return "", fmt.Errorf("cloudwatchlogs: not a logs arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// CWL log group ARN resource format: log-group:<name>[:*]
		const prefix = "log-group:"
		if !strings.HasPrefix(parsed.Resource, prefix) {
			return "", fmt.Errorf("cloudwatchlogs: arn resource %q is not log-group:<name>: %w", parsed.Resource, ErrNotSupported)
		}
		rest := strings.TrimPrefix(parsed.Resource, prefix)
		// Strip trailing ":*" wildcard if present.
		rest = strings.TrimSuffix(rest, ":*")
		if rest == "" {
			return "", fmt.Errorf("cloudwatchlogs: empty name in arn %q: %w", id, ErrNotSupported)
		}
		return rest, nil
	}
	// Log group names commonly start with /aws/lambda/... so a leading slash
	// is allowed; only colons / spaces signal a malformed input.
	if strings.ContainsAny(id, ": ") {
		return "", fmt.Errorf("cloudwatchlogs: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
