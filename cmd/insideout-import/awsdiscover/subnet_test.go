package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errSubnetSeed is the package-level sentinel for subnet error
// propagation — see vpc_test.go for the contract.
var errSubnetSeed = errors.New("AccessDenied")

type fakeSubnetClient struct {
	pages []ec2.DescribeSubnetsOutput
	calls []ec2.DescribeSubnetsInput
	err   error
}

func (f *fakeSubnetClient) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeSubnetsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func subnetWithTags(id, vpcID, az, cidr string, tags map[string]string) ec2types.Subnet {
	s := ec2types.Subnet{
		SubnetId:         aws.String(id),
		VpcId:            aws.String(vpcID),
		AvailabilityZone: aws.String(az),
		CidrBlock:        aws.String(cidr),
	}
	for k, v := range tags {
		s.Tags = append(s.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return s
}

func TestSubnetDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient {
		return &fakeSubnetClient{
			pages: []ec2.DescribeSubnetsOutput{
				{
					Subnets: []ec2types.Subnet{
						subnetWithTags("subnet-aaaa00000000001", "vpc-052c72972a11f8677", "us-east-1a", "10.0.1.0/24", map[string]string{"Name": "io-foo-private-1a", "Project": "io-foo"}),
						subnetWithTags("subnet-bbbb00000000002", "vpc-052c72972a11f8677", "us-east-1b", "10.0.2.0/24", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_subnet" {
			t.Errorf("Type=%q, want aws_subnet", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["subnet_id"] == "" {
			t.Error("NativeIDs[subnet_id] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["availability_zone"] == "" {
			t.Error("NativeIDs[availability_zone] empty")
		}
		if ir.Identity.NativeIDs["cidr_block"] == "" {
			t.Error("NativeIDs[cidr_block] empty")
		}
	}
	// Output is sorted by subnet ID.
	if got[0].Identity.ImportID != "subnet-aaaa00000000001" {
		t.Errorf("first ImportID=%q, want subnet-aaaa00000000001 (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NameHint != "io-foo-private-1a" {
		t.Errorf("first NameHint=%q, want io-foo-private-1a (Name tag)", got[0].Identity.NameHint)
	}
	if got[1].Identity.NameHint != "subnet-bbbb00000000002" {
		t.Errorf("second NameHint=%q, want fallback to subnet ID", got[1].Identity.NameHint)
	}
}

func TestSubnetDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeSubnetClient{
		pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-a000000000000001", "vpc-1", "us-east-1a", "10.0.1.0/24", nil)}, NextToken: aws.String("tok1")},
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-b000000000000002", "vpc-1", "us-east-1b", "10.0.2.0/24", nil)}, NextToken: aws.String("tok2")},
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-c000000000000003", "vpc-1", "us-east-1c", "10.0.3.0/24", nil)}}, // terminal
		},
	}
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.calls) < 3 {
		t.Fatalf("DescribeSubnets calls=%d, want >=3", len(fake.calls))
	}
	if aws.ToString(fake.calls[1].NextToken) != "tok1" {
		t.Errorf("call[1].NextToken=%q, want tok1", aws.ToString(fake.calls[1].NextToken))
	}
	if aws.ToString(fake.calls[2].NextToken) != "tok2" {
		t.Errorf("call[2].NextToken=%q, want tok2", aws.ToString(fake.calls[2].NextToken))
	}
}

func TestSubnetDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeSubnetClient{}
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeSubnets call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeSubnetsInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestSubnetDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeSubnetClient{}
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeSubnets call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestSubnetDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient {
		return &fakeSubnetClient{err: errSubnetSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errSubnetSeed) {
		t.Errorf("err=%v, want errors.Is(err, errSubnetSeed)", err)
	}
}

func TestSubnetDiscoverByID_AcceptsSubnetID(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient {
		return &fakeSubnetClient{pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-aaaa00000000001", "vpc-052c72972a11f8677", "us-east-1a", "10.0.1.0/24", map[string]string{"Name": "io-foo-private-1a"})}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "subnet-aaaa00000000001", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_subnet" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "subnet-aaaa00000000001" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-private-1a" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.NativeIDs["availability_zone"] != "us-east-1a" {
		t.Errorf("NativeIDs[availability_zone]=%q", got.Identity.NativeIDs["availability_zone"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestSubnetDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient {
		return &fakeSubnetClient{pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-aaaa00000000001", "vpc-1", "us-east-1a", "10.0.1.0/24", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:subnet/subnet-aaaa00000000001", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "subnet-aaaa00000000001" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestSubnetDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return &fakeSubnetClient{} }}
	_, err := d.DiscoverByID(context.Background(), "subnet-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestSubnetDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient {
		return &fakeSubnetClient{err: ec2APIError("InvalidSubnetID.NotFound", "The subnet ID 'subnet-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "subnet-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestSubnetDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeSubnetClient{
		"us-east-1": {pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-east0000000001", "vpc-1", "us-east-1a", "10.0.1.0/24", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-west0000000001", "vpc-2", "eu-west-1a", "10.1.1.0/24", nil)}},
		}},
	}
	var seenRegions []string
	d := &subnetDiscoverer{new: func(region string) subnetClient {
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
		t.Error("expected one DescribeSubnets call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestSubnetDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeSubnetClient{
		pages: []ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{
			subnetWithTags("subnet-prod000000001", "vpc-1", "us-east-1a", "10.0.1.0/24", map[string]string{"Name": "prod-1a", "env": "prod"}),
			subnetWithTags("subnet-stag000000001", "vpc-1", "us-east-1a", "10.0.2.0/24", map[string]string{"Name": "staging-1a", "env": "staging"}),
		}}},
	}
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod subnet should pass)", len(got))
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestSubnetDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeSubnetClient{
		"us-east-1": {pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-east0000000001", "vpc-1", "us-east-1a", "10.0.1.0/24", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeSubnetsOutput{
			{Subnets: []ec2types.Subnet{subnetWithTags("subnet-west0000000001", "vpc-2", "eu-west-1a", "10.1.1.0/24", nil)}},
		}},
	}
	d := &subnetDiscoverer{new: func(region string) subnetClient { return fakes[region] }}
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
			if e.Service != "subnet" {
				t.Errorf("event %d: service=%q, want subnet", i, e.Service)
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

func TestSubnetDiscover_EmitsItemFound_PerSubnet(t *testing.T) {
	t.Parallel()
	ids := []string{"subnet-aaa00000000001", "subnet-bbb00000000002", "subnet-ccc00000000003"}
	subs := make([]ec2types.Subnet, 0, len(ids))
	for _, id := range ids {
		subs = append(subs, subnetWithTags(id, "vpc-1", "us-east-1a", "10.0.1.0/24", nil))
	}
	fake := &fakeSubnetClient{pages: []ec2.DescribeSubnetsOutput{{Subnets: subs}}}
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return fake }}
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
		if it.Service != "subnet" {
			t.Errorf("item %d: service=%q, want subnet", i, it.Service)
		}
		if it.TFType != "aws_subnet" {
			t.Errorf("item %d: tf_type=%q, want aws_subnet", i, it.TFType)
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

func TestSubnetDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &subnetDiscoverer{new: func(_ string) subnetClient { return &fakeSubnetClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not subnet
		"vpc-1234",                              // wrong prefix
		"subnet 1234",                           // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
