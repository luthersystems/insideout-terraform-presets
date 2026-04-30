package discovery

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

type mockCWL struct {
	describeLogGroupsPages map[string][]cloudwatchlogs.DescribeLogGroupsOutput
	listTagsResp           map[string]*cloudwatchlogs.ListTagsForResourceOutput
	describeErr            error
	listTagsErr            error
	pageIdx                map[string]int
}

func (m *mockCWL) DescribeLogGroups(_ context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	prefix := aws.ToString(input.LogGroupNamePrefix)
	if m.pageIdx == nil {
		m.pageIdx = make(map[string]int)
	}
	idx := m.pageIdx[prefix]
	pages := m.describeLogGroupsPages[prefix]
	if idx >= len(pages) {
		return &cloudwatchlogs.DescribeLogGroupsOutput{}, nil
	}
	page := pages[idx]
	m.pageIdx[prefix] = idx + 1
	return &page, nil
}

func (m *mockCWL) ListTagsForResource(_ context.Context, input *cloudwatchlogs.ListTagsForResourceInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.ListTagsForResourceOutput, error) {
	if m.listTagsErr != nil {
		return nil, m.listTagsErr
	}
	if resp, ok := m.listTagsResp[aws.ToString(input.ResourceArn)]; ok {
		return resp, nil
	}
	return &cloudwatchlogs.ListTagsForResourceOutput{}, nil
}

func TestCloudWatchLogsDiscoverer_Discover(t *testing.T) {
	lgARN := "arn:aws:logs:us-east-1:123456789012:log-group:/my-project-logs/app:*"

	mock := &mockCWL{
		describeLogGroupsPages: map[string][]cloudwatchlogs.DescribeLogGroupsOutput{
			"/my-project": {
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String("/my-project-logs/app"), Arn: aws.String(lgARN)},
				}},
			},
			"/aws/lambda/my-project": {
				{LogGroups: []cwltypes.LogGroup{}},
			},
		},
		listTagsResp: map[string]*cloudwatchlogs.ListTagsForResourceOutput{
			lgARN: {Tags: map[string]string{"Project": "my-project"}},
		},
	}

	d := &CloudWatchLogsDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}

	r := resources[0]
	if r.Name != "/my-project-logs/app" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.ImportID != "/my-project-logs/app" {
		t.Errorf("ImportID = %q (should be log group name)", r.ImportID)
	}
	if r.TerraformType != "aws_cloudwatch_log_group" {
		t.Errorf("TerraformType = %q", r.TerraformType)
	}
}

func TestCloudWatchLogsDiscoverer_Deduplication(t *testing.T) {
	lgARN := "arn:aws:logs:us-east-1:123:log-group:/my-project-logs:*"

	// Same log group appears under both prefix searches
	mock := &mockCWL{
		describeLogGroupsPages: map[string][]cloudwatchlogs.DescribeLogGroupsOutput{
			"/my-project": {
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String("/my-project-logs"), Arn: aws.String(lgARN)},
				}},
			},
			"/aws/lambda/my-project": {
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String("/my-project-logs"), Arn: aws.String(lgARN)},
				}},
			},
		},
	}

	d := &CloudWatchLogsDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	// Should deduplicate — only 1 resource despite appearing in both prefix searches
	if len(resources) != 1 {
		t.Errorf("expected 1 resource (deduped), got %d", len(resources))
	}
}

// TestCloudWatchLogsDiscoverer_TagErrorFailsLoud pins the symmetric tag-
// error policy. Pre-#58-review CWL silently swallowed ListTagsForResource
// errors and emitted the resource with empty tags — which (a) silently
// dropped resources from --tags filtering and (b) emitted a misleading
// "no tags" report for resources whose tags were inaccessible. Both
// outcomes are wrong for a security-relevant tool. Lambda / SQS /
// DynamoDB already failed-loud on tag errors; CWL now matches them.
// (SecretsManager has no separate tag call — tags are inline in
// ListSecrets — so it has no tag-step to fail-loud on.)
func TestCloudWatchLogsDiscoverer_TagErrorFailsLoud(t *testing.T) {
	mock := &mockCWL{
		describeLogGroupsPages: map[string][]cloudwatchlogs.DescribeLogGroupsOutput{
			"/my-project": {
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String("/my-project-logs"), Arn: aws.String("arn:aws:logs:us-east-1:123456789012:log-group:/my-project-logs")},
				}},
			},
			"/aws/lambda/my-project": {},
		},
		listTagsErr: fmt.Errorf("access denied"),
	}

	d := &CloudWatchLogsDiscoverer{client: mock}
	_, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err == nil {
		t.Fatal("Discover() must fail loud on tag-listing error (symmetric with Lambda / SQS / DynamoDB / SecretsManager)")
	}
	if !strings.Contains(err.Error(), "list tags for /my-project-logs") {
		t.Errorf("error must name the log group whose tags failed; got %v", err)
	}
}
