package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errNetworkInterfaceSeed is the package-level sentinel for ENI error
// propagation — see vpc_test.go for the contract.
var errNetworkInterfaceSeed = errors.New("AccessDenied")

type fakeNetworkInterfaceClient struct {
	pages []ec2.DescribeNetworkInterfacesOutput
	calls []ec2.DescribeNetworkInterfacesInput
	err   error
}

func (f *fakeNetworkInterfaceClient) DescribeNetworkInterfaces(_ context.Context, in *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeNetworkInterfacesOutput{}, nil
	}
	return &f.pages[idx], nil
}

// networkInterfaceWithTags builds an ENI fixture. Note: ENI tags live on
// `TagSet`, not `Tags`, but the underlying type is the same []ec2types.Tag.
func networkInterfaceWithTags(id, vpcID, subnetID, interfaceType string, tags map[string]string) ec2types.NetworkInterface {
	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String(id),
		VpcId:              aws.String(vpcID),
		SubnetId:           aws.String(subnetID),
		InterfaceType:      ec2types.NetworkInterfaceType(interfaceType),
	}
	for k, v := range tags {
		eni.TagSet = append(eni.TagSet, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return eni
}

func TestNetworkInterfaceDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient {
		return &fakeNetworkInterfaceClient{
			pages: []ec2.DescribeNetworkInterfacesOutput{
				{
					NetworkInterfaces: []ec2types.NetworkInterface{
						networkInterfaceWithTags("eni-0cb0714368fa16eef", "vpc-052c72972a11f8677", "subnet-1aaa", "interface", map[string]string{"Name": "io-foo-lambda-eni", "Project": "io-foo"}),
						networkInterfaceWithTags("eni-0a1b2c3d4e5f60718", "vpc-052c72972a11f8677", "subnet-1bbb", "nat_gateway", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_network_interface" {
			t.Errorf("Type=%q, want aws_network_interface", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["network_interface_id"] == "" {
			t.Error("NativeIDs[network_interface_id] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["subnet_id"] == "" {
			t.Error("NativeIDs[subnet_id] empty")
		}
		if ir.Identity.NativeIDs["interface_type"] == "" {
			t.Error("NativeIDs[interface_type] empty")
		}
	}
	// Sorted by ENI ID; eni-0a1b... sorts before eni-0cb0...
	if got[0].Identity.ImportID != "eni-0a1b2c3d4e5f60718" {
		t.Errorf("first ImportID=%q, want eni-0a1b2c3d4e5f60718 (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NameHint != "eni-0a1b2c3d4e5f60718" {
		t.Errorf("first NameHint=%q, want fallback to ENI ID", got[0].Identity.NameHint)
	}
	if got[1].Identity.NameHint != "io-foo-lambda-eni" {
		t.Errorf("second NameHint=%q, want io-foo-lambda-eni (Name tag)", got[1].Identity.NameHint)
	}
	if got[0].Identity.NativeIDs["interface_type"] != "nat_gateway" {
		t.Errorf("first interface_type=%q, want nat_gateway", got[0].Identity.NativeIDs["interface_type"])
	}
	if got[1].Identity.NativeIDs["interface_type"] != "interface" {
		t.Errorf("second interface_type=%q, want interface", got[1].Identity.NativeIDs["interface_type"])
	}
	if got[1].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[1].Identity.Tags["Project"])
	}
}

func TestNetworkInterfaceDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient {
		return &fakeNetworkInterfaceClient{
			pages: []ec2.DescribeNetworkInterfacesOutput{
				{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-aaa00000000000001", "vpc-1", "subnet-1", "interface", nil)}, NextToken: aws.String("tok1")},
				{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-bbb00000000000002", "vpc-1", "subnet-1", "interface", nil)}, NextToken: aws.String("tok2")},
				{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-ccc00000000000003", "vpc-1", "subnet-1", "interface", nil)}}, // terminal
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

func TestNetworkInterfaceDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkInterfaceClient{}
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeNetworkInterfaces call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeNetworkInterfacesInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestNetworkInterfaceDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkInterfaceClient{}
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeNetworkInterfaces call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestNetworkInterfaceDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient {
		return &fakeNetworkInterfaceClient{err: errNetworkInterfaceSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errNetworkInterfaceSeed) {
		t.Errorf("err=%v, want errors.Is(err, errNetworkInterfaceSeed)", err)
	}
}

func TestNetworkInterfaceDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient {
		return &fakeNetworkInterfaceClient{pages: []ec2.DescribeNetworkInterfacesOutput{
			{NetworkInterfaces: []ec2types.NetworkInterface{
				networkInterfaceWithTags("eni-0cb0714368fa16eef", "vpc-052c72972a11f8677", "subnet-1", "interface", map[string]string{"Name": "io-foo-eni"}),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "eni-0cb0714368fa16eef", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_network_interface" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "eni-0cb0714368fa16eef" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-eni" {
		t.Errorf("NameHint=%q, want io-foo-eni", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.NativeIDs["interface_type"] != "interface" {
		t.Errorf("NativeIDs[interface_type]=%q", got.Identity.NativeIDs["interface_type"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestNetworkInterfaceDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient {
		return &fakeNetworkInterfaceClient{pages: []ec2.DescribeNetworkInterfacesOutput{
			{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-0cb0714368fa16eef", "vpc-1", "subnet-1", "interface", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:network-interface/eni-0cb0714368fa16eef", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "eni-0cb0714368fa16eef" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestNetworkInterfaceDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient { return &fakeNetworkInterfaceClient{} }}
	_, err := d.DiscoverByID(context.Background(), "eni-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestNetworkInterfaceDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient {
		return &fakeNetworkInterfaceClient{err: errors.New("api error InvalidNetworkInterfaceID.NotFound: The network interface 'eni-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "eni-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestNetworkInterfaceDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeNetworkInterfaceClient{
		"us-east-1": {pages: []ec2.DescribeNetworkInterfacesOutput{
			{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-east0000000000001", "vpc-1", "subnet-1", "interface", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeNetworkInterfacesOutput{
			{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-west0000000000001", "vpc-2", "subnet-2", "interface", nil)}},
		}},
	}
	var seenRegions []string
	d := &networkInterfaceDiscoverer{new: func(region string) networkInterfaceClient {
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
		t.Errorf("region closure invocations = %v, want 2", seenRegions)
	}
	if len(fakes["us-east-1"].calls) == 0 || len(fakes["eu-west-1"].calls) == 0 {
		t.Error("expected one DescribeNetworkInterfaces call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestNetworkInterfaceDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkInterfaceClient{
		pages: []ec2.DescribeNetworkInterfacesOutput{{NetworkInterfaces: []ec2types.NetworkInterface{
			networkInterfaceWithTags("eni-prod000000000001", "vpc-1", "subnet-1", "interface", map[string]string{"Name": "prod-eni", "env": "prod"}),
			networkInterfaceWithTags("eni-stag000000000002", "vpc-1", "subnet-1", "interface", map[string]string{"Name": "staging-eni", "env": "staging"}),
		}}},
	}
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod ENI should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-eni" {
		t.Errorf("NameHint=%q, want prod-eni", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestNetworkInterfaceDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeNetworkInterfaceClient{
		"us-east-1": {pages: []ec2.DescribeNetworkInterfacesOutput{
			{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-east0000000000001", "vpc-1", "subnet-1", "interface", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeNetworkInterfacesOutput{
			{NetworkInterfaces: []ec2types.NetworkInterface{networkInterfaceWithTags("eni-west0000000000001", "vpc-2", "subnet-2", "interface", nil)}},
		}},
	}
	d := &networkInterfaceDiscoverer{new: func(region string) networkInterfaceClient { return fakes[region] }}
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
			if e.Service != "network_interface" {
				t.Errorf("event %d: service=%q, want network_interface", i, e.Service)
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

func TestNetworkInterfaceDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"eni-aaa00000000000001", "eni-bbb00000000000002", "eni-ccc00000000000003"}
	enis := make([]ec2types.NetworkInterface, 0, len(ids))
	for _, id := range ids {
		enis = append(enis, networkInterfaceWithTags(id, "vpc-1", "subnet-1", "interface", nil))
	}
	fake := &fakeNetworkInterfaceClient{pages: []ec2.DescribeNetworkInterfacesOutput{{NetworkInterfaces: enis}}}
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient { return fake }}
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
		if it.Service != "network_interface" {
			t.Errorf("item %d: service=%q, want network_interface", i, it.Service)
		}
		if it.TFType != "aws_network_interface" {
			t.Errorf("item %d: tf_type=%q, want aws_network_interface", i, it.TFType)
		}
	}
}

func TestNetworkInterfaceDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &networkInterfaceDiscoverer{new: func(_ string) networkInterfaceClient { return &fakeNetworkInterfaceClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not network-interface
		"vpc-1234",                              // wrong prefix
		"eni 1234",                              // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
