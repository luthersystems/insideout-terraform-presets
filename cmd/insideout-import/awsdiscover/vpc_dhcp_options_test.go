package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// errVPCDHCPOptionsSeed is the package-level sentinel for vpc-dhcp-options
// error propagation — see vpc_test.go for the contract.
var errVPCDHCPOptionsSeed = errors.New("AccessDenied")

type fakeVPCDHCPOptionsClient struct {
	pages []ec2.DescribeDhcpOptionsOutput
	calls []ec2.DescribeDhcpOptionsInput
	err   error
}

func (f *fakeVPCDHCPOptionsClient) DescribeDhcpOptions(_ context.Context, in *ec2.DescribeDhcpOptionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeDhcpOptionsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &ec2.DescribeDhcpOptionsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func dhcpOptionsWithTags(id string, tags map[string]string) ec2types.DhcpOptions {
	dopt := ec2types.DhcpOptions{
		DhcpOptionsId: aws.String(id),
	}
	for k, v := range tags {
		dopt.Tags = append(dopt.Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return dopt
}

func TestVPCDHCPOptionsDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient {
		return &fakeVPCDHCPOptionsClient{
			pages: []ec2.DescribeDhcpOptionsOutput{
				{
					DhcpOptions: []ec2types.DhcpOptions{
						dhcpOptionsWithTags("dopt-05dd88c04d34bd2ee", map[string]string{"Name": "io-foo-dopt", "Project": "io-foo"}),
						dhcpOptionsWithTags("dopt-0a1b2c3d4e5f60718", map[string]string{"Project": "io-foo"}),
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
		if ir.Identity.Type != "aws_vpc_dhcp_options" {
			t.Errorf("Type=%q, want aws_vpc_dhcp_options", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["dhcp_options_id"] == "" {
			t.Error("NativeIDs[dhcp_options_id] empty")
		}
	}
	// Sorted by DHCP-options ID; dopt-05dd... sorts before dopt-0a1b...
	if got[0].Identity.ImportID != "dopt-05dd88c04d34bd2ee" {
		t.Errorf("first ImportID=%q, want dopt-05dd88c04d34bd2ee (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NameHint != "io-foo-dopt" {
		t.Errorf("first NameHint=%q, want io-foo-dopt", got[0].Identity.NameHint)
	}
	if got[1].Identity.NameHint != "dopt-0a1b2c3d4e5f60718" {
		t.Errorf("second NameHint=%q, want fallback to DHCP-options ID", got[1].Identity.NameHint)
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestVPCDHCPOptionsDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient {
		return &fakeVPCDHCPOptionsClient{
			pages: []ec2.DescribeDhcpOptionsOutput{
				{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-aaa00000000000001", nil)}, NextToken: aws.String("tok1")},
				{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-bbb00000000000002", nil)}, NextToken: aws.String("tok2")},
				{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-ccc00000000000003", nil)}}, // terminal
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

func TestVPCDHCPOptionsDiscover_PassesProjectTagFilterServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCDHCPOptionsClient{}
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeDhcpOptions call")
	}
	in := fake.calls[0]
	if len(in.Filters) == 0 {
		t.Fatal("expected at least one Filter on DescribeDhcpOptionsInput")
	}
	if got := aws.ToString(in.Filters[0].Name); got != "tag:Project" {
		t.Errorf("Filters[0].Name=%q, want tag:Project", got)
	}
	if len(in.Filters[0].Values) == 0 || in.Filters[0].Values[0] != "io-foo" {
		t.Errorf("Filters[0].Values=%v, want [io-foo]", in.Filters[0].Values)
	}
}

func TestVPCDHCPOptionsDiscover_EmptyProjectPassesNoFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCDHCPOptionsClient{}
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one DescribeDhcpOptions call")
	}
	if len(fake.calls[0].Filters) != 0 {
		t.Errorf("Filters=%v, want empty for empty project", fake.calls[0].Filters)
	}
}

func TestVPCDHCPOptionsDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient {
		return &fakeVPCDHCPOptionsClient{err: errVPCDHCPOptionsSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errVPCDHCPOptionsSeed) {
		t.Errorf("err=%v, want errors.Is(err, errVPCDHCPOptionsSeed)", err)
	}
}

func TestVPCDHCPOptionsDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient {
		return &fakeVPCDHCPOptionsClient{pages: []ec2.DescribeDhcpOptionsOutput{
			{DhcpOptions: []ec2types.DhcpOptions{
				dhcpOptionsWithTags("dopt-05dd88c04d34bd2ee", map[string]string{"Name": "io-foo-dopt"}),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), "dopt-05dd88c04d34bd2ee", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_vpc_dhcp_options" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "dopt-05dd88c04d34bd2ee" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo-dopt" {
		t.Errorf("NameHint=%q, want io-foo-dopt", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["dhcp_options_id"] != "dopt-05dd88c04d34bd2ee" {
		t.Errorf("NativeIDs[dhcp_options_id]=%q", got.Identity.NativeIDs["dhcp_options_id"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestVPCDHCPOptionsDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient {
		return &fakeVPCDHCPOptionsClient{pages: []ec2.DescribeDhcpOptionsOutput{
			{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-05dd88c04d34bd2ee", nil)}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:ec2:us-east-1:123:dhcp-options/dopt-05dd88c04d34bd2ee", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "dopt-05dd88c04d34bd2ee" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestVPCDHCPOptionsDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient { return &fakeVPCDHCPOptionsClient{} }}
	_, err := d.DiscoverByID(context.Background(), "dopt-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestVPCDHCPOptionsDiscoverByID_NotFound_FromAPIErrorCode(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient {
		return &fakeVPCDHCPOptionsClient{err: errors.New("api error InvalidDhcpOptionID.NotFound: The DHCP options set 'dopt-deadbeef' does not exist")}
	}}
	_, err := d.DiscoverByID(context.Background(), "dopt-deadbeef00000000", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestVPCDHCPOptionsDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeVPCDHCPOptionsClient{
		"us-east-1": {pages: []ec2.DescribeDhcpOptionsOutput{
			{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-east0000000000001", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeDhcpOptionsOutput{
			{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-west0000000000001", nil)}},
		}},
	}
	var seenRegions []string
	d := &vpcDHCPOptionsDiscoverer{new: func(region string) vpcDHCPOptionsClient {
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
		t.Error("expected one DescribeDhcpOptions call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestVPCDHCPOptionsDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCDHCPOptionsClient{
		pages: []ec2.DescribeDhcpOptionsOutput{{DhcpOptions: []ec2types.DhcpOptions{
			dhcpOptionsWithTags("dopt-prod000000000001", map[string]string{"Name": "prod-dopt", "env": "prod"}),
			dhcpOptionsWithTags("dopt-stag000000000002", map[string]string{"Name": "staging-dopt", "env": "staging"}),
		}}},
	}
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod DHCP options should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod-dopt" {
		t.Errorf("NameHint=%q, want prod-dopt", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestVPCDHCPOptionsDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeVPCDHCPOptionsClient{
		"us-east-1": {pages: []ec2.DescribeDhcpOptionsOutput{
			{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-east0000000000001", nil)}},
		}},
		"eu-west-1": {pages: []ec2.DescribeDhcpOptionsOutput{
			{DhcpOptions: []ec2types.DhcpOptions{dhcpOptionsWithTags("dopt-west0000000000001", nil)}},
		}},
	}
	d := &vpcDHCPOptionsDiscoverer{new: func(region string) vpcDHCPOptionsClient { return fakes[region] }}
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
			if e.Service != "vpc_dhcp_options" {
				t.Errorf("event %d: service=%q, want vpc_dhcp_options", i, e.Service)
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

func TestVPCDHCPOptionsDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"dopt-aaa00000000000001", "dopt-bbb00000000000002", "dopt-ccc00000000000003"}
	dopts := make([]ec2types.DhcpOptions, 0, len(ids))
	for _, id := range ids {
		dopts = append(dopts, dhcpOptionsWithTags(id, nil))
	}
	fake := &fakeVPCDHCPOptionsClient{pages: []ec2.DescribeDhcpOptionsOutput{{DhcpOptions: dopts}}}
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient { return fake }}
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
		if it.Service != "vpc_dhcp_options" {
			t.Errorf("item %d: service=%q, want vpc_dhcp_options", i, it.Service)
		}
		if it.TFType != "aws_vpc_dhcp_options" {
			t.Errorf("item %d: tf_type=%q, want aws_vpc_dhcp_options", i, it.TFType)
		}
	}
}

func TestVPCDHCPOptionsDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &vpcDHCPOptionsDiscoverer{new: func(_ string) vpcDHCPOptionsClient { return &fakeVPCDHCPOptionsClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket",
		"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", // ec2 but not dhcp-options
		"vpc-1234",                              // wrong prefix
		"dopt 1234",                             // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
