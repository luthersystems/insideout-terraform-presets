package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errNATSeed is the package-level sentinel for nat_gateway error
// propagation — see vpc_test.go for the contract.
var errNATSeed = errors.New("AccessDenied")

type fakeNatGatewayClient struct {
	pages []ec2.DescribeNatGatewaysOutput
	calls []ec2.DescribeNatGatewaysInput
	err   error
}

func (f *fakeNatGatewayClient) DescribeNatGateways(_ context.Context, in *ec2.DescribeNatGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeNatGatewaysOutput{}, nil
	}
	return &f.pages[idx], nil
}

func natWithTags(id, vpcID, subnetID, publicIP string, state ec2types.NatGatewayState, tags map[string]string) ec2types.NatGateway {
	n := ec2types.NatGateway{
		NatGatewayId: aws.String(id),
		VpcId:        aws.String(vpcID),
		SubnetId:     aws.String(subnetID),
		State:        state,
	}
	if publicIP != "" {
		n.NatGatewayAddresses = []ec2types.NatGatewayAddress{{PublicIp: aws.String(publicIP)}}
	}
	for k, v := range tags {
		n.Tags = append(n.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return n
}

func TestNATGatewayDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient {
		return &fakeNatGatewayClient{
			pages: []ec2.DescribeNatGatewaysOutput{
				{
					NatGateways: []ec2types.NatGateway{
						natWithTags("nat-0bf36e3c90fe23bf5", "vpc-052c72972a11f8677", "subnet-1aaa", "203.0.113.10",
							ec2types.NatGatewayStateAvailable, map[string]string{"Name": "io-foo-nat", "Project": "io-foo"}),
						natWithTags("nat-0a1b2c3d4e5f60718", "vpc-052c72972a11f8677", "subnet-1bbb", "",
							ec2types.NatGatewayStateAvailable, map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_nat_gateway" {
			t.Errorf("Type=%q, want aws_nat_gateway", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["nat_gateway_id"] == "" {
			t.Error("NativeIDs[nat_gateway_id] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["subnet_id"] == "" {
			t.Error("NativeIDs[subnet_id] empty")
		}
	}
	if got[0].Identity.ImportID != "nat-0a1b2c3d4e5f60718" {
		t.Errorf("first ImportID=%q, want nat-0a1b2c3d4e5f60718 (sorted)", got[0].Identity.ImportID)
	}
	// The bf5-suffixed NAT (sorted second) carries the Name tag and PublicIp.
	if got[1].Identity.NameHint != "io-foo-nat" {
		t.Errorf("second NameHint=%q, want io-foo-nat (Name tag)", got[1].Identity.NameHint)
	}
	if got[1].Identity.NativeIDs["public_ip"] != "203.0.113.10" {
		t.Errorf("NativeIDs[public_ip]=%q, want 203.0.113.10", got[1].Identity.NativeIDs["public_ip"])
	}
	// First NAT has no PublicIp set: public_ip key absent.
	if _, ok := got[0].Identity.NativeIDs["public_ip"]; ok {
		t.Errorf("first NativeIDs[public_ip] should be unset for NAT without address")
	}
	if got[1].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[1].Identity.Tags["Project"])
	}
}

func TestNATGatewayDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeNatGatewayClient{
		pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-aaa00000000000001", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil)}, NextToken: aws.String("tok1")},
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-bbb00000000000002", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil)}, NextToken: aws.String("tok2")},
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-ccc00000000000003", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil)}}, // terminal
		},
	}
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.calls) < 3 {
		t.Fatalf("DescribeNatGateways calls=%d, want >=3", len(fake.calls))
	}
	if aws.ToString(fake.calls[1].NextToken) != "tok1" {
		t.Errorf("call[1].NextToken=%q, want tok1", aws.ToString(fake.calls[1].NextToken))
	}
	if aws.ToString(fake.calls[2].NextToken) != "tok2" {
		t.Errorf("call[2].NextToken=%q, want tok2", aws.ToString(fake.calls[2].NextToken))
	}
}

func TestNATGatewayDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeNatGatewayClient{}
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeNatGateways call")
	}
	in := fake.calls[0]
	// NB: DescribeNatGatewaysInput uses the singular `Filter` field.
	if len(in.Filter) == 0 {
		t.Fatal("expected at least one Filter on DescribeNatGatewaysInput")
	}
	if got := aws.ToString(in.Filter[0].Name); got != "tag:Project" {
		t.Errorf("Filter[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filter[0].Values) == 0 || in.Filter[0].Values[0] != "io-foo" {
		t.Errorf("Filter[0].Values=%v, want [io-foo]", in.Filter[0].Values)
	}
}

func TestNATGatewayDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeNatGatewayClient{}
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeNatGateways call")
	}
	if len(fake.calls[0].Filter) != 0 {
		t.Errorf("Filter=%v, want empty for empty project", fake.calls[0].Filter)
	}
}

func TestNATGatewayDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient {
		return &fakeNatGatewayClient{err: errNATSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errNATSeed) {
		t.Errorf("err=%v, want errors.Is(err, errNATSeed)", err)
	}
}

