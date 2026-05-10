package vpcquery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
)

// fakeRunner records the arguments it was called with and returns a
// caller-provided event stream (or sentinel error). It is the
// QueryRunner-shaped equivalent of fakeVPCClient in the parent package.
type fakeRunner struct {
	stdoutByRegion map[string][]byte
	calls          []fakeRunnerCall
	err            error
}

type fakeRunnerCall struct {
	WorkDir, Region, Project string
}

func (f *fakeRunner) Run(_ context.Context, workDir, region, project string) ([]byte, error) {
	f.calls = append(f.calls, fakeRunnerCall{WorkDir: workDir, Region: region, Project: project})
	if f.err != nil {
		return nil, f.err
	}
	if b, ok := f.stdoutByRegion[region]; ok {
		return b, nil
	}
	return nil, nil
}

// fakeVPCClient is a near-copy of the parent package's fakeVPCClient,
// duplicated here because the parent's is unexported. Returns canned
// DescribeVpcs pages indexed by call number, or `err` if non-nil.
type fakeVPCClient struct {
	pages []ec2.DescribeVpcsOutput
	calls []ec2.DescribeVpcsInput
	err   error
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

// listResourceFoundLine builds one terraform-query JSON event line with
// the given VPC ID. The shape is hand-rolled to match the live
// terraform 1.14.9 + hashicorp/aws 6.44.0 output observed during the
// CUST3 smoke (see docs/terraform-query-prototype.md).
func listResourceFoundLine(t *testing.T, vpcID, accountID, region string) []byte {
	t.Helper()
	envelope := map[string]any{
		"@level":     "info",
		"@message":   "list.aws_vpc.all: Result found",
		"@module":    "terraform.ui",
		"@timestamp": "2026-05-09T15:00:26.155258-07:00",
		"type":       "list_resource_found",
		"list_resource_found": map[string]any{
			"address":      "list.aws_vpc.all",
			"display_name": vpcID + " display",
			"identity": map[string]any{
				"account_id": accountID,
				"id":         vpcID,
				"region":     region,
			},
			"identity_version": 0,
			"resource_type":    "aws_vpc",
		},
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(b, '\n')
}

// frameStream concatenates one or more event lines and prepends a
// version + list_start + appends a list_complete envelope, mimicking
// the real stream framing.
func frameStream(t *testing.T, lines ...[]byte) []byte {
	t.Helper()
	out := []byte(`{"@level":"info","@message":"Terraform 1.14.9","type":"version"}` + "\n")
	out = append(out, []byte(`{"@level":"info","type":"list_start","list_start":{"address":"list.aws_vpc.all"}}`+"\n")...)
	for _, l := range lines {
		out = append(out, l...)
	}
	out = append(out, []byte(`{"@level":"info","type":"list_complete","list_complete":{"address":"list.aws_vpc.all","total":1}}`+"\n")...)
	return out
}

// newTestDiscoverer wires a Discoverer with the supplied fake runner +
// fake VPC client factory. Mirrors how vpc_test.go constructs a
// vpcDiscoverer with a fake client closure.
func newTestDiscoverer(runner QueryRunner, perRegion map[string]*fakeVPCClient) *Discoverer {
	return &Discoverer{
		runner: runner,
		new: func(region string) VpcClient {
			if c, ok := perRegion[region]; ok {
				return c
			}
			return &fakeVPCClient{}
		},
	}
}

func TestDiscover_HappyPath(t *testing.T) {
	t.Parallel()

	region := "us-east-1"
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{
		region: frameStream(t,
			listResourceFoundLine(t, "vpc-052c72972a11f8677", "031780745048", region),
			listResourceFoundLine(t, "vpc-0a1b2c3d4e5f60718", "031780745048", region),
		),
	}}
	clients := map[string]*fakeVPCClient{
		region: {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{
				vpcWithTags("vpc-052c72972a11f8677", "10.0.0.0/16", map[string]string{"Name": "io-foo-prod-vpc", "Project": "io-foo"}),
				vpcWithTags("vpc-0a1b2c3d4e5f60718", "10.1.0.0/16", map[string]string{"Project": "io-foo"}),
			}},
		}},
	}

	d := newTestDiscoverer(runner, clients)
	got, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{region},
		AccountID: "031780745048",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}

	// Same shape contract as parent vpc_test.go::TestVPCDiscover_HappyPath.
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
	}
	// Output is sorted by VPC ID.
	if got[0].Identity.ImportID != "vpc-052c72972a11f8677" {
		t.Errorf("first ImportID=%q, want vpc-052c… (sorted)", got[0].Identity.ImportID)
	}
	// Name tag wins over the bare VPC ID for NameHint.
	if got[0].Identity.NameHint != "io-foo-prod-vpc" {
		t.Errorf("first NameHint=%q, want io-foo-prod-vpc", got[0].Identity.NameHint)
	}
	// Fallback for the un-Named VPC.
	if got[1].Identity.NameHint != "vpc-0a1b2c3d4e5f60718" {
		t.Errorf("second NameHint=%q, want fallback to VPC ID", got[1].Identity.NameHint)
	}
	// Tags propagate.
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestDiscover_PassesProjectFilterAsVar(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{"us-east-1": frameStream(t)}}
	clients := map[string]*fakeVPCClient{}
	d := newTestDiscoverer(runner, clients)
	if _, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls=%d, want 1", len(runner.calls))
	}
	if runner.calls[0].Project != "io-foo" {
		t.Errorf("runner.calls[0].Project=%q, want io-foo (project_filter must thread through to -var)", runner.calls[0].Project)
	}
	if runner.calls[0].Region != "us-east-1" {
		t.Errorf("runner.calls[0].Region=%q, want us-east-1", runner.calls[0].Region)
	}
}

