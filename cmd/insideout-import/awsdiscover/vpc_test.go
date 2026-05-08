package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errVPCSeed is the package-level sentinel returned by the fake VPC
// client in tests that want to assert error propagation. Tests should
// use errors.Is(err, errVPCSeed) rather than asserting only on
// `err != nil` — the latter masks regressions where the discover layer
// silently swallows the SDK error and returns a different one.
var errVPCSeed = errors.New("AccessDenied")

type fakeVPCClient struct {
	pages []ec2.DescribeVpcsOutput
	calls []ec2.DescribeVpcsInput
	err   error // when non-nil, every DescribeVpcs call returns this
}

func (f *fakeVPCClient) DescribeVpcs(_ context.Context, in *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeVpcsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func vpcWithTags(id, cidr string, tags map[string]string) ec2types.Vpc {
	v := ec2types.Vpc{
		VpcId:     aws.String(id),
		CidrBlock: aws.String(cidr),
	}
	for k, val := range tags {
		v.Tags = append(v.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(val)})
	}
	return v
}

func TestVPCDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &vpcDiscoverer{new: func(_ string) vpcClient {
		return &fakeVPCClient{
			pages: []ec2.DescribeVpcsOutput{
				{
					Vpcs: []ec2types.Vpc{
						vpcWithTags("vpc-052c72972a11f8677", "10.0.0.0/16", map[string]string{"Name": "io-foo-prod-vpc", "Project": "io-foo"}),
						vpcWithTags("vpc-0a1b2c3d4e5f60718", "10.1.0.0/16", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_vpc" {
			t.Errorf("Type=%q, want aws_vpc", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["cidr_block"] == "" {
			t.Error("NativeIDs[cidr_block] empty")
		}
		if ir.Identity.NativeIDs["name"] == "" {
			t.Error("NativeIDs[name] empty")
		}
	}
	// Output is sorted by VPC ID → addresses are deterministic.
	if got[0].Identity.ImportID != "vpc-052c72972a11f8677" {
		t.Errorf("first ImportID=%q, want vpc-052c72972a11f8677 (sorted)", got[0].Identity.ImportID)
	}
	// Name tag wins over the bare VPC ID for NameHint.
	if got[0].Identity.NameHint != "io-foo-prod-vpc" {
		t.Errorf("first NameHint=%q, want io-foo-prod-vpc (Name tag)", got[0].Identity.NameHint)
	}
	// Fallback: VPC with no Name tag uses the VPC ID itself.
	if got[1].Identity.NameHint != "vpc-0a1b2c3d4e5f60718" {
		t.Errorf("second NameHint=%q, want fallback to VPC ID", got[1].Identity.NameHint)
	}
	// Tags propagate onto Identity.Tags (filter+persist contract).
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestVPCDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &vpcDiscoverer{new: func(_ string) vpcClient {
		return &fakeVPCClient{
			pages: []ec2.DescribeVpcsOutput{
				{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-aaa00000000000001", "10.0.0.0/16", nil)}, NextToken: aws.String("tok1")},
				{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-bbb00000000000002", "10.1.0.0/16", nil)}, NextToken: aws.String("tok2")},
				{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-ccc00000000000003", "10.2.0.0/16", nil)}}, // terminal
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

func TestVPCDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCClient{}
	d := &vpcDiscoverer{new: func(_ string) vpcClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeVpcs call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeVpcsInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestVPCDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCClient{}
	d := &vpcDiscoverer{new: func(_ string) vpcClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeVpcs call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestVPCDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &vpcDiscoverer{new: func(_ string) vpcClient {
		return &fakeVPCClient{err: errVPCSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errVPCSeed) {
		t.Errorf("err=%v, want errors.Is(err, errVPCSeed) — discover swallowed the SDK error", err)
	}
}

func TestVPCDiscoverByID_AcceptsVPCID(t *testing.T) {
	t.Parallel()
	d := &vpcDiscoverer{new: func(_ string) vpcClient {
		return &fakeVPCClient{pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-052c72972a11f8677", "10.0.0.0/16", map[string]string{"Name": "io-foo-prod-vpc"})}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "vpc-052c72972a11f8677", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_vpc" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "vpc-052c72972a11f8677" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-prod-vpc" {
		t.Errorf("NameHint=%q, want io-foo-prod-vpc", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.NativeIDs["cidr_block"] != "10.0.0.0/16" {
		t.Errorf("NativeIDs[cidr_block]=%q", got.Identity.NativeIDs["cidr_block"])
	}
	// DiscoverByID does not fetch tags — Identity.Tags is nil per the
	// makeImportedResource contract.
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestVPCDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &vpcDiscoverer{new: func(_ string) vpcClient {
		return &fakeVPCClient{pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-052c72972a11f8677", "10.0.0.0/16", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:vpc/vpc-052c72972a11f8677", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "vpc-052c72972a11f8677" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestVPCDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	// Empty fake → DescribeVpcs returns empty Vpcs slice → ErrNotFound.
	d := &vpcDiscoverer{new: func(_ string) vpcClient { return &fakeVPCClient{} }}
	_, err := d.DiscoverByID(context.Background(), "vpc-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestVPCDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	// Real AWS responses for an unknown VPC ID return a smithy error
	// containing "InvalidVpcID.NotFound" — match the substring path.
	d := &vpcDiscoverer{new: func(_ string) vpcClient {
		return &fakeVPCClient{err: errors.New("api error InvalidVpcID.NotFound: The vpc ID 'vpc-deadbeef00000000' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "vpc-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestVPCDiscover_MultiRegionTriggersOneSDKCallPerRegion is the
// pattern-pin for the per-service Discover's `for _, region := range
// args.Regions` loop — see the SQS canonical version for the contract.
func TestVPCDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeVPCClient{
		"us-east-1": {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-east00000000001", "10.0.0.0/16", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-west00000000001", "10.1.0.0/16", nil)}},
		}},
	}
	var seenRegions []string
	d := &vpcDiscoverer{new: func(region string) vpcClient {
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
	if len(fakes["us-east-1"].calls) == 0 {
		t.Error("us-east-1 fake never received DescribeVpcs; per-region loop dropped the first region")
	}
	if len(fakes["eu-west-1"].calls) == 0 {
		t.Error("eu-west-1 fake never received DescribeVpcs; per-region loop dropped the second region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
	gotIDs := map[string]bool{}
	for _, ir := range got {
		gotIDs[ir.Identity.ImportID] = true
	}
	if !gotIDs["vpc-east00000000001"] || !gotIDs["vpc-west00000000001"] {
		t.Errorf("manifest IDs = %v, want both east + west VPCs", gotIDs)
	}
}

// TestVPCDiscover_TagSelectorAppliedAsFilter is the pattern-pin for the
// `if !MatchesAll(...) { continue }` in-loop filter — see SQS canonical
// version for the contract.
func TestVPCDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCClient{
		pages: []ec2.DescribeVpcsOutput{{Vpcs: []ec2types.Vpc{
			vpcWithTags("vpc-prod000000000001", "10.0.0.0/16", map[string]string{"Name": "io-foo-prod", "env": "prod"}),
			vpcWithTags("vpc-stag000000000002", "10.1.0.0/16", map[string]string{"Name": "io-foo-staging", "env": "staging"}),
		}}},
	}
	d := &vpcDiscoverer{new: func(_ string) vpcClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod VPC should pass)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-prod" {
		t.Errorf("NameHint=%q, want io-foo-prod", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

// TestVPCDiscover_EmitsServiceStartFinish_PerRegion pins the per-region
// service-scope progress contract — see the SQS canonical version.
func TestVPCDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeVPCClient{
		"us-east-1": {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-east00000000001", "10.0.0.0/16", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-west00000000001", "10.1.0.0/16", nil)}},
		}},
	}
	d := &vpcDiscoverer{new: func(region string) vpcClient { return fakes[region] }}
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
			if e.Service != "vpc" {
				t.Errorf("event %d: service=%q, want vpc", i, e.Service)
			}
		}
	}
	if got["us-east-1"].start == 0 || got["us-east-1"].finish == 0 {
		t.Errorf("us-east-1: missing service_start or service_finish: %+v", got["us-east-1"])
	}
	if got["eu-west-1"].start == 0 || got["eu-west-1"].finish == 0 {
		t.Errorf("eu-west-1: missing service_start or service_finish: %+v", got["eu-west-1"])
	}
	if got["us-east-1"].start >= got["us-east-1"].finish {
		t.Errorf("us-east-1: start at index %d >= finish at index %d", got["us-east-1"].start, got["us-east-1"].finish)
	}
}

// TestVPCDiscover_EmitsItemFound_PerVPC pins one item_found per emitted
// resource — see SQS canonical version for the contract.
func TestVPCDiscover_EmitsItemFound_PerVPC(t *testing.T) {
	t.Parallel()
	ids := []string{"vpc-aaa00000000000001", "vpc-bbb00000000000002", "vpc-ccc00000000000003"}
	vpcs := make([]ec2types.Vpc, 0, len(ids))
	for _, id := range ids {
		vpcs = append(vpcs, vpcWithTags(id, "10.0.0.0/16", nil))
	}
	fake := &fakeVPCClient{pages: []ec2.DescribeVpcsOutput{{Vpcs: vpcs}}}
	d := &vpcDiscoverer{new: func(_ string) vpcClient { return fake }}
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
		t.Errorf("item_found count = %d, want %d (one per emitted resource)", len(items), len(got))
	}
	wantIDs := map[string]bool{ids[0]: true, ids[1]: true, ids[2]: true}
	for i, it := range items {
		if it.Service != "vpc" {
			t.Errorf("item %d: service=%q, want vpc", i, it.Service)
		}
		if it.Region != "us-east-1" {
			t.Errorf("item %d: region=%q, want us-east-1", i, it.Region)
		}
		if it.TFType != "aws_vpc" {
			t.Errorf("item %d: tf_type=%q, want aws_vpc", i, it.TFType)
		}
		if !wantIDs[it.ImportID] {
			t.Errorf("item %d: import_id=%q not in expected IDs", i, it.ImportID)
		}
	}
	for _, e := range rec.snapshot() {
		if e.Kind == "service_finish" && e.Count != len(got) {
			t.Errorf("service_finish.count=%d, want %d", e.Count, len(got))
		}
	}
}

func TestVPCDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &vpcDiscoverer{new: func(_ string) vpcClient { return &fakeVPCClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket", // wrong service
		"arn:aws:lambda:us-east-1:123:function:hello", // wrong service
		"arn:aws:ec2:us-east-1:123:subnet/subnet-abc", // ec2 but not vpc
		"subnet-1234",  // wrong prefix
		"vpc 12345678", // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