// TestNATGatewayDiscover_SkipsDeletedAndDeletingState pins the
// skip-list contract: NAT gateways in deleted/deleting state cannot be
// imported (terraform import returns NatGatewayNotFound) so the
// discoverer must drop them before emitting a manifest entry the
// operator cannot resolve. AWS keeps deleted NATs visible in
// DescribeNatGateways for ~1 hour after deletion, so this skip is
// essential against any non-empty-cleanup account.
func TestNATGatewayDiscover_SkipsDeletedAndDeletingState(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient {
		return &fakeNatGatewayClient{
			pages: []ec2.DescribeNatGatewaysOutput{
				{
					NatGateways: []ec2types.NatGateway{
						natWithTags("nat-aaa00000000000001", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil),
						natWithTags("nat-bbb00000000000002", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateDeleted, nil),
						natWithTags("nat-ccc00000000000003", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateDeleting, nil),
						natWithTags("nat-ddd00000000000004", "vpc-1", "subnet-1", "", ec2types.NatGatewayStatePending, nil),
					},
				},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	// Available + Pending pass; Deleted + Deleting drop.
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (deleted/deleting must be skipped)", len(got))
	}
	gotIDs := map[string]bool{}
	for _, ir := range got {
		gotIDs[ir.Identity.ImportID] = true
	}
	if !gotIDs["nat-aaa00000000000001"] {
		t.Error("missing available NAT in result")
	}
	if !gotIDs["nat-ddd00000000000004"] {
		t.Error("missing pending NAT in result (only deleted/deleting are skipped)")
	}
	if gotIDs["nat-bbb00000000000002"] {
		t.Error("deleted NAT must be skipped")
	}
	if gotIDs["nat-ccc00000000000003"] {
		t.Error("deleting NAT must be skipped")
	}
}

func TestNATGatewayDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient {
		return &fakeNatGatewayClient{pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{
				natWithTags("nat-0bf36e3c90fe23bf5", "vpc-052c72972a11f8677", "subnet-1", "203.0.113.10",
					ec2types.NatGatewayStateAvailable, map[string]string{"Name": "io-foo-nat"}),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "nat-0bf36e3c90fe23bf5", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_nat_gateway" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "nat-0bf36e3c90fe23bf5" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-nat" {
		t.Errorf("NameHint=%q, want io-foo-nat", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.NativeIDs["public_ip"] != "203.0.113.10" {
		t.Errorf("NativeIDs[public_ip]=%q", got.Identity.NativeIDs["public_ip"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestNATGatewayDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient {
		return &fakeNatGatewayClient{pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{
				natWithTags("nat-0bf36e3c90fe23bf5", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:natgateway/nat-0bf36e3c90fe23bf5", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "nat-0bf36e3c90fe23bf5" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestNATGatewayDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return &fakeNatGatewayClient{} }}
	_, err := d.DiscoverByID(context.Background(), "nat-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestNATGatewayDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient {
		return &fakeNatGatewayClient{err: ec2APIError("NatGatewayNotFound", "The NAT gateway 'nat-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "nat-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestNATGatewayDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeNatGatewayClient{
		"us-east-1": {pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-east0000000000001", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-west0000000000001", "vpc-2", "subnet-2", "", ec2types.NatGatewayStateAvailable, nil)}},
		}},
	}
	var seenRegions []string
	d := &natGatewayDiscoverer{new: func(region string) natGatewayClient {
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
		t.Error("expected one DescribeNatGateways call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestNATGatewayDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeNatGatewayClient{
		pages: []ec2.DescribeNatGatewaysOutput{{NatGateways: []ec2types.NatGateway{
			natWithTags("nat-prod000000000001", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, map[string]string{"Name": "prod-nat", "env": "prod"}),
			natWithTags("nat-stag000000000002", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, map[string]string{"Name": "staging-nat", "env": "staging"}),
		}}},
	}
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod NAT should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-nat" {
		t.Errorf("NameHint=%q, want prod-nat", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestNATGatewayDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeNatGatewayClient{
		"us-east-1": {pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-east0000000000001", "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeNatGatewaysOutput{
			{NatGateways: []ec2types.NatGateway{natWithTags("nat-west0000000000001", "vpc-2", "subnet-2", "", ec2types.NatGatewayStateAvailable, nil)}},
		}},
	}
	d := &natGatewayDiscoverer{new: func(region string) natGatewayClient { return fakes[region] }}
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
			if e.Service != "nat_gateway" {
				t.Errorf("event %d: service=%q, want nat_gateway", i, e.Service)
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

func TestNATGatewayDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"nat-aaa00000000000001", "nat-bbb00000000000002", "nat-ccc00000000000003"}
	ns := make([]ec2types.NatGateway, 0, len(ids))
	for _, id := range ids {
		ns = append(ns, natWithTags(id, "vpc-1", "subnet-1", "", ec2types.NatGatewayStateAvailable, nil))
	}
	fake := &fakeNatGatewayClient{pages: []ec2.DescribeNatGatewaysOutput{{NatGateways: ns}}}
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return fake }}
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
		if it.Service != "nat_gateway" {
			t.Errorf("item %d: service=%q, want nat_gateway", i, it.Service)
		}
		if it.TFType != "aws_nat_gateway" {
			t.Errorf("item %d: tf_type=%q, want aws_nat_gateway", i, it.TFType)
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

func TestNATGatewayDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &natGatewayDiscoverer{new: func(_ string) natGatewayClient { return &fakeNatGatewayClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not natgateway
		"vpc-1234",                              // wrong prefix
		"nat 1234",                              // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