func TestDiscover_EmptyProjectPassesEmptyFilter(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{"us-east-1": frameStream(t)}}
	d := newTestDiscoverer(runner, map[string]*fakeVPCClient{})
	if _, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project: "", Regions: []string{"us-east-1"}, AccountID: "123",
	}); err != nil {
		t.Fatal(err)
	}
	if runner.calls[0].Project != "" {
		t.Errorf("runner.calls[0].Project=%q, want empty (admin path: no filter)", runner.calls[0].Project)
	}
}

func TestDiscover_PropagatesRunnerError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("terraform init: provider auth failed")
	runner := &fakeRunner{err: sentinel}
	d := newTestDiscoverer(runner, map[string]*fakeVPCClient{})
	_, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err=%v, want errors.Is(err, sentinel) — wrapper swallowed runner error", err)
	}
}

func TestDiscover_PropagatesDescribeVpcsError(t *testing.T) {
	t.Parallel()
	region := "us-east-1"
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{
		region: frameStream(t, listResourceFoundLine(t, "vpc-052c72972a11f8677", "123", region)),
	}}
	sentinel := errors.New("AccessDenied")
	clients := map[string]*fakeVPCClient{region: {err: sentinel}}
	d := newTestDiscoverer(runner, clients)
	_, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project: "io-foo", Regions: []string{region}, AccountID: "123",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err=%v, want errors.Is(err, sentinel) — DescribeVpcs error swallowed", err)
	}
}

func TestDiscover_MultiRegionTriggersOneRunnerCallPerRegion(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{
		"us-east-1": frameStream(t, listResourceFoundLine(t, "vpc-east00000000001", "123", "us-east-1")),
		"eu-west-1": frameStream(t, listResourceFoundLine(t, "vpc-west00000000001", "123", "eu-west-1")),
	}}
	clients := map[string]*fakeVPCClient{
		"us-east-1": {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-east00000000001", "10.0.0.0/16", map[string]string{"Project": "io-foo"})}},
		}},
		"eu-west-1": {pages: []ec2.DescribeVpcsOutput{
			{Vpcs: []ec2types.Vpc{vpcWithTags("vpc-west00000000001", "10.1.0.0/16", map[string]string{"Project": "io-foo"})}},
		}},
	}
	d := newTestDiscoverer(runner, clients)
	got, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls=%d, want 2", len(runner.calls))
	}
	if runner.calls[0].Region != "us-east-1" || runner.calls[1].Region != "eu-west-1" {
		t.Errorf("runner regions=[%s,%s], want [us-east-1,eu-west-1]", runner.calls[0].Region, runner.calls[1].Region)
	}
	// Both regions saw a DescribeVpcs (the tag-fetch) round-trip.
	if len(clients["us-east-1"].calls) == 0 {
		t.Error("us-east-1 fakeVPCClient never received DescribeVpcs (tag fetch dropped)")
	}
	if len(clients["eu-west-1"].calls) == 0 {
		t.Error("eu-west-1 fakeVPCClient never received DescribeVpcs (tag fetch dropped)")
	}
}

