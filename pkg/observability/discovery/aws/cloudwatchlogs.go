// CloudWatch Logs service inspector.
//
// Ported from reliable internal/agentapi/aws_inspect.go
// (cloudwatchlogs:790).
//
// CloudWatch Logs supports server-side LogGroupNamePrefix filtering, so
// project scoping happens via a `/aws/<project>` prefix rather than a
// fan-out tag check. This relies on the preset convention of naming
// project log groups under `/aws/<project>/...` — if the preset ever
// drops that convention, the prefix filter goes from "narrow" to
// "empty". Mirrors the prefix path reliable already shipped.

package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cloudwatchlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// cloudWatchLogsClient is the narrowed surface used by the
// describe-log-groups action. Lets tests inject a fake without doing
// real AWS auth.
type cloudWatchLogsClient interface {
	DescribeLogGroups(ctx context.Context, params *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
}

func inspectCloudWatchLogs(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)

	switch action {
	case "describe-log-groups":
		return describeProjectLogGroups(ctx, cloudwatchlogs.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("cloudwatchlogs")
	default:
		return nil, unsupportedActionError("cloudwatchlogs", action)
	}
}

// describeProjectLogGroups runs DescribeLogGroups, applying the
// `/aws/<project>` LogGroupNamePrefix filter when project is non-empty
// so callers see only this stack's groups.
func describeProjectLogGroups(ctx context.Context, client cloudWatchLogsClient, project string) ([]cloudwatchlogstypes.LogGroup, error) {
	input := &cloudwatchlogs.DescribeLogGroupsInput{}
	if project != "" {
		prefix := "/aws/" + project
		input.LogGroupNamePrefix = &prefix
	}
	out, err := client.DescribeLogGroups(ctx, input)
	if err != nil {
		return nil, err
	}
	return out.LogGroups, nil
}
