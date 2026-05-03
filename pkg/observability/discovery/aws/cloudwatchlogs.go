// CloudWatch Logs service inspector.
//
// Ported from reliable internal/agentapi/aws_inspect.go
// (cloudwatchlogs:790).
//
// Project scoping uses LogGroupNamePattern (server-side case-sensitive
// substring match) rather than LogGroupNamePrefix. Real-world preset
// log group names don't share a single prefix:
//
//   /aws/eks/<project>-prod-lu-eks0/cluster
//   /aws/rds/instance/<project>-prod-...-rds0/postgresql
//   /aws/lambda/<project>-...
//   /<project>-prod-...-cwl<id>/app
//
// A `/aws/<project>` prefix matches none of these. The project name
// itself is contained as a substring in every project-scoped log group
// emitted by these presets, so a substring filter scopes correctly
// across all four shapes. Verified live on cust2 (project
// `io-hrbs5zprbk51`, us-east-1) where the prefix path returned 0 of 4
// project-scoped log groups; the substring path returns all 4.
//
// LogGroupNamePattern reduces the response to {arn, creationTime,
// logGroupName}. Discovery only consumes logGroupName, so the field
// reduction is a non-issue.

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

// describeProjectLogGroups runs DescribeLogGroups, applying the project
// name as a LogGroupNamePattern (server-side substring match) when
// project is non-empty so callers see only this stack's groups —
// regardless of whether the log group's prefix is `/aws/eks/`,
// `/aws/rds/instance/`, `/aws/lambda/`, or the bare `/<project>-...`.
func describeProjectLogGroups(ctx context.Context, client cloudWatchLogsClient, project string) ([]cloudwatchlogstypes.LogGroup, error) {
	input := &cloudwatchlogs.DescribeLogGroupsInput{}
	if project != "" {
		input.LogGroupNamePattern = aws.String(project)
	}
	out, err := client.DescribeLogGroups(ctx, input)
	if err != nil {
		return nil, err
	}
	return out.LogGroups, nil
}
