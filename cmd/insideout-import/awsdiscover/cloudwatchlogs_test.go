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

func TestCWLDiscover_PassesProjectAsLogGroupNamePattern(t *testing.T) {
	t.Parallel()
	fake := &fakeCWLClient{}
	d := &cwlDiscoverer{new: func() cwlClient { return fake }}
	if _, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeLogGroups call")
	}
	in := fake.calls[0]
	if in.LogGroupNamePattern == nil || *in.LogGroupNamePattern != "io-foo" {
		t.Errorf("LogGroupNamePattern=%v, want io-foo", in.LogGroupNamePattern)
	}
	// Pin: prefix must NOT be set — the two filters are mutually
	// exclusive at the server. A regression that sends both would fail
	// at runtime with InvalidParameterException.
	if in.LogGroupNamePrefix != nil {
		t.Errorf("LogGroupNamePrefix=%v, must be nil (mutually exclusive with LogGroupNamePattern)", in.LogGroupNamePrefix)
	}
}

func TestCWLDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeCWLClient{}
	d := &cwlDiscoverer{new: func() cwlClient { return fake }}
	if _, err := d.Discover(context.Background(), "", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeLogGroups call")
	}
	if fake.calls[0].LogGroupNamePattern != nil || fake.calls[0].LogGroupNamePrefix != nil {
		t.Errorf("expected no filter for empty project; pattern=%v prefix=%v",
			fake.calls[0].LogGroupNamePattern, fake.calls[0].LogGroupNamePrefix)
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

func TestCWLDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	name := "/aws/lambda/io-foo-handler"
	arn := "arn:aws:logs:us-east-1:123:log-group:" + name + ":*"
	d := &cwlDiscoverer{new: func() cwlClient {
		return &fakeCWLClient{
			pages: []cloudwatchlogs.DescribeLogGroupsOutput{
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String(name), Arn: aws.String(arn)},
				}},
			},
		}
	}}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_cloudwatch_log_group" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != name {
		t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, name)
	}
	if got.Identity.NativeIDs["arn"] != arn {
		t.Errorf("NativeIDs[arn]=%q, want %q", got.Identity.NativeIDs["arn"], arn)
	}
}

func TestCWLDiscoverByID_AcceptsBareName(t *testing.T) {
	t.Parallel()
	name := "/aws/lambda/io-foo-handler"
	d := &cwlDiscoverer{new: func() cwlClient {
		return &fakeCWLClient{
			pages: []cloudwatchlogs.DescribeLogGroupsOutput{
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String(name), Arn: aws.String("arn:test")},
				}},
			},
		}
	}}
	got, err := d.DiscoverByID(context.Background(), name, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NameHint != name {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
}

func TestCWLDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &cwlDiscoverer{new: func() cwlClient {
		// Empty pages → CWL returns empty list (no typed not-found error).
		return &fakeCWLClient{}
	}}
	_, err := d.DiscoverByID(context.Background(), "/aws/lambda/missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestCWLDiscoverByID_NotFoundOnPrefixMatchOnly(t *testing.T) {
	t.Parallel()
	// CWL prefix match returns names that share a prefix; DiscoverByID
	// must require an exact match.
	d := &cwlDiscoverer{new: func() cwlClient {
		return &fakeCWLClient{
			pages: []cloudwatchlogs.DescribeLogGroupsOutput{
				{LogGroups: []cwltypes.LogGroup{
					{LogGroupName: aws.String("/aws/lambda/missing-extended"), Arn: aws.String("arn:test")},
				}},
			},
		}
	}}
	_, err := d.DiscoverByID(context.Background(), "/aws/lambda/missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestCWLDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &cwlDiscoverer{new: func() cwlClient { return &fakeCWLClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:logs:us-east-1:123:metric-filter", // wrong resource type
		"name with spaces",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
