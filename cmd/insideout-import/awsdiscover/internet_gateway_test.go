package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errIGWSeed is the package-level sentinel for internet_gateway error
// propagation — see vpc_test.go for the contract.
var errIGWSeed = errors.New("AccessDenied")

type fakeInternetGatewayClient struct {
	pages []ec2.DescribeInternetGatewaysOutput
	calls []ec2.DescribeInternetGatewaysInput
	err   error
}

func (f *fakeInternetGatewayClient) DescribeInternetGateways(_ context.Context, in *ec2.DescribeInternetGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeInternetGatewaysOutput{}, nil
	}
	return &f.pages[idx], nil
}

func igwWithTags(id, vpcID string, tags map[string]string) ec2types.InternetGateway {
	g := ec2types.InternetGateway{
		InternetGatewayId: aws.String(id),
	}
	if vpcID != "" {
		g.Attachments = []ec2types.InternetGatewayAttachment{{VpcId: aws.String(vpcID)}}
	}
	for k, v := range tags {
		g.Tags = append(g.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return g
}

func TestInternetGatewayDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient {
		return &fakeInternetGatewayClient{
			pages: []ec2.DescribeInternetGatewaysOutput{
				{
					InternetGateways: []ec2types.InternetGateway{
						igwWithTags("igw-03550cf7bb845997c", "vpc-052c72972a11f8677", map[string]string{"Name": "io-foo-igw", "Project": "io-foo"}),
						igwWithTags("igw-0a1b2c3d4e5f60718", "", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_internet_gateway" {
			t.Errorf("Type=%q, want aws_internet_gateway", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["internet_gateway_id"] == "" {
			t.Error("NativeIDs[internet_gateway_id] empty")
		}
	}
	// Output is sorted by IGW ID.
	if got[0].Identity.ImportID != "igw-03550cf7bb845997c" {
		t.Errorf("first ImportID=%q, want igw-03550cf7bb845997c (sorted)", got[0].Identity.ImportID)
	}
	// Name tag wins over the bare IGW ID for NameHint.
	if got[0].Identity.NameHint != "io-foo-igw" {
		t.Errorf("first NameHint=%q, want io-foo-igw (Name tag)", got[0].Identity.NameHint)
	}
	// Attached VPC is propagated.
	if got[0].Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("first NativeIDs[vpc_id]=%q, want vpc-052c72972a11f8677", got[0].Identity.NativeIDs["vpc_id"])
	}
	// Detached IGW (no Attachments): no vpc_id key, NameHint falls back to IGW ID.
	if got[1].Identity.NameHint != "igw-0a1b2c3d4e5f60718" {
		t.Errorf("second NameHint=%q, want fallback to IGW ID", got[1].Identity.NameHint)
	}
	if _, ok := got[1].Identity.NativeIDs["vpc_id"]; ok {
		t.Errorf("second NativeIDs[vpc_id] should be unset for detached IGW")
	}
	// Tags propagate (filter+persist contract).
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestInternetGatewayDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeInternetGatewayClient{
		pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-aaa00000000000001", "vpc-1", nil)}, NextToken: aws.String("tok1")},
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-bbb00000000000002", "vpc-1", nil)}, NextToken: aws.String("tok2")},
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-ccc00000000000003", "vpc-1", nil)}}, // terminal
		},
	}
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.calls) < 3 {
		t.Fatalf("DescribeInternetGateways calls=%d, want >=3", len(fake.calls))
	}
	if aws.ToString(fake.calls[1].NextToken) != "tok1" {
		t.Errorf("call[1].NextToken=%q, want tok1", aws.ToString(fake.calls[1].NextToken))
	}
	if aws.ToString(fake.calls[2].NextToken) != "tok2" {
		t.Errorf("call[2].NextToken=%q, want tok2", aws.ToString(fake.calls[2].NextToken))
	}
}

func TestInternetGatewayDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeInternetGatewayClient{}
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeInternetGateways call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeInternetGatewaysInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestInternetGatewayDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeInternetGatewayClient{}
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeInternetGateways call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestInternetGatewayDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient {
		return &fakeInternetGatewayClient{err: errIGWSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errIGWSeed) {
		t.Errorf("err=%v, want errors.Is(err, errIGWSeed)", err)
	}
}

func TestInternetGatewayDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient {
		return &fakeInternetGatewayClient{pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-03550cf7bb845997c", "vpc-052c72972a11f8677", map[string]string{"Name": "io-foo-igw"})}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "igw-03550cf7bb845997c", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_internet_gateway" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "igw-03550cf7bb845997c" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-igw" {
		t.Errorf("NameHint=%q, want io-foo-igw", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestInternetGatewayDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient {
		return &fakeInternetGatewayClient{pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-03550cf7bb845997c", "vpc-1", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:internet-gateway/igw-03550cf7bb845997c", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "igw-03550cf7bb845997c" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestInternetGatewayDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return &fakeInternetGatewayClient{} }}
	_, err := d.DiscoverByID(context.Background(), "igw-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestInternetGatewayDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient {
		return &fakeInternetGatewayClient{err: ec2APIError("InvalidInternetGatewayID.NotFound", "The internet gateway 'igw-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "igw-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestInternetGatewayDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeInternetGatewayClient{
		"us-east-1": {pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-east0000000000001", "vpc-1", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-west0000000000001", "vpc-2", nil)}},
		}},
	}
	var seenRegions []string
	d := &internetGatewayDiscoverer{new: func(region string) internetGatewayClient {
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

	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seenRegions)
	}
	if len(fakes["us-east-1"].calls) == 0 || len(fakes["eu-west-1"].calls) == 0 {
		t.Error("expected one DescribeInternetGateways call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestInternetGatewayDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeInternetGatewayClient{
		pages: []ec2.DescribeInternetGatewaysOutput{{InternetGateways: []ec2types.InternetGateway{
			igwWithTags("igw-prod000000000001", "vpc-1", map[string]string{"Name": "prod-igw", "env": "prod"}),
			igwWithTags("igw-stag000000000002", "vpc-1", map[string]string{"Name": "staging-igw", "env": "staging"}),
		}}},
	}
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod IGW should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-igw" {
		t.Errorf("NameHint=%q, want prod-igw", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestInternetGatewayDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeInternetGatewayClient{
		"us-east-1": {pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-east0000000000001", "vpc-1", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeInternetGatewaysOutput{
			{InternetGateways: []ec2types.InternetGateway{igwWithTags("igw-west0000000000001", "vpc-2", nil)}},
		}},
	}
	d := &internetGatewayDiscoverer{new: func(region string) internetGatewayClient { return fakes[region] }}
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
			if e.Service != "internet_gateway" {
				t.Errorf("event %d: service=%q, want internet_gateway", i, e.Service)
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

func TestInternetGatewayDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"igw-aaa00000000000001", "igw-bbb00000000000002", "igw-ccc00000000000003"}
	gs := make([]ec2types.InternetGateway, 0, len(ids))
	for _, id := range ids {
		gs = append(gs, igwWithTags(id, "vpc-1", nil))
	}
	fake := &fakeInternetGatewayClient{pages: []ec2.DescribeInternetGatewaysOutput{{InternetGateways: gs}}}
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return fake }}
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
		if it.Service != "internet_gateway" {
			t.Errorf("item %d: service=%q, want internet_gateway", i, it.Service)
		}
		if it.TFType != "aws_internet_gateway" {
			t.Errorf("item %d: tf_type=%q, want aws_internet_gateway", i, it.TFType)
		}
		if it.Region != "us-east-1" {
			t.Errorf("item %d: region=%q, want us-east-1", i, it.Region)
		}
	}
	for _, e := range rec.snapshot() {
		if e.Kind == "service_finish" && e.Count != len(got) {
			t.Errorf("service_finish.count=%d, want %d", e.Count, len(got))
		}
	}
}

func TestInternetGatewayDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &internetGatewayDiscoverer{new: func(_ string) internetGatewayClient { return &fakeInternetGatewayClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not internet-gateway
		"vpc-1234",                              // wrong prefix
		"igw 1234",                              // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
