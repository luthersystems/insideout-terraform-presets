package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errVPCEndpointSeed is the package-level sentinel for vpc-endpoint
// error propagation — see vpc_test.go for the contract.
var errVPCEndpointSeed = errors.New("AccessDenied")

type fakeVPCEndpointClient struct {
	pages []ec2.DescribeVpcEndpointsOutput
	calls []ec2.DescribeVpcEndpointsInput
	err   error
}

func (f *fakeVPCEndpointClient) DescribeVpcEndpoints(_ context.Context, in *ec2.DescribeVpcEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeVpcEndpointsOutput{}, nil
	}
	return &f.pages[idx], nil
}

// vpcEndpointWithTags builds a VPC endpoint fixture. state is a free
// string that maps to ec2types.State (e.g. "available", "deleted",
// "deleting", "pending"); SDK type is a string-backed enum.
func vpcEndpointWithTags(id, vpcID, serviceName, endpointType, state string, tags map[string]string) ec2types.VpcEndpoint {
	vpce := ec2types.VpcEndpoint{
		VpcEndpointId:   aws.String(id),
		VpcId:           aws.String(vpcID),
		ServiceName:     aws.String(serviceName),
		VpcEndpointType: ec2types.VpcEndpointType(endpointType),
		State:           ec2types.State(state),
	}
	for k, v := range tags {
		vpce.Tags = append(vpce.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return vpce
}

func TestVPCEndpointDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient {
		return &fakeVPCEndpointClient{
			pages: []ec2.DescribeVpcEndpointsOutput{
				{
					VpcEndpoints: []ec2types.VpcEndpoint{
						vpcEndpointWithTags("vpce-0bf69dc65f738a357", "vpc-052c72972a11f8677", "com.amazonaws.us-east-1.s3", "Gateway", "available", map[string]string{"Name": "io-foo-s3-vpce", "Project": "io-foo"}),
						vpcEndpointWithTags("vpce-0a1b2c3d4e5f60718", "vpc-052c72972a11f8677", "com.amazonaws.us-east-1.dynamodb", "Interface", "available", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_vpc_endpoint" {
			t.Errorf("Type=%q, want aws_vpc_endpoint", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["vpc_endpoint_id"] == "" {
			t.Error("NativeIDs[vpc_endpoint_id] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["service_name"] == "" {
			t.Error("NativeIDs[service_name] empty")
		}
		if ir.Identity.NativeIDs["vpc_endpoint_type"] == "" {
			t.Error("NativeIDs[vpc_endpoint_type] empty")
		}
	}
	// Sorted by VPC-endpoint ID; vpce-0a1b... sorts before vpce-0bf6...
	if got[0].Identity.ImportID != "vpce-0a1b2c3d4e5f60718" {
		t.Errorf("first ImportID=%q, want vpce-0a1b2c3d4e5f60718 (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NameHint != "vpce-0a1b2c3d4e5f60718" {
		t.Errorf("first NameHint=%q, want fallback to VPC-endpoint ID", got[0].Identity.NameHint)
	}
	if got[1].Identity.NameHint != "io-foo-s3-vpce" {
		t.Errorf("second NameHint=%q, want io-foo-s3-vpce", got[1].Identity.NameHint)
	}
	if got[1].Identity.NativeIDs["vpc_endpoint_type"] != "Gateway" {
		t.Errorf("vpc_endpoint_type=%q, want Gateway", got[1].Identity.NativeIDs["vpc_endpoint_type"])
	}
	if got[0].Identity.NativeIDs["vpc_endpoint_type"] != "Interface" {
		t.Errorf("vpc_endpoint_type=%q, want Interface", got[0].Identity.NativeIDs["vpc_endpoint_type"])
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestVPCEndpointDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCEndpointClient{
		pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-aaa00000000000001", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil)}, NextToken: aws.String("tok1")},
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-bbb00000000000002", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil)}, NextToken: aws.String("tok2")},
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-ccc00000000000003", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil)}}, // terminal
		},
	}
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.calls) < 3 {
		t.Fatalf("DescribeVpcEndpoints calls=%d, want >=3", len(fake.calls))
	}
	if aws.ToString(fake.calls[1].NextToken) != "tok1" {
		t.Errorf("call[1].NextToken=%q, want tok1", aws.ToString(fake.calls[1].NextToken))
	}
	if aws.ToString(fake.calls[2].NextToken) != "tok2" {
		t.Errorf("call[2].NextToken=%q, want tok2", aws.ToString(fake.calls[2].NextToken))
	}
}

func TestVPCEndpointDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCEndpointClient{}
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeVpcEndpoints call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeVpcEndpointsInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestVPCEndpointDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCEndpointClient{}
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeVpcEndpoints call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestVPCEndpointDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient {
		return &fakeVPCEndpointClient{err: errVPCEndpointSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errVPCEndpointSeed) {
		t.Errorf("err=%v, want errors.Is(err, errVPCEndpointSeed)", err)
	}
}

// TestVPCEndpointDiscover_SkipsDeletedAndDeletingState pins the
// skip-list contract: VPC endpoints in deleted/deleting state cannot be
// imported (terraform import returns InvalidVpcEndpointId.NotFound) so
// the discoverer must drop them before emitting a manifest entry the
// operator cannot resolve. Mirrors the NAT skip-state pattern from
// PR #322.
func TestVPCEndpointDiscover_SkipsDeletedAndDeletingState(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient {
		return &fakeVPCEndpointClient{
			pages: []ec2.DescribeVpcEndpointsOutput{
				{
					VpcEndpoints: []ec2types.VpcEndpoint{
						vpcEndpointWithTags("vpce-aaa00000000000001", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil),
						vpcEndpointWithTags("vpce-bbb00000000000002", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "deleted", nil),
						vpcEndpointWithTags("vpce-ccc00000000000003", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "deleting", nil),
						vpcEndpointWithTags("vpce-ddd00000000000004", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "pending", nil),
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
	if !gotIDs["vpce-aaa00000000000001"] {
		t.Error("missing available VPC endpoint in result")
	}
	if !gotIDs["vpce-ddd00000000000004"] {
		t.Error("missing pending VPC endpoint in result (only deleted/deleting are skipped)")
	}
	if gotIDs["vpce-bbb00000000000002"] {
		t.Error("deleted VPC endpoint must be skipped")
	}
	if gotIDs["vpce-ccc00000000000003"] {
		t.Error("deleting VPC endpoint must be skipped")
	}
}

func TestVPCEndpointDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient {
		return &fakeVPCEndpointClient{pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{
				vpcEndpointWithTags("vpce-0bf69dc65f738a357", "vpc-052c72972a11f8677", "com.amazonaws.us-east-1.s3", "Gateway", "available", map[string]string{"Name": "io-foo-s3-vpce"}),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "vpce-0bf69dc65f738a357", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_vpc_endpoint" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "vpce-0bf69dc65f738a357" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-s3-vpce" {
		t.Errorf("NameHint=%q, want io-foo-s3-vpce", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.NativeIDs["service_name"] != "com.amazonaws.us-east-1.s3" {
		t.Errorf("NativeIDs[service_name]=%q", got.Identity.NativeIDs["service_name"])
	}
	if got.Identity.NativeIDs["vpc_endpoint_type"] != "Gateway" {
		t.Errorf("NativeIDs[vpc_endpoint_type]=%q", got.Identity.NativeIDs["vpc_endpoint_type"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestVPCEndpointDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient {
		return &fakeVPCEndpointClient{pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-0bf69dc65f738a357", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:vpc-endpoint/vpce-0bf69dc65f738a357", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "vpce-0bf69dc65f738a357" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestVPCEndpointDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return &fakeVPCEndpointClient{} }}
	_, err := d.DiscoverByID(context.Background(), "vpce-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestVPCEndpointDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient {
		return &fakeVPCEndpointClient{err: ec2APIError("InvalidVpcEndpointId.NotFound", "The VPC endpoint 'vpce-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "vpce-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestVPCEndpointDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeVPCEndpointClient{
		"us-east-1": {pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-east0000000000001", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-west0000000000001", "vpc-2", "com.amazonaws.eu-west-1.s3", "Gateway", "available", nil)}},
		}},
	}
	var seenRegions []string
	d := &vpcEndpointDiscoverer{new: func(region string) vpcEndpointClient {
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
		t.Error("expected one DescribeVpcEndpoints call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestVPCEndpointDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCEndpointClient{
		pages: []ec2.DescribeVpcEndpointsOutput{{VpcEndpoints: []ec2types.VpcEndpoint{
			vpcEndpointWithTags("vpce-prod000000000001", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", map[string]string{"Name": "prod-vpce", "env": "prod"}),
			vpcEndpointWithTags("vpce-stag000000000002", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", map[string]string{"Name": "staging-vpce", "env": "staging"}),
		}}},
	}
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod VPC endpoint should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-vpce" {
		t.Errorf("NameHint=%q, want prod-vpce", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestVPCEndpointDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeVPCEndpointClient{
		"us-east-1": {pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-east0000000000001", "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeVpcEndpointsOutput{
			{VpcEndpoints: []ec2types.VpcEndpoint{vpcEndpointWithTags("vpce-west0000000000001", "vpc-2", "com.amazonaws.eu-west-1.s3", "Gateway", "available", nil)}},
		}},
	}
	d := &vpcEndpointDiscoverer{new: func(region string) vpcEndpointClient { return fakes[region] }}
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
			if e.Service != "vpc_endpoint" {
				t.Errorf("event %d: service=%q, want vpc_endpoint", i, e.Service)
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

func TestVPCEndpointDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"vpce-aaa00000000000001", "vpce-bbb00000000000002", "vpce-ccc00000000000003"}
	vpces := make([]ec2types.VpcEndpoint, 0, len(ids))
	for _, id := range ids {
		vpces = append(vpces, vpcEndpointWithTags(id, "vpc-1", "com.amazonaws.us-east-1.s3", "Gateway", "available", nil))
	}
	fake := &fakeVPCEndpointClient{pages: []ec2.DescribeVpcEndpointsOutput{{VpcEndpoints: vpces}}}
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return fake }}
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
		if it.Service != "vpc_endpoint" {
			t.Errorf("item %d: service=%q, want vpc_endpoint", i, it.Service)
		}
		if it.TFType != "aws_vpc_endpoint" {
			t.Errorf("item %d: tf_type=%q, want aws_vpc_endpoint", i, it.TFType)
		}
	}
}

func TestVPCEndpointDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &vpcEndpointDiscoverer{new: func(_ string) vpcEndpointClient { return &fakeVPCEndpointClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not vpc-endpoint
		"vpc-1234",                              // wrong prefix
		"vpce 1234",                             // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
