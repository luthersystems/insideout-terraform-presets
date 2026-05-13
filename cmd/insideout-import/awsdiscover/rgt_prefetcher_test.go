package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgttypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// fakeRGTClient is a hand-rolled rgtClient. Pages are returned in order;
// `err` short-circuits the call (single error per fake; covers the
// per-region failure scenarios).
type fakeRGTClient struct {
	pages [][]rgttypes.ResourceTagMapping
	err   error
	calls atomic.Int64
}

func (f *fakeRGTClient) GetResources(_ context.Context,
	in *resourcegroupstaggingapi.GetResourcesInput,
	_ ...func(*resourcegroupstaggingapi.Options),
) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	idx := f.calls.Add(1) - 1
	if int(idx) >= len(f.pages) {
		return &resourcegroupstaggingapi.GetResourcesOutput{}, nil
	}
	out := &resourcegroupstaggingapi.GetResourcesOutput{
		ResourceTagMappingList: f.pages[idx],
	}
	if int(idx)+1 < len(f.pages) {
		out.PaginationToken = aws.String(fmt.Sprintf("page-%d", idx+1))
	}
	return out, nil
}

// mapping builds a ResourceTagMapping for tests with the given ARN and
// project tag. Additional tags can be supplied via extra.
func mapping(arn, project string, extra ...rgttypes.Tag) rgttypes.ResourceTagMapping {
	tags := []rgttypes.Tag{{Key: aws.String("Project"), Value: aws.String(project)}}
	tags = append(tags, extra...)
	return rgttypes.ResourceTagMapping{ResourceARN: aws.String(arn), Tags: tags}
}

// buildPrefetcherWithClients returns a realRGTPrefetcher whose per-region
// `new` closure returns the pre-built client mapped by region. Unknown
// regions fall through to an empty fake (zero pages).
func buildPrefetcherWithClients(byRegion map[string]*fakeRGTClient) *realRGTPrefetcher {
	return &realRGTPrefetcher{
		new: func(region string) rgtClient {
			c, ok := byRegion[region]
			if !ok {
				return &fakeRGTClient{}
			}
			return c
		},
	}
}

func TestRGTPrefetcher_HappyPath_BucketsByCFNType(t *testing.T) {
	t.Parallel()
	fake := &fakeRGTClient{
		pages: [][]rgttypes.ResourceTagMapping{
			{
				mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-aaa", "proj-1"),
				mapping("arn:aws:ec2:us-east-1:111111111111:subnet/subnet-bbb", "proj-1"),
				mapping("arn:aws:sns:us-east-1:111111111111:topic-c", "proj-1"),
			},
		},
	}
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{"us-east-1": fake})

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1"},
		DiscoverArgs{Project: "proj-1"})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if cache == nil {
		t.Fatal("cache: got nil, want populated")
	}

	if infos, ok := cache.ForCFN("us-east-1", "AWS::EC2::VPC"); !ok || len(infos) != 1 || infos[0].ARN != "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-aaa" {
		t.Errorf("VPC bucket: ok=%v infos=%+v", ok, infos)
	}
	if infos, ok := cache.ForCFN("us-east-1", "AWS::EC2::Subnet"); !ok || len(infos) != 1 || infos[0].ARN != "arn:aws:ec2:us-east-1:111111111111:subnet/subnet-bbb" {
		t.Errorf("Subnet bucket: ok=%v infos=%+v", ok, infos)
	}
	if infos, ok := cache.ForCFN("us-east-1", "AWS::SNS::Topic"); !ok || len(infos) != 1 {
		t.Errorf("SNS bucket: ok=%v infos=%+v", ok, infos)
	}
}

func TestRGTPrefetcher_ProjectTagFilter_WiredIntoRequest(t *testing.T) {
	t.Parallel()
	var capturedFilters []rgttypes.TagFilter
	p := &realRGTPrefetcher{
		new: func(_ string) rgtClient {
			return &capturingRGTClient{capture: func(in *resourcegroupstaggingapi.GetResourcesInput) {
				capturedFilters = in.TagFilters
			}}
		},
	}

	_, err := p.Prefetch(context.Background(), []string{"us-east-1"},
		DiscoverArgs{
			Project: "proj-1",
			TagSelectors: []TagSelector{
				{Key: "Environment", Value: "prod"},
				{Key: "Owner", Value: "team-x"},
			},
		})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if len(capturedFilters) != 3 {
		t.Fatalf("filters: got %d, want 3 (Project + 2 selectors)", len(capturedFilters))
	}
	wantKeys := []string{"Project", "Environment", "Owner"}
	for i, want := range wantKeys {
		if aws.ToString(capturedFilters[i].Key) != want {
			t.Errorf("filter[%d].Key = %q, want %q", i, aws.ToString(capturedFilters[i].Key), want)
		}
	}
}

