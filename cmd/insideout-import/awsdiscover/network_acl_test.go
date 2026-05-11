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
						// Project-tagged default NACL — must be filtered
						// out (#357) because aws_network_acl cannot model
						// it. See discoverer doc.
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
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (the project-tagged default NACL must be filtered)", len(got))
	}
	ir := got[0]
	if ir.Identity.Type != "aws_network_acl" {
		t.Errorf("Type=%q, want aws_network_acl", ir.Identity.Type)
	}
	if ir.Identity.ImportID != "acl-09fd2b515b8886254" {
		t.Errorf("ImportID=%q, want acl-09fd2b515b8886254", ir.Identity.ImportID)
	}
	if ir.Identity.NameHint != "io-foo-public-nacl" {
		t.Errorf("NameHint=%q, want io-foo-public-nacl (Name tag)", ir.Identity.NameHint)
	}
	if ir.Identity.NativeIDs["network_acl_id"] != "acl-09fd2b515b8886254" {
		t.Errorf("NativeIDs[network_acl_id]=%q", ir.Identity.NativeIDs["network_acl_id"])
	}
	if ir.Identity.NativeIDs["vpc_id"] != "vpc-052c72972a11f8677" {
		t.Errorf("NativeIDs[vpc_id]=%q", ir.Identity.NativeIDs["vpc_id"])
	}
	if ir.Identity.NativeIDs["is_default"] != "false" {
		t.Errorf("is_default=%q, want \"false\"", ir.Identity.NativeIDs["is_default"])
	}
	if ir.Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", ir.Identity.Tags["Project"])
	}
}

// TestNetworkACLDiscover_SkipsDefaultNACL (#357) covers the
// project-tagged-default-NACL case in isolation: a default NACL whose
// Project tag matches the stack must still be filtered out, otherwise
// Stage 2c1 dies with "Configuration for import target does not exist"
// because terraform plan -generate-config-out can't emit a body for
// aws_network_acl on a default NACL (provider models it via
// aws_default_network_acl).
func TestNetworkACLDiscover_SkipsDefaultNACL(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{
			pages: []ec2.DescribeNetworkAclsOutput{{
				NetworkAcls: []ec2types.NetworkAcl{
					// Only the default NACL is present. It carries the
					// project tag (InsideOut VPC preset propagates it via
					// merge(module.x.tags, var.tags)), so the server-side
					// tag:Project filter would let it through — we rely on
					// the client-side IsDefault check.
					networkACLWithTags("acl-default0000001", "vpc-1", true, map[string]string{"Project": "io-foo"}),
				},
			}},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d, want 0 (default NACL must be skipped)", len(got))
	}
}

// TestNetworkACLDiscover_DefaultNACLSkippedAmongMixedSet pins the
// ordering invariant: when both default and non-default NACLs come back
// from the describe call, only the non-defaults survive — regardless of
// the order the API returns them in.
func TestNetworkACLDiscover_DefaultNACLSkippedAmongMixedSet(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{
			pages: []ec2.DescribeNetworkAclsOutput{{
				NetworkAcls: []ec2types.NetworkAcl{
					networkACLWithTags("acl-default0000001", "vpc-1", true, map[string]string{"Project": "io-foo"}),
					networkACLWithTags("acl-nondefault0001", "vpc-1", false, map[string]string{"Project": "io-foo"}),
					networkACLWithTags("acl-default0000002", "vpc-1", true, map[string]string{"Project": "io-foo"}),
					networkACLWithTags("acl-nondefault0002", "vpc-1", false, map[string]string{"Project": "io-foo"}),
				},
			}},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (only 2 non-default NACLs survive)", len(got))
	}
	// Pin which IDs survive — a mutation that inverted the filter (dropped
	// non-defaults and kept defaults) would still produce len==2 with
	// is_default="false" if it happened to leave a coincidental pair.
	// Asserting the actual IDs catches that class of regression.
	gotIDs := map[string]bool{}
	for _, ir := range got {
		gotIDs[ir.Identity.ImportID] = true
		if ir.Identity.NativeIDs["is_default"] != "false" {
			t.Errorf("ImportID=%q is_default=%q; expected only is_default=false NACLs", ir.Identity.ImportID, ir.Identity.NativeIDs["is_default"])
		}
	}
	if !gotIDs["acl-nondefault0001"] || !gotIDs["acl-nondefault0002"] {
		t.Errorf("expected both non-default NACLs to survive; got %v", gotIDs)
	}
	if gotIDs["acl-default0000001"] || gotIDs["acl-default0000002"] {
		t.Errorf("expected NO default NACLs to survive; got %v", gotIDs)
	}
}

// TestNetworkACLDiscoverByID_AcceptsDefaultNACL (#357) pins that
// DiscoverByID — the dep-chase per-ID lookup path — does NOT apply
// the IsDefault filter that Discover does. The Discover-side filter
// is about avoiding orphan import blocks in batch flow; ByID is
// called for explicit-ID lookups where the caller has already
// decided to fetch this specific NACL. A future refactor that
// copy-pasted the `if aws.ToBool(nacl.IsDefault) { continue }`
// guard into DiscoverByID would break dep-chase for any default NACL
// that needs to be resolved as a dependency (e.g. attribute reference
// from a sibling resource). This test pins the contract: ByID
// surfaces defaults; Discover does not.
func TestNetworkACLDiscoverByID_AcceptsDefaultNACL(t *testing.T) {
	t.Parallel()
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient {
		return &fakeNetworkACLClient{pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{
				networkACLWithTags("acl-default0000001", "vpc-1", true, nil),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "acl-default0000001", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "acl-default0000001" {
		t.Errorf("ImportID=%q, want acl-default0000001 (ByID must accept defaults)", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["is_default"] != "true" {
		t.Errorf("is_default=%q, want \"true\"", got.Identity.NativeIDs["is_default"])
	}
}

func TestNetworkACLDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkACLClient{
		pages: []ec2.DescribeNetworkAclsOutput{
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-aaa00000000000001", "vpc-1", false, nil)}, NextToken: aws.String("tok1")},
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-bbb00000000000002", "vpc-1", false, nil)}, NextToken: aws.String("tok2")},
			{NetworkAcls: []ec2types.NetworkAcl{networkACLWithTags("acl-ccc00000000000003", "vpc-1", false, nil)}}, // terminal
		},
	}
	d := &networkACLDiscoverer{new: func(_ string) networkACLClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.calls) < 3 {
		t.Fatalf("DescribeNetworkAcls calls=%d, want >=3", len(fake.calls))
	}
	if aws.ToString(fake.calls[1].NextToken) != "tok1" {
		t.Errorf("call[1].NextToken=%q, want tok1", aws.ToString(fake.calls[1].NextToken))
	}
	if aws.ToString(fake.calls[2].NextToken) != "tok2" {
		t.Errorf("call[2].NextToken=%q, want tok2", aws.ToString(fake.calls[2].NextToken))
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
		return &fakeNetworkACLClient{err: ec2APIError("InvalidNetworkAclID.NotFound", "The network ACL 'acl-deadbeef' does not exist")}
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
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seenRegions)
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
