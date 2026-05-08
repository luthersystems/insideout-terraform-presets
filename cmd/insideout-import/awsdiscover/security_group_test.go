package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errSGSeed is the package-level sentinel for security_group error
// propagation — see vpc_test.go for the contract.
var errSGSeed = errors.New("AccessDenied")

type fakeSecurityGroupClient struct {
	pages []ec2.DescribeSecurityGroupsOutput
	calls []ec2.DescribeSecurityGroupsInput
	err   error
}

func (f *fakeSecurityGroupClient) DescribeSecurityGroups(_ context.Context, in *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeSecurityGroupsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func sgWithTags(id, name, vpcID string, tags map[string]string) ec2types.SecurityGroup {
	g := ec2types.SecurityGroup{
		GroupId:   aws.String(id),
		GroupName: aws.String(name),
		VpcId:     aws.String(vpcID),
	}
	for k, v := range tags {
		g.Tags = append(g.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return g
}

func TestSecurityGroupDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient {
		return &fakeSecurityGroupClient{
			pages: []ec2.DescribeSecurityGroupsOutput{
				{
					SecurityGroups: []ec2types.SecurityGroup{
						sgWithTags("sg-0947e82d14371a085", "io-foo-app", "vpc-052c72972a11f8677", map[string]string{"Project": "io-foo"}),
						sgWithTags("sg-0aaaa00000000aaa1", "io-foo-db", "vpc-052c72972a11f8677", map[string]string{"Project": "io-foo"}),
					},
				},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != "aws_security_group" {
			t.Errorf("Type=%q, want aws_security_group", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["group_id"] == "" {
			t.Error("NativeIDs[group_id] empty")
		}
		if ir.Identity.NativeIDs["group_name"] == "" {
			t.Error("NativeIDs[group_name] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
	}
	// Output is sorted by group ID.
	if got[0].Identity.ImportID != "sg-0947e82d14371a085" {
		t.Errorf("first ImportID=%q, want sg-0947e82d14371a085 (sorted)", got[0].Identity.ImportID)
	}
	// GroupName wins as NameHint over any Name tag — for SGs the
	// GroupName is the canonical label.
	if got[0].Identity.NameHint != "io-foo-app" {
		t.Errorf("first NameHint=%q, want io-foo-app (GroupName)", got[0].Identity.NameHint)
	}
}

func TestSecurityGroupDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient {
		return &fakeSecurityGroupClient{
			pages: []ec2.DescribeSecurityGroupsOutput{
				{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-aaa00000000000001", "a", "vpc-1", nil)}, NextToken: aws.String("tok1")},
				{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-bbb00000000000002", "b", "vpc-1", nil)}, NextToken: aws.String("tok2")},
				{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-ccc00000000000003", "c", "vpc-1", nil)}}, // terminal
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
}

func TestSecurityGroupDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeSecurityGroupClient{}
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeSecurityGroups call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeSecurityGroupsInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestSecurityGroupDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeSecurityGroupClient{}
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeSecurityGroups call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestSecurityGroupDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient {
		return &fakeSecurityGroupClient{err: errSGSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errSGSeed) {
		t.Errorf("err=%v, want errors.Is(err, errSGSeed)", err)
	}
}

func TestSecurityGroupDiscoverByID_AcceptsGroupID(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient {
		return &fakeSecurityGroupClient{pages: []ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-0947e82d14371a085", "io-foo-app", "vpc-052c72972a11f8677", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "sg-0947e82d14371a085", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_security_group" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "sg-0947e82d14371a085" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-app" {
		t.Errorf("NameHint=%q, want io-foo-app", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestSecurityGroupDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient {
		return &fakeSecurityGroupClient{pages: []ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-0947e82d14371a085", "io-foo-app", "vpc-1", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:security-group/sg-0947e82d14371a085", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "sg-0947e82d14371a085" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestSecurityGroupDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient { return &fakeSecurityGroupClient{} }}
	_, err := d.DiscoverByID(context.Background(), "sg-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestSecurityGroupDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient {
		return &fakeSecurityGroupClient{err: errors.New("api error InvalidGroup.NotFound: The security group 'sg-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "sg-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestSecurityGroupDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeSecurityGroupClient{
		"us-east-1": {pages: []ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-east0000000000001", "east", "vpc-1", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-west0000000000001", "west", "vpc-2", nil)}},
		}},
	}
	var seenRegions []string
	d := &securityGroupDiscoverer{new: func(region string) securityGroupClient {
		seenRegions = append(seenRegions, region)
		f, ok := fakes[region]
		if !ok {
			t.Fatalf("closure called with unexpected region %q", region)
		}
		return f
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(seenRegions) != 2 {
		t.Errorf("region closure invocations = %v, want 2 entries", seenRegions)
	}
	if len(fakes["us-east-1"].calls) == 0 || len(fakes["eu-west-1"].calls) == 0 {
		t.Error("expected one DescribeSecurityGroups call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestSecurityGroupDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeSecurityGroupClient{
		pages: []ec2.DescribeSecurityGroupsOutput{{SecurityGroups: []ec2types.SecurityGroup{
			sgWithTags("sg-prod000000000001", "prod-sg", "vpc-1", map[string]string{"env": "prod"}),
			sgWithTags("sg-stag000000000002", "staging-sg", "vpc-1", map[string]string{"env": "staging"}),
		}}},
	}
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:      "io-foo",
		Regions:      []string{"us-east-1"},
		AccountID:    "123",
		TagSelectors: []TagSelector{{Key: "env", Value: "prod"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only env=prod SG should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-sg" {
		t.Errorf("NameHint=%q, want prod-sg", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestSecurityGroupDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeSecurityGroupClient{
		"us-east-1": {pages: []ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-east0000000000001", "east", "vpc-1", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{sgWithTags("sg-west0000000000001", "west", "vpc-2", nil)}},
		}},
	}
	d := &securityGroupDiscoverer{new: func(region string) securityGroupClient { return fakes[region] }}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	events := rec.snapshot()

	type bracket struct{ start, finish int }
	got := map[string]bracket{}
	for i, e := range events {
		switch e.Kind {
		case "service_start":
			b := got[e.Region]
			b.start = i + 1
			got[e.Region] = b
		case "service_finish":
			b := got[e.Region]
			b.finish = i + 1
			got[e.Region] = b
		}
		if e.Kind == "service_start" || e.Kind == "service_finish" {
			if e.Service != "security_group" {
				t.Errorf("event %d: service=%q, want security_group", i, e.Service)
			}
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if got[region].start == 0 || got[region].finish == 0 {
			t.Errorf("%s: missing service_start or service_finish: %+v", region, got[region])
		}
		if got[region].start >= got[region].finish {
			t.Errorf("%s: start at %d >= finish at %d", region, got[region].start, got[region].finish)
		}
	}
}

func TestSecurityGroupDiscover_EmitsItemFound_PerSecurityGroup(t *testing.T) {
	t.Parallel()
	ids := []string{"sg-aaa00000000000001", "sg-bbb00000000000002", "sg-ccc00000000000003"}
	gs := make([]ec2types.SecurityGroup, 0, len(ids))
	for _, id := range ids {
		gs = append(gs, sgWithTags(id, "name-"+id, "vpc-1", nil))
	}
	fake := &fakeSecurityGroupClient{pages: []ec2.DescribeSecurityGroupsOutput{{SecurityGroups: gs}}}
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient { return fake }}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "item_found" {
			items = append(items, e)
		}
	}
	if len(items) != len(got) {
		t.Errorf("item_found count = %d, want %d", len(items), len(got))
	}
	for i, it := range items {
		if it.Service != "security_group" {
			t.Errorf("item %d: service=%q, want security_group", i, it.Service)
		}
		if it.TFType != "aws_security_group" {
			t.Errorf("item %d: tf_type=%q, want aws_security_group", i, it.TFType)
		}
	}
}

func TestSecurityGroupDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &securityGroupDiscoverer{new: func(_ string) securityGroupClient { return &fakeSecurityGroupClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not security-group
		"vpc-1234",                              // wrong prefix
		"sg 1234",                               // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