func TestRGTPrefetcher_NoFilters_ReturnsNilCache_NoAPICall(t *testing.T) {
	t.Parallel()
	fake := &fakeRGTClient{}
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{"us-east-1": fake})

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1"},
		DiscoverArgs{})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if cache != nil {
		t.Errorf("cache: got non-nil, want nil (no filter → skip prefetch)")
	}
	if got := fake.calls.Load(); got != 0 {
		t.Errorf("API calls: got %d, want 0", got)
	}
}

func TestRGTPrefetcher_Paginates_AcrossMultiplePages(t *testing.T) {
	t.Parallel()
	fake := &fakeRGTClient{
		pages: [][]rgttypes.ResourceTagMapping{
			{mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-page1", "proj-1")},
			{mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-page2", "proj-1")},
			{mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-page3", "proj-1")},
		},
	}
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{"us-east-1": fake})

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1"},
		DiscoverArgs{Project: "proj-1"})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if got := fake.calls.Load(); got != 3 {
		t.Errorf("API calls: got %d, want 3 (one per page)", got)
	}
	infos, ok := cache.ForCFN("us-east-1", "AWS::EC2::VPC")
	if !ok {
		t.Fatal("VPC bucket: not found")
	}
	if len(infos) != 3 {
		t.Errorf("infos: got %d, want 3", len(infos))
	}
	// Verify all three ARNs landed (sorted by ARN per Prefetch contract).
	wantARNs := []string{
		"arn:aws:ec2:us-east-1:111111111111:vpc/vpc-page1",
		"arn:aws:ec2:us-east-1:111111111111:vpc/vpc-page2",
		"arn:aws:ec2:us-east-1:111111111111:vpc/vpc-page3",
	}
	for i, want := range wantARNs {
		if infos[i].ARN != want {
			t.Errorf("infos[%d].ARN = %q, want %q", i, infos[i].ARN, want)
		}
	}
}

func TestRGTPrefetcher_MultiRegion_PerRegionBuckets(t *testing.T) {
	t.Parallel()
	east := &fakeRGTClient{pages: [][]rgttypes.ResourceTagMapping{
		{mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-east", "proj-1")},
	}}
	west := &fakeRGTClient{pages: [][]rgttypes.ResourceTagMapping{
		{mapping("arn:aws:ec2:us-west-2:111111111111:vpc/vpc-west", "proj-1")},
	}}
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{
		"us-east-1": east,
		"us-west-2": west,
	})

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1", "us-west-2"},
		DiscoverArgs{Project: "proj-1"})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if east.calls.Load() != 1 || west.calls.Load() != 1 {
		t.Errorf("calls: east=%d west=%d, want 1+1", east.calls.Load(), west.calls.Load())
	}
	if infos, ok := cache.ForCFN("us-east-1", "AWS::EC2::VPC"); !ok || len(infos) != 1 || infos[0].ARN != "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-east" {
		t.Errorf("east VPC: ok=%v infos=%+v", ok, infos)
	}
	if infos, ok := cache.ForCFN("us-west-2", "AWS::EC2::VPC"); !ok || len(infos) != 1 || infos[0].ARN != "arn:aws:ec2:us-west-2:111111111111:vpc/vpc-west" {
		t.Errorf("west VPC: ok=%v infos=%+v", ok, infos)
	}
}

func TestRGTPrefetcher_PerRegionFailure_WarnsAndOmits(t *testing.T) {
	t.Parallel()
	east := &fakeRGTClient{pages: [][]rgttypes.ResourceTagMapping{
		{mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-east", "proj-1")},
	}}
	westErr := errors.New("east is up but west is down")
	west := &fakeRGTClient{err: westErr}
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{
		"us-east-1": east,
		"us-west-2": west,
	})
	rec := &recordingEmitter{}

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1", "us-west-2"},
		DiscoverArgs{Project: "proj-1", Emitter: rec})
	if err != nil {
		t.Fatalf("Prefetch: %v (per-region failures should warn, not error)", err)
	}
	// east bucket present
	if _, ok := cache.ForCFN("us-east-1", "AWS::EC2::VPC"); !ok {
		t.Error("east-1 bucket missing despite success")
	}
	// west bucket omitted (region key absent)
	if _, ok := cache.ForCFN("us-west-2", "AWS::EC2::VPC"); ok {
		t.Error("west-2 bucket present despite API failure (should be omitted)")
	}
	// warn emitted for west
	var sawWest bool
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" && ev.Service == "rgt" && ev.Region == "us-west-2" {
			sawWest = true
			break
		}
	}
	if !sawWest {
		t.Error("no service_warn emitted for us-west-2 failure")
	}
}

