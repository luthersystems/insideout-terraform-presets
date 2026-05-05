package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

type fakeCWLClient struct {
	pages []cloudwatchlogs.DescribeLogGroupsOutput
	calls []cloudwatchlogs.DescribeLogGroupsInput
	err   error
}

func (f *fakeCWLClient) DescribeLogGroups(_ context.Context, in *cloudwatchlogs.DescribeLogGroupsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &cloudwatchlogs.DescribeLogGroupsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func TestCWLDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &cwlDiscoverer{new: func() cwlClient {
		return &fakeCWLClient{
			pages: []cloudwatchlogs.DescribeLogGroupsOutput{
				{
					LogGroups: []cwltypes.LogGroup{
						{LogGroupName: aws.String("/aws/lambda/io-foo-handler"), Arn: aws.String("arn:aws:logs:us-east-1:123:log-group:/aws/lambda/io-foo-handler:*")},
						{LogGroupName: aws.String("/aws/lambda/io-foo-worker"), Arn: aws.String("arn:aws:logs:us-east-1:123:log-group:/aws/lambda/io-foo-worker:*")},
					},
				},
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].Identity.ImportID != "/aws/lambda/io-foo-handler" {
		t.Errorf("first ImportID=%q, want log group name (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NativeIDs["arn"] == "" {
		t.Error("NativeIDs[arn] empty")
	}
}

func TestCWLDiscover_PassesPrefixServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeCWLClient{}
	d := &cwlDiscoverer{new: func() cwlClient { return fake }}
	if _, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 || fake.calls[0].LogGroupNamePrefix == nil || *fake.calls[0].LogGroupNamePrefix != "io-foo" {
		t.Errorf("expected LogGroupNamePrefix=io-foo, got %+v", fake.calls)
	}
}

func TestCWLDiscover_EmptyProjectPassesNoPrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeCWLClient{}
	d := &cwlDiscoverer{new: func() cwlClient { return fake }}
	if _, err := d.Discover(context.Background(), "", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeLogGroups call")
	}
	if fake.calls[0].LogGroupNamePrefix != nil {
		t.Errorf("LogGroupNamePrefix=%v, want nil for empty project", fake.calls[0].LogGroupNamePrefix)
	}
}

func TestCWLDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &cwlDiscoverer{new: func() cwlClient {
		return &fakeCWLClient{
			pages: []cloudwatchlogs.DescribeLogGroupsOutput{
				{LogGroups: []cwltypes.LogGroup{{LogGroupName: aws.String("/io-foo-a"), Arn: aws.String("a")}}, NextToken: aws.String("t1")},
				{LogGroups: []cwltypes.LogGroup{{LogGroupName: aws.String("/io-foo-b"), Arn: aws.String("b")}}}, // terminal
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestCWLDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &cwlDiscoverer{new: func() cwlClient { return &fakeCWLClient{err: errors.New("Throttling")} }}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}
