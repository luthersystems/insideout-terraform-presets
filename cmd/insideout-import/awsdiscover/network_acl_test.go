package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errNetworkACLSeed is the package-level sentinel for network-ACL error
// propagation — see vpc_test.go for the contract.
var errNetworkACLSeed = errors.New("AccessDenied")

type fakeNetworkACLClient struct {
	pages []ec2.DescribeNetworkAclsOutput
	calls []ec2.DescribeNetworkAclsInput
	err   error
}

func (f *fakeNetworkACLClient) DescribeNetworkAcls(_ context.Context, in *ec2.DescribeNetworkAclsInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkAclsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeNetworkAclsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func networkACLWithTags(id, vpcID string, isDefault bool, tags map[string]string) ec2types.NetworkAcl {
	nacl := ec2types.NetworkAcl{
		NetworkAclId: aws.String(id),
		VpcId:        aws.String(vpcID),
		IsDefault:    aws.Bool(isDefault),
	}
	for k, v := range tags {
		nacl.Tags = append(nacl.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return nacl
}

func TestNetworkACLDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{
			pages: []ec2.DescribeNetworkAclsOutput{
				{
					NetworkAcls: []ec2types.NetworkAcl{
						networkACLWithTags("acl-09fd2b515b8886254", "vpc-052c72972a11f8677", false, map[string]string{"Name": "io-foo-public-nacl", "Project": "io-foo"}),
						networkACLWithTags("acl-0a1b2c3d4e5f60718", "vpc-052c72972a11f8677", true, map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_network_acl" {
			t.Errorf("Type=%q, want aws_network_acl", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["network_acl_id"] == "" {
			t.Error("NativeIDs[network_acl_id] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["is_default"] == "" {
			t.Error("NativeIDs[is_default] empty (must be 'true' or 'false')")
		}
	}
	// Sorted by NACL ID; acl-0a1b... sorts before acl-09fd...
	if got[0].Identity.ImportID != "acl-09fd2b515b8886254" {
		t.Errorf("first ImportID=%q, want acl-09fd2b515b8886254 (sorted)", got[0].Identity.ImportID)
	}
	// Name tag wins over the bare NACL ID for NameHint.
	if got[0].Identity.NameHint != "io-foo-public-nacl" {
		t.Errorf("first NameHint=%q, want io-foo-public-nacl (Name tag)", got[0].Identity.NameHint)
	}
	// Fallback to NACL ID when no Name tag.
	if got[1].Identity.NameHint != "acl-0a1b2c3d4e5f60718" {
		t.Errorf("second NameHint=%q, want fallback to NACL ID", got[1].Identity.NameHint)
	}
	// is_default round-trips as a string.
	if got[0].Identity.NativeIDs["is_default"] != "false" {
		t.Errorf("first is_default=%q, want \"false\"", got[0].Identity.NativeIDs["is_default"])
	}
	if got[1].Identity.NativeIDs["is_default"] != "true" {
		t.Errorf("second is_default=%q, want \"true\"", got[1].Identity.NativeIDs["is_default"])
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestNetworkACLDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{
			pages: []ec2.DescribeNetworkAclsOutput{
				{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-aaa00000000000001", "vpc-1", false, nil)}, NextToken: aws.String("tok1")},
				{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-bbb00000000000002", "vpc-1", false, nil)}, NextToken: aws.String("tok2")},
				{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-ccc00000000000003", "vpc-1", false, nil)}}, // terminal
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

func TestNetworkACLDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLClient{}
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeNetworkAcls call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeNetworkAclsInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestNetworkACLDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLClient{}
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeNetworkAcls call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestNetworkACLDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{err: errNetworkACLSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errNetworkACLSeed) {
		t.Errorf("err=%v, want errors.Is(err, errNetworkACLSeed)", err)
	}
}

func TestNetworkACLDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{
				networkACLWithTags("acl-09fd2b515b8886254", "vpc-052c72972a11f8677", false, map[string]string{"Name": "io-foo-nacl"}),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "acl-09fd2b515b8886254", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_network_acl" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "acl-09fd2b515b8886254" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-nacl" {
		t.Errorf("NameHint=%q, want io-foo-nacl", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.NativeIDs["is_default"] != "false" {
		t.Errorf("NativeIDs[is_default]=%q, want \"false\"", got.Identity.NativeIDs["is_default"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestNetworkACLDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-09fd2b515b8886254", "vpc-1", false, nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:network-acl/acl-09fd2b515b8886254", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "acl-09fd2b515b8886254" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestNetworkACLDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return &fakeNetworkACLClient{} }}
	_, err := d.DiscoverByID(context.Background(), "acl-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestNetworkACLDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{err: errors.New("api error InvalidNetworkAclID.NotFound: The network ACL 'acl-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "acl-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestNetworkACLDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeNetworkACLClient{
		"us-east-1": {pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-east0000000000001", "vpc-1", false, nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-west0000000000001", "vpc-2", false, nil)}},
		}},
	}
	var seenRegions []string
	d := &networkACLDiscoverer{new: func(region string) networkACLClient {
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
		t.Error("expected one DescribeNetworkAcls call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestNetworkACLDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLClient{
		pages: []ec2.DescribeNetworkAclsOutput{{NetworkAcls: []ec2types.NetworkAcl{
			networkACLWithTags("acl-prod000000000001", "vpc-1", false, map[string]string{"Name": "prod-nacl", "env": "prod"}),
			networkACLWithTags("acl-stag000000000002", "vpc-1", false, map[string]string{"Name": "staging-nacl", "env": "staging"}),
		}}},
	}
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod NACL should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-nacl" {
		t.Errorf("NameHint=%q, want prod-nacl", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestNetworkACLDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeNetworkACLClient{
		"us-east-1": {pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-east0000000000001", "vpc-1", false, nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-west0000000000001", "vpc-2", false, nil)}},
		}},
	}
	d := &networkACLDiscoverer{new: func(region string) networkACLClient { return fakes[region] }}
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
			if e.Service != "network_acl" {
				t.Errorf("event %d: service=%q, want network_acl", i, e.Service)
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

func TestNetworkACLDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"acl-aaa00000000000001", "acl-bbb00000000000002", "acl-ccc00000000000003"}
	nacls := make([]ec2types.NetworkAcl, 0, len(ids))
	for _, id := range ids {
		nacls = append(nacls, networkACLWithTags(id, "vpc-1", false, nil))
	}
	fake := &fakeNetworkACLClient{pages: []ec2.DescribeNetworkAclsOutput{{NetworkAcls: nacls}}}
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return fake }}
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
		if it.Service != "network_acl" {
			t.Errorf("item %d: service=%q, want network_acl", i, it.Service)
		}
		if it.TFType != "aws_network_acl" {
			t.Errorf("item %d: tf_type=%q, want aws_network_acl", i, it.TFType)
		}
	}
}

func TestNetworkACLDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return &fakeNetworkACLClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not network-acl
		"vpc-1234",                              // wrong prefix
		"acl 1234",                              // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