func TestDiscover_FiltersByTagSelectors(t *testing.T) {
	t.Parallel()
	region := "us-east-1"
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{
		region: frameStream(t,
			listResourceFoundLine(t, "vpc-aaa00000000000001", "123", region),
			listResourceFoundLine(t, "vpc-bbb00000000000002", "123", region),
		),
	}}
	clients := map[string]*fakeVPCClient{
		region: {pages: []ec2.DescribeVpcsOutput{{Vpcs: []ec2types.Vpc{
			vpcWithTags("vpc-aaa00000000000001", "10.0.0.0/16", map[string]string{"Project": "io-foo", "Env": "prod"}),
			vpcWithTags("vpc-bbb00000000000002", "10.1.0.0/16", map[string]string{"Project": "io-foo", "Env": "dev"}),
		}}}},
	}
	d := newTestDiscoverer(runner, clients)
	got, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project:      "io-foo",
		Regions:      []string{region},
		AccountID:    "123",
		TagSelectors: []awsdiscover.TagSelector{{Key: "Env", Value: "prod"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only prod VPC)", len(got))
	}
	if got[0].Identity.ImportID != "vpc-aaa00000000000001" {
		t.Errorf("ImportID=%q, want vpc-aaa…", got[0].Identity.ImportID)
	}
}

func TestDiscover_RaceBetweenQueryAndTagFetchSkipsMissing(t *testing.T) {
	t.Parallel()
	// Query reports two VPCs, but DescribeVpcs returns only one (the
	// other was deleted between the calls). The wrapper should skip
	// the missing one rather than building an Identity with empty
	// CIDR/tags.
	region := "us-east-1"
	runner := &fakeRunner{stdoutByRegion: map[string][]byte{
		region: frameStream(t,
			listResourceFoundLine(t, "vpc-aaa00000000000001", "123", region),
			listResourceFoundLine(t, "vpc-bbb00000000000002", "123", region),
		),
	}}
	clients := map[string]*fakeVPCClient{
		region: {pages: []ec2.DescribeVpcsOutput{{Vpcs: []ec2types.Vpc{
			vpcWithTags("vpc-aaa00000000000001", "10.0.0.0/16", nil),
			// vpc-bbb absent — simulating the deletion race.
		}}}},
	}
	d := newTestDiscoverer(runner, clients)
	got, err := d.Discover(context.Background(), awsdiscover.DiscoverArgs{
		Project: "io-foo", Regions: []string{region}, AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (race-deleted VPC dropped)", len(got))
	}
}

func TestParseQueryEvents_IgnoresNonListResourceFound(t *testing.T) {
	t.Parallel()
	// Stream contains version + list_start + list_complete + a real
	// list_resource_found. Only the last should produce a queryMatch.
	stream := frameStream(t, listResourceFoundLine(t, "vpc-052c72972a11f8677", "123", "us-east-1"))
	matches, err := parseQueryEvents(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("len=%d, want 1 (non-list_resource_found events must be ignored)", len(matches))
	}
	if matches[0].VPCID != "vpc-052c72972a11f8677" {
		t.Errorf("VPCID=%q", matches[0].VPCID)
	}
}

func TestParseQueryEvents_TolueratesMalformedLines(t *testing.T) {
	t.Parallel()
	// A trailing garbage line shouldn't kill the parse.
	good := listResourceFoundLine(t, "vpc-052c72972a11f8677", "123", "us-east-1")
	stream := append(good, []byte("not-json\n")...)
	matches, err := parseQueryEvents(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Errorf("len=%d, want 1 (garbage line should be skipped)", len(matches))
	}
}

func TestParseQueryEvents_FiltersForeignResourceTypes(t *testing.T) {
	t.Parallel()
	// A future config might list multiple types in the same query; we
	// must keep only aws_vpc events.
	other := []byte(`{"type":"list_resource_found","list_resource_found":{"resource_type":"aws_subnet","identity":{"id":"subnet-deadbeef"},"address":"list.aws_subnet.all"}}` + "\n")
	good := listResourceFoundLine(t, "vpc-052c72972a11f8677", "123", "us-east-1")
	stream := append(other, good...)
	matches, err := parseQueryEvents(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].VPCID != "vpc-052c72972a11f8677" {
		t.Errorf("matches=%v, want one aws_vpc match", matches)
	}
}

// TestEmbeddedHCLIsParseable is a smoke test: the embedded
// vpc.tfquery.hcl must contain a `list "aws_vpc"` block. Catches
// embed-directive misconfigurations that would only surface at
// `terraform init` time otherwise.
func TestEmbeddedHCLIsParseable(t *testing.T) {
	t.Parallel()
	if !strings.Contains(string(vpcQueryHCL), `list "aws_vpc"`) {
		t.Errorf("embedded vpc.tfquery.hcl missing `list \"aws_vpc\"` block; embed broken")
	}
	if !strings.Contains(string(vpcQueryHCL), "tag:Project") {
		t.Errorf("embedded vpc.tfquery.hcl missing tag:Project filter — server-side filter wiring broken")
	}
}
