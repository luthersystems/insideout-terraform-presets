// CloudWatch Logs inspector tests. Covers the `/aws/<project>` prefix
// scoping that lets the panel show only this stack's log groups instead
// of fanning out per-resource ListTagsForResource calls.
//
// Ported from reliable internal/agentapi/aws_inspect_test.go cases for
// inspectCloudWatchLogs.

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cloudwatchlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCloudWatchLogsClient struct {
	out       *cloudwatchlogs.DescribeLogGroupsOutput
	err       error
	lastInput *cloudwatchlogs.DescribeLogGroupsInput
	calls     int
}

func (f *fakeCloudWatchLogsClient) DescribeLogGroups(_ context.Context, in *cloudwatchlogs.DescribeLogGroupsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	f.calls++
	f.lastInput = in
	if f.err != nil {
		return nil, f.err
	}
	if f.out == nil {
		return &cloudwatchlogs.DescribeLogGroupsOutput{}, nil
	}
	return f.out, nil
}

// TestDescribeProjectLogGroups_PrefixScoping pins the prefix scoping
// contract: a non-empty project becomes `/aws/<project>` so AWS does
// the filtering server-side.
func TestDescribeProjectLogGroups_PrefixScoping(t *testing.T) {
	t.Parallel()
	client := &fakeCloudWatchLogsClient{
		out: &cloudwatchlogs.DescribeLogGroupsOutput{
			LogGroups: []cloudwatchlogstypes.LogGroup{
				{LogGroupName: aws.String("/aws/myproj/lambda/foo")},
				{LogGroupName: aws.String("/aws/myproj/eks/cluster")},
			},
		},
	}

	got, err := describeProjectLogGroups(context.Background(), client, "myproj")
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.NotNil(t, client.lastInput, "DescribeLogGroups must be called once")
	require.NotNil(t, client.lastInput.LogGroupNamePrefix, "non-empty project must populate LogGroupNamePrefix")
	assert.Equal(t, "/aws/myproj", aws.ToString(client.lastInput.LogGroupNamePrefix))
}

// TestDescribeProjectLogGroups_EmptyProjectNoPrefix — when no project
// filter is supplied, the call must NOT pass a prefix or the panel
// shows nothing on stacks where log groups predate the convention.
func TestDescribeProjectLogGroups_EmptyProjectNoPrefix(t *testing.T) {
	t.Parallel()
	client := &fakeCloudWatchLogsClient{
		out: &cloudwatchlogs.DescribeLogGroupsOutput{
			LogGroups: []cloudwatchlogstypes.LogGroup{
				{LogGroupName: aws.String("/aws/lambda/legacy")},
			},
		},
	}

	got, err := describeProjectLogGroups(context.Background(), client, "")
	require.NoError(t, err)
	require.Len(t, got, 1)

	require.NotNil(t, client.lastInput)
	assert.Nil(t, client.lastInput.LogGroupNamePrefix, "empty project must NOT set a prefix")
}

// TestDescribeProjectLogGroups_APIError surfaces the error verbatim;
// callers (the dispatcher) decide whether to log+skip or propagate.
func TestDescribeProjectLogGroups_APIError(t *testing.T) {
	t.Parallel()
	client := &fakeCloudWatchLogsClient{err: errors.New("AccessDenied")}

	_, err := describeProjectLogGroups(context.Background(), client, "myproj")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

// TestDescribeProjectLogGroups_EmptyResult returns an empty slice (not
// nil) so the JSON wire shape is `[]` not `null`.
func TestDescribeProjectLogGroups_EmptyResult(t *testing.T) {
	t.Parallel()
	client := &fakeCloudWatchLogsClient{
		out: &cloudwatchlogs.DescribeLogGroupsOutput{LogGroups: []cloudwatchlogstypes.LogGroup{}},
	}

	got, err := describeProjectLogGroups(context.Background(), client, "myproj")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestInspectCloudWatchLogs_GetMetricsAction routes through
// metricsRouted, which deliberately returns ErrUseMetricsPackage so
// callers know to invoke pkg/observability/metrics directly. Pin the
// error chain so the routing contract doesn't silently change.
func TestInspectCloudWatchLogs_GetMetricsAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudWatchLogs(context.Background(), aws.Config{Region: "eu-west-2"}, "get-metrics", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUseMetricsPackage,
		"get-metrics must surface ErrUseMetricsPackage so callers route to pkg/observability/metrics")
	assert.Contains(t, err.Error(), "cloudwatchlogs",
		"the routed error must mention the service for log diagnosis")
}

// TestInspectCloudWatchLogs_UnknownAction returns the canonical
// unsupported-action error.
func TestInspectCloudWatchLogs_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudWatchLogs(context.Background(), aws.Config{Region: "eu-west-2"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloudwatchlogs")
	assert.Contains(t, err.Error(), "no-such-action")
}
