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

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

func inspectCloudWatchLogs(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := cloudwatchlogs.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "describe-log-groups":
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
	case "get-metrics":
		return metricsRouted("cloudwatchlogs")
	default:
		return nil, unsupportedActionError("cloudwatchlogs", action)
	}
}
