package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// cwlClient defines the CloudWatch Logs API methods used by the discoverer.
type cwlClient interface {
	DescribeLogGroups(ctx context.Context, params *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
	ListTagsForResource(ctx context.Context, params *cloudwatchlogs.ListTagsForResourceInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.ListTagsForResourceOutput, error)
}

// CloudWatchLogsDiscoverer discovers CloudWatch Log Groups.
type CloudWatchLogsDiscoverer struct {
	client cwlClient
}

func NewCloudWatchLogsDiscoverer(cfg aws.Config) *CloudWatchLogsDiscoverer {
	return &CloudWatchLogsDiscoverer{client: cloudwatchlogs.NewFromConfig(cfg)}
}

func (d *CloudWatchLogsDiscoverer) ResourceType() string { return "aws_cloudwatch_log_group" }

func (d *CloudWatchLogsDiscoverer) Discover(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	// Search multiple prefix patterns since InsideOut log groups can be under
	// /<project> (CWL module) or /aws/lambda/<project> (Lambda-created).
	prefixes := []string{"/"}
	if filter.Project != "" {
		prefixes = []string{
			"/" + filter.Project,
			"/aws/lambda/" + filter.Project,
		}
	}

	seen := make(map[string]bool)
	var resources []DiscoveredResource

	for _, prefix := range prefixes {
		input := &cloudwatchlogs.DescribeLogGroupsInput{
			LogGroupNamePrefix: aws.String(prefix),
		}
		paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(d.client, input)
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("cloudwatchlogs describe log groups (prefix %s): %w", prefix, err)
			}
			for _, lg := range page.LogGroups {
				name := aws.ToString(lg.LogGroupName)
				if seen[name] {
					continue
				}
				seen[name] = true

				arn := aws.ToString(lg.Arn)

				tags, err := d.client.ListTagsForResource(ctx, &cloudwatchlogs.ListTagsForResourceInput{
					ResourceArn: aws.String(arn),
				})
				if err != nil {
					// ListTagsForResource requires a proper ARN. If the ARN is malformed
					// or the API returns an access error, return empty tags rather than
					// blocking discovery entirely. Tags are used for optional filtering only.
					tags = &cloudwatchlogs.ListTagsForResourceOutput{}
				}

				if len(filter.Tags) > 0 && !MatchesTags(tags.Tags, filter.Tags) {
					continue
				}

				resources = append(resources, DiscoveredResource{
					TerraformType: "aws_cloudwatch_log_group",
					ImportID:      name,
					Name:          name,
					Tags:          tags.Tags,
					ARN:           arn,
				})
			}
		}
	}
	return resources, nil
}