func TestRGTPrefetcher_UnmappableARN_WarnsOnce(t *testing.T) {
	t.Parallel()
	fake := &fakeRGTClient{
		pages: [][]rgttypes.ResourceTagMapping{
			{
				// Unmapped — no rule for organizations.
				mapping("arn:aws:organizations::111111111111:account/o-123/111111111111", "proj-1"),
				mapping("arn:aws:organizations::111111111111:account/o-123/222222222222", "proj-1"),
				// Mapped.
				mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-aaa", "proj-1"),
			},
		},
	}
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{"us-east-1": fake})
	rec := &recordingEmitter{}

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1"},
		DiscoverArgs{Project: "proj-1", Emitter: rec})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	// Mapped survived.
	if _, ok := cache.ForCFN("us-east-1", "AWS::EC2::VPC"); !ok {
		t.Error("mapped VPC missing from cache")
	}
	// Unmapped warned exactly once (despite 2 unmapped ARNs of the same kind).
	var warns int
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" && ev.Service == "rgt" && ev.Region == "" {
			warns++
		}
	}
	if warns != 1 {
		t.Errorf("unmapped warns: got %d, want 1 (one per service/resourceType pair)", warns)
	}
}

func TestRGTCache_ForGlobalCFN_DedupesAcrossRegions(t *testing.T) {
	t.Parallel()
	// Simulate an IAM role appearing in three regions (RGT surfaces
	// global resources in every region's response).
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {
			"AWS::IAM::Role": {{ARN: "arn:aws:iam::111111111111:role/r-dupe", Tags: map[string]string{"Project": "p"}}},
		},
		"us-west-2": {
			"AWS::IAM::Role": {
				{ARN: "arn:aws:iam::111111111111:role/r-dupe", Tags: map[string]string{"Project": "p"}},
				{ARN: "arn:aws:iam::111111111111:role/r-only-west", Tags: map[string]string{"Project": "p"}},
			},
		},
	}}

	got, ok := cache.ForGlobalCFN("AWS::IAM::Role")
	if !ok {
		t.Fatal("ForGlobalCFN: ok=false, want true")
	}
	sort.SliceStable(got, func(i, j int) bool { return got[i].ARN < got[j].ARN })
	want := []string{"arn:aws:iam::111111111111:role/r-dupe", "arn:aws:iam::111111111111:role/r-only-west"}
	if len(got) != len(want) {
		t.Fatalf("global infos: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ARN != w {
			t.Errorf("[%d] got %q, want %q", i, got[i].ARN, w)
		}
	}
}

func TestRGTCache_ForCFN_MissReturnsFalse(t *testing.T) {
	t.Parallel()
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {"AWS::EC2::VPC": {{ARN: "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-a"}}},
	}}

	if _, ok := cache.ForCFN("us-west-2", "AWS::EC2::VPC"); ok {
		t.Error("unknown region: ok=true, want false")
	}
	if _, ok := cache.ForCFN("us-east-1", "AWS::EC2::Subnet"); ok {
		t.Error("unknown cfnType in known region: ok=true, want false")
	}
}

func TestRGTCache_NilReceiver_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var c *rgtCache
	if _, ok := c.ForCFN("us-east-1", "AWS::EC2::VPC"); ok {
		t.Error("nil cache ForCFN: ok=true, want false")
	}
	if _, ok := c.ForGlobalCFN("AWS::IAM::Role"); ok {
		t.Error("nil cache ForGlobalCFN: ok=true, want false")
	}
}

func TestRGTPrefetcher_EmptyResults_NoBucketsCreated(t *testing.T) {
	t.Parallel()
	// One region returns zero resources — the prefetcher must NOT add
	// an empty map for that region (downstream ForCFN expects "no
	// entry" not "empty entry").
	east := &fakeRGTClient{pages: [][]rgttypes.ResourceTagMapping{
		{mapping("arn:aws:ec2:us-east-1:111111111111:vpc/vpc-aaa", "p")},
	}}
	west := &fakeRGTClient{} // empty
	p := buildPrefetcherWithClients(map[string]*fakeRGTClient{
		"us-east-1": east,
		"us-west-2": west,
	})

	cache, err := p.Prefetch(context.Background(), []string{"us-east-1", "us-west-2"},
		DiscoverArgs{Project: "p"})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if _, ok := cache.byRegionAndType["us-west-2"]; ok {
		t.Error("empty region us-west-2 should not appear as a key in the cache")
	}
}

func TestNoopRGTPrefetcher_ReturnsNilNil(t *testing.T) {
	t.Parallel()
	c, err := noopRGTPrefetcher{}.Prefetch(context.Background(), []string{"us-east-1"}, DiscoverArgs{Project: "p"})
	if err != nil {
		t.Errorf("noop: err=%v, want nil", err)
	}
	if c != nil {
		t.Errorf("noop: cache=%+v, want nil", c)
	}
}

// capturingRGTClient is a one-shot client used by
// TestRGTPrefetcher_ProjectTagFilter_WiredIntoRequest. Tests that need to
// inspect the request shape (vs. just the response) inject this.
type capturingRGTClient struct {
	capture func(*resourcegroupstaggingapi.GetResourcesInput)
}

func (c *capturingRGTClient) GetResources(_ context.Context,
	in *resourcegroupstaggingapi.GetResourcesInput,
	_ ...func(*resourcegroupstaggingapi.Options),
) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	c.capture(in)
	return &resourcegroupstaggingapi.GetResourcesOutput{}, nil
}
