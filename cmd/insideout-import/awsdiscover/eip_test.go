package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errEIPSeed is the package-level sentinel for eip error propagation —
// see vpc_test.go for the contract.
var errEIPSeed = errors.New("AccessDenied")

type fakeEIPClient struct {
	resp  *ec2.DescribeAddressesOutput
	calls []ec2.DescribeAddressesInput
	err   error
}

func (f *fakeEIPClient) DescribeAddresses(_ context.Context, in *ec2.DescribeAddressesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	if f.resp == nil {
		return &ec2.DescribeAddressesOutput{}, nil
	}
	return f.resp, nil
}

func eipWithTags(allocID, publicIP, associationID string, domain ec2types.DomainType, tags map[string]string) ec2types.Address {
	a := ec2types.Address{
		AllocationId: aws.String(allocID),
		Domain:       domain,
	}
	if publicIP != "" {
		a.PublicIp = aws.String(publicIP)
	}
	if associationID != "" {
		a.AssociationId = aws.String(associationID)
	}
	for k, v := range tags {
		a.Tags = append(a.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return a
}

func TestEIPDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient {
		return &fakeEIPClient{
			resp: &ec2.DescribeAddressesOutput{
				Addresses: []ec2types.Address{
					eipWithTags("eipalloc-0cb7bedeabe1a2035", "203.0.113.10", "eipassoc-aaa", ec2types.DomainTypeVpc, map[string]string{"Name": "io-foo-eip", "Project": "io-foo"}),
					eipWithTags("eipalloc-0a1b2c3d4e5f60718", "203.0.113.11", "", ec2types.DomainTypeVpc, map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_eip" {
			t.Errorf("Type=%q, want aws_eip", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["allocation_id"] == "" {
			t.Error("NativeIDs[allocation_id] empty")
		}
		if ir.Identity.NativeIDs["domain"] != "vpc" {
			t.Errorf("NativeIDs[domain]=%q, want vpc", ir.Identity.NativeIDs["domain"])
		}
	}
	// Sorted by allocation ID.
	if got[0].Identity.ImportID != "eipalloc-0a1b2c3d4e5f60718" {
		t.Errorf("first ImportID=%q, want eipalloc-0a1b2c3d4e5f60718 (sorted)", got[0].Identity.ImportID)
	}
	// The second EIP (sorted second) carries Name and association ID.
	if got[1].Identity.NameHint != "io-foo-eip" {
		t.Errorf("second NameHint=%q, want io-foo-eip (Name tag)", got[1].Identity.NameHint)
	}
	if got[1].Identity.NativeIDs["public_ip"] != "203.0.113.10" {
		t.Errorf("NativeIDs[public_ip]=%q, want 203.0.113.10", got[1].Identity.NativeIDs["public_ip"])
	}
	if got[1].Identity.NativeIDs["association_id"] != "eipassoc-aaa" {
		t.Errorf("NativeIDs[association_id]=%q, want eipassoc-aaa", got[1].Identity.NativeIDs["association_id"])
	}
	// First EIP: no AssociationId, so association_id key absent.
	if _, ok := got[0].Identity.NativeIDs["association_id"]; ok {
		t.Errorf("first NativeIDs[association_id] should be unset for unassociated EIP")
	}
	// Fallback NameHint is the allocation ID for an EIP without a Name tag.
	if got[0].Identity.NameHint != "eipalloc-0a1b2c3d4e5f60718" {
		t.Errorf("first NameHint=%q, want fallback to allocation ID", got[0].Identity.NameHint)
	}
	// Tags propagate.
	if got[1].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[1].Identity.Tags["Project"])
	}
}

// TestEIPDiscover_HandlesEmptyResponse pins that DescribeAddresses
// (which does NOT paginate) is called once and an empty response is a
// well-formed no-op — this is the EIP analogue of the Paginates test
// for VPC/IGW/NAT.
func TestEIPDiscover_HandlesEmptyResponse(t *testing.T) {
	t.Parallel()
	fake := &fakeEIPClient{resp: &ec2.DescribeAddressesOutput{}}
	d := &eipDiscoverer{new: func(_ string) eipClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0 (empty response)", len(got))
	}
	// Exactly one DescribeAddresses call — no pagination loop.
	if len(fake.calls) != 1 {
		t.Errorf("DescribeAddresses calls=%d, want exactly 1 (no pagination)", len(fake.calls))
	}
}

func TestEIPDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeEIPClient{}
	d := &eipDiscoverer{new: func(_ string) eipClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeAddresses call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeAddressesInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestEIPDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeEIPClient{}
	d := &eipDiscoverer{new: func(_ string) eipClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeAddresses call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestEIPDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient {
		return &fakeEIPClient{err: errEIPSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errEIPSeed) {
		t.Errorf("err=%v, want errors.Is(err, errEIPSeed)", err)
	}
}

func TestEIPDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient {
		return &fakeEIPClient{resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{eipWithTags("eipalloc-0cb7bedeabe1a2035", "203.0.113.10", "eipassoc-aaa", ec2types.DomainTypeVpc, map[string]string{"Name": "io-foo-eip"})},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "eipalloc-0cb7bedeabe1a2035", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_eip" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "eipalloc-0cb7bedeabe1a2035" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-eip" {
		t.Errorf("NameHint=%q, want io-foo-eip", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["allocation_id"] != "eipalloc-0cb7bedeabe1a2035" {
		t.Errorf("NativeIDs[allocation_id]=%q", got.Identity.NativeIDs["allocation_id"])
	}
	if got.Identity.NativeIDs["public_ip"] != "203.0.113.10" {
		t.Errorf("NativeIDs[public_ip]=%q", got.Identity.NativeIDs["public_ip"])
	}
	if got.Identity.NativeIDs["association_id"] != "eipassoc-aaa" {
		t.Errorf("NativeIDs[association_id]=%q", got.Identity.NativeIDs["association_id"])
	}
	if got.Identity.NativeIDs["domain"] != "vpc" {
		t.Errorf("NativeIDs[domain]=%q, want vpc", got.Identity.NativeIDs["domain"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestEIPDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient {
		return &fakeEIPClient{resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{eipWithTags("eipalloc-0cb7bedeabe1a2035", "", "", ec2types.DomainTypeVpc, nil)},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:elastic-ip/eipalloc-0cb7bedeabe1a2035", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "eipalloc-0cb7bedeabe1a2035" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestEIPDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient { return &fakeEIPClient{} }}
	_, err := d.DiscoverByID(context.Background(), "eipalloc-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestEIPDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient {
		return &fakeEIPClient{err: ec2APIError("InvalidAllocationID.NotFound", "The allocation ID 'eipalloc-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "eipalloc-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestEIPDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeEIPClient{
		"us-east-1": {resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{eipWithTags("eipalloc-east0000000000001", "203.0.113.10", "", ec2types.DomainTypeVpc, nil)},
		}},
		"eu-west-1": {resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{eipWithTags("eipalloc-west0000000000001", "203.0.113.11", "", ec2types.DomainTypeVpc, nil)},
		}},
	}
	var seenRegions []string
	d := &eipDiscoverer{new: func(region string) eipClient {
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
	if len(fakes["us-east-1"].calls) != 1 || len(fakes["eu-west-1"].calls) != 1 {
		t.Errorf("expected exactly one DescribeAddresses call per region; us-east-1=%d eu-west-1=%d",
			len(fakes["us-east-1"].calls), len(fakes["eu-west-1"].calls))
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestEIPDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeEIPClient{
		resp: &ec2.DescribeAddressesOutput{Addresses: []ec2types.Address{
			eipWithTags("eipalloc-prod000000000001", "203.0.113.10", "", ec2types.DomainTypeVpc, map[string]string{"Name": "prod-eip", "env": "prod"}),
			eipWithTags("eipalloc-stag000000000002", "203.0.113.11", "", ec2types.DomainTypeVpc, map[string]string{"Name": "staging-eip", "env": "staging"}),
		}},
	}
	d := &eipDiscoverer{new: func(_ string) eipClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod EIP should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-eip" {
		t.Errorf("NameHint=%q, want prod-eip", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestEIPDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeEIPClient{
		"us-east-1": {resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{eipWithTags("eipalloc-east0000000000001", "203.0.113.10", "", ec2types.DomainTypeVpc, nil)},
		}},
		"eu-west-1": {resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{eipWithTags("eipalloc-west0000000000001", "203.0.113.11", "", ec2types.DomainTypeVpc, nil)},
		}},
	}
	d := &eipDiscoverer{new: func(region string) eipClient { return fakes[region] }}
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
			if e.Service != "eip" {
				t.Errorf("event %d: service=%q, want eip", i, e.Service)
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

func TestEIPDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"eipalloc-aaa00000000000001", "eipalloc-bbb00000000000002", "eipalloc-ccc00000000000003"}
	es := make([]ec2types.Address, 0, len(ids))
	for _, id := range ids {
		es = append(es, eipWithTags(id, "203.0.113.10", "", ec2types.DomainTypeVpc, nil))
	}
	fake := &fakeEIPClient{resp: &ec2.DescribeAddressesOutput{Addresses: es}}
	d := &eipDiscoverer{new: func(_ string) eipClient { return fake }}
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
		if it.Service != "eip" {
			t.Errorf("item %d: service=%q, want eip", i, it.Service)
		}
		if it.TFType != "aws_eip" {
			t.Errorf("item %d: tf_type=%q, want aws_eip", i, it.TFType)
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

// TestEIPDiscover_SkipsClassicEIPsWithoutAllocationID pins that EC2-Classic
// EIPs (no AllocationId, only PublicIp) cannot be imported by aws_eip
// and are therefore dropped from the manifest. This is a defensive
// check for legacy accounts; no modern (non-Classic) EIP should land in
// this path.
func TestEIPDiscover_SkipsClassicEIPsWithoutAllocationID(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient {
		return &fakeEIPClient{resp: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{
				{PublicIp: aws.String("198.51.100.1"), Domain: ec2types.DomainTypeStandard}, // EC2-Classic — no AllocationId
				eipWithTags("eipalloc-0cb7bedeabe1a2035", "203.0.113.10", "", ec2types.DomainTypeVpc, nil),
			},
		}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (Classic EIP without AllocationId should be skipped)", len(got))
	}
	if got[0].Identity.ImportID != "eipalloc-0cb7bedeabe1a2035" {
		t.Errorf("ImportID=%q, want eipalloc-0cb7bedeabe1a2035", got[0].Identity.ImportID)
	}
}

func TestEIPDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &eipDiscoverer{new: func(_ string) eipClient { return &fakeEIPClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not elastic-ip
		"vpc-1234",                              // wrong prefix
		"eipalloc 1234",                         // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
