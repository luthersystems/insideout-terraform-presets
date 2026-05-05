package awsdiscover

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
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
