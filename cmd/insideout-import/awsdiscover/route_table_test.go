package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errRouteTableSeed is the package-level sentinel for route-table error
// propagation — see vpc_test.go for the contract.
var errRouteTableSeed = errors.New("AccessDenied")

type fakeRouteTableClient struct {
	pages []ec2.DescribeRouteTablesOutput
	calls []ec2.DescribeRouteTablesInput
	err   error
}

func (f *fakeRouteTableClient) DescribeRouteTables(_ context.Context, in *ec2.DescribeRouteTablesInput, _ ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeRouteTablesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func routeTableWithTags(id, vpcID string, tags map[string]string) ec2types.RouteTable {
	rt := ec2types.RouteTable{
		RouteTableId: aws.String(id),
		VpcId:        aws.String(vpcID),
	}
	for k, v := range tags {
		rt.Tags = append(rt.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return rt
}

func TestRouteTableDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient {
		return &fakeRouteTableClient{
			pages: []ec2.DescribeRouteTablesOutput{
				{
					RouteTables: []ec2types.RouteTable{
						routeTableWithTags("rtb-0af8ec4b68f7250fc", "vpc-052c72972a11f8677", map[string]string{"Name": "io-foo-public-rt", "Project": "io-foo"}),
						routeTableWithTags("rtb-0a1b2c3d4e5f60718", "vpc-052c72972a11f8677", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_route_table" {
			t.Errorf("Type=%q, want aws_route_table", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["route_table_id"] == "" {
			t.Error("NativeIDs[route_table_id] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
	}
	// Sorted by route-table ID; rtb-0a1b... sorts before rtb-0af8...
	if got[0].Identity.ImportID != "rtb-0a1b2c3d4e5f60718" {
		t.Errorf("first ImportID=%q, want rtb-0a1b2c3d4e5f60718 (sorted)", got[0].Identity.ImportID)
	}
	// Fallback: route table without Name tag uses route-table ID itself.
	if got[0].Identity.NameHint != "rtb-0a1b2c3d4e5f60718" {
		t.Errorf("first NameHint=%q, want fallback to route-table ID", got[0].Identity.NameHint)
	}
	// Name tag wins over the bare route-table ID for NameHint.
	if got[1].Identity.NameHint != "io-foo-public-rt" {
		t.Errorf("second NameHint=%q, want io-foo-public-rt (Name tag)", got[1].Identity.NameHint)
	}
	if got[1].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[1].Identity.Tags["Project"])
	}
}

func TestRouteTableDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient {
		return &fakeRouteTableClient{
			pages: []ec2.DescribeRouteTablesOutput{
				{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-aaa00000000000001", "vpc-1", nil)}, NextToken: aws.String("tok1")},
				{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-bbb00000000000002", "vpc-1", nil)}, NextToken: aws.String("tok2")},
				{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-ccc00000000000003", "vpc-1", nil)}}, // terminal
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

func TestRouteTableDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeRouteTableClient{}
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeRouteTables call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeRouteTablesInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestRouteTableDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeRouteTableClient{}
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeRouteTables call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestRouteTableDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient {
		return &fakeRouteTableClient{err: errRouteTableSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errRouteTableSeed) {
		t.Errorf("err=%v, want errors.Is(err, errRouteTableSeed)", err)
	}
}

func TestRouteTableDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient {
		return &fakeRouteTableClient{pages: []ec2.DescribeRouteTablesOutput{
			{RouteTables: []ec2types.RouteTable{
				routeTableWithTags("rtb-0af8ec4b68f7250fc", "vpc-052c72972a11f8677", map[string]string{"Name": "io-foo-public-rt"}),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "rtb-0af8ec4b68f7250fc", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_route_table" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "rtb-0af8ec4b68f7250fc" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-public-rt" {
		t.Errorf("NameHint=%q, want io-foo-public-rt", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestRouteTableDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient {
		return &fakeRouteTableClient{pages: []ec2.DescribeRouteTablesOutput{
			{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-0af8ec4b68f7250fc", "vpc-1", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:route-table/rtb-0af8ec4b68f7250fc", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "rtb-0af8ec4b68f7250fc" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestRouteTableDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient { return &fakeRouteTableClient{} }}
	_, err := d.DiscoverByID(context.Background(), "rtb-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestRouteTableDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient {
		return &fakeRouteTableClient{err: errors.New("api error InvalidRouteTableID.NotFound: The route table 'rtb-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "rtb-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestRouteTableDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeRouteTableClient{
		"us-east-1": {pages: []ec2.DescribeRouteTablesOutput{
			{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-east0000000000001", "vpc-1", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeRouteTablesOutput{
			{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-west0000000000001", "vpc-2", nil)}},
		}},
	}
	var seenRegions []string
	d := &routeTableDiscoverer{new: func(region string) routeTableClient {
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
		t.Error("expected one DescribeRouteTables call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestRouteTableDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeRouteTableClient{
		pages: []ec2.DescribeRouteTablesOutput{{RouteTables: []ec2types.RouteTable{
			routeTableWithTags("rtb-prod000000000001", "vpc-1", map[string]string{"Name": "prod-rt", "env": "prod"}),
			routeTableWithTags("rtb-stag000000000002", "vpc-1", map[string]string{"Name": "staging-rt", "env": "staging"}),
		}}},
	}
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod RT should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-rt" {
		t.Errorf("NameHint=%q, want prod-rt", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestRouteTableDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeRouteTableClient{
		"us-east-1": {pages: []ec2.DescribeRouteTablesOutput{
			{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-east0000000000001", "vpc-1", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeRouteTablesOutput{
			{RouteTables: []ec2types.RouteTable{routeTableWithTags("rtb-west0000000000001", "vpc-2", nil)}},
		}},
	}
	d := &routeTableDiscoverer{new: func(region string) routeTableClient { return fakes[region] }}
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
			if e.Service != "route_table" {
				t.Errorf("event %d: service=%q, want route_table", i, e.Service)
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

func TestRouteTableDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"rtb-aaa00000000000001", "rtb-bbb00000000000002", "rtb-ccc00000000000003"}
	rts := make([]ec2types.RouteTable, 0, len(ids))
	for _, id := range ids {
		rts = append(rts, routeTableWithTags(id, "vpc-1", nil))
	}
	fake := &fakeRouteTableClient{pages: []ec2.DescribeRouteTablesOutput{{RouteTables: rts}}}
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient { return fake }}
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
		if it.Service != "route_table" {
			t.Errorf("item %d: service=%q, want route_table", i, it.Service)
		}
		if it.TFType != "aws_route_table" {
			t.Errorf("item %d: tf_type=%q, want aws_route_table", i, it.TFType)
		}
	}
}

func TestRouteTableDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &routeTableDiscoverer{new: func(_ string) routeTableClient { return &fakeRouteTableClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not route-table
		"vpc-1234",                              // wrong prefix
		"rtb 1234",                              // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
