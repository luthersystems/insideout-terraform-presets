package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// TestRGTPrefetcher_ProjectTagFilter_WiredIntoRequest pins that every
// piece of the TagFilter request is correct — both Key AND Value, AND
// the single-element Values slice shape. A regression that swapped Value
// for Key (or appended stray values) would survive a Key-only assertion;
// the full triple-check (Key, len(Values)==1, Values[0]) blocks that.
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
	want := []struct {
		key, value string
	}{
		{"Project", "proj-1"},
		{"Environment", "prod"},
		{"Owner", "team-x"},
	}
	for i, w := range want {
		if aws.ToString(capturedFilters[i].Key) != w.key {
			t.Errorf("filter[%d].Key = %q, want %q", i, aws.ToString(capturedFilters[i].Key), w.key)
		}
		if len(capturedFilters[i].Values) != 1 {
			t.Errorf("filter[%d].Values len = %d, want 1 (one value per AND-conjunction filter)", i, len(capturedFilters[i].Values))
			continue
		}
		if capturedFilters[i].Values[0] != w.value {
			t.Errorf("filter[%d].Values[0] = %q, want %q", i, capturedFilters[i].Values[0], w.value)
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

// TestRGTPrefetcher_UnmappableARN_WarnsOncePerPair pins the
// "once per (service, resourceType) pair" warn semantics, not just
// "deduped within a pair." Fixture has TWO distinct unmapped pairs
// (organizations + fsx), each appearing twice — must produce exactly
// 2 warns, with the warn messages containing the per-pair keys so a
// regression that emits a generic "rgt: unmapped" placeholder also
// fails.
func TestRGTPrefetcher_UnmappableARN_WarnsOncePerPair(t *testing.T) {
	t.Parallel()
	fake := &fakeRGTClient{
		pages: [][]rgttypes.ResourceTagMapping{
			{
				// Pair A: organizations/account (2 ARNs).
				mapping("arn:aws:organizations::111111111111:account/o-123/111111111111", "proj-1"),
				mapping("arn:aws:organizations::111111111111:account/o-123/222222222222", "proj-1"),
				// Pair B: fsx/file-system (2 ARNs).
				mapping("arn:aws:fsx:us-east-1:111111111111:file-system/fs-aaaaa", "proj-1"),
				mapping("arn:aws:fsx:us-east-1:111111111111:file-system/fs-bbbbb", "proj-1"),
				// Mapped — should still survive.
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
	if _, ok := cache.ForCFN("us-east-1", "AWS::EC2::VPC"); !ok {
		t.Error("mapped VPC missing from cache")
	}

	var (
		orgWarn, fsxWarn int
		seenMessages     []string
	)
	for _, ev := range rec.snapshot() {
		if ev.Kind != "service_warn" || ev.Service != "rgt" || ev.Region != "" {
			continue
		}
		seenMessages = append(seenMessages, ev.Message)
		switch {
		case strings.Contains(ev.Message, "organizations/account"):
			orgWarn++
		case strings.Contains(ev.Message, "fsx/file-system"):
			fsxWarn++
		}
	}
	if orgWarn != 1 {
		t.Errorf("organizations/account warns: got %d, want 1 (messages: %v)", orgWarn, seenMessages)
	}
	if fsxWarn != 1 {
		t.Errorf("fsx/file-system warns: got %d, want 1 (messages: %v)", fsxWarn, seenMessages)
	}
	if len(seenMessages) != 2 {
		t.Errorf("total unmapped warns: got %d, want 2 (one per distinct pair); messages: %v", len(seenMessages), seenMessages)
	}
}

// TestRGTPrefetcher_KnownSkipRule_NoWarnNoBucket pins that an ARN
// matched by a rule with cfnType=="" (an explicit known-skip — e.g.
// AWS::ApiGatewayV2::Stage which is hand-rolled because Cloud Control
// READ is unsupported) is silently dropped: no entry in the cache, no
// "no arnRule" warn. Without this, every project's stage ARNs floods
// the warn stream on every discover run (surfaced during the Bundle
// 13c live smoke). The fixture also includes a genuinely-unmapped ARN
// (organizations/account) so the test pins INDEPENDENCE of the
// known-skip path from the unmapped-aggregator path: the org warn
// must still fire, and the stage ARN must not get swept into it.
func TestRGTPrefetcher_KnownSkipRule_NoWarnNoBucket(t *testing.T) {
	t.Parallel()
	fake := &fakeRGTClient{
		pages: [][]rgttypes.ResourceTagMapping{
			{
				// Stage ARN — matches the known-skip rule, cfnType="".
				mapping("arn:aws:apigateway:us-east-1::/apis/abc/stages/$default", "proj-1"),
				// Api ARN — matches the AWS::ApiGatewayV2::Api rule.
				mapping("arn:aws:apigateway:us-east-1::/apis/abc", "proj-1"),
				// Genuinely-unmapped ARN — pins that known-skip
				// suppression doesn't accidentally muzzle the
				// unmapped-pair aggregator (the two paths must
				// remain independent).
				mapping("arn:aws:organizations::111111111111:account/o-123/111111111111", "proj-1"),
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
	// Api should be cached.
	if infos, ok := cache.ForCFN("us-east-1", "AWS::ApiGatewayV2::Api"); !ok || len(infos) != 1 {
		t.Errorf("Api bucket missing; got ok=%v len=%d", ok, len(infos))
	}
	// Stage must NOT appear under its cfnType (known-skip drops it).
	// ForCFN returns ok=true because the region was prefetched (the
	// authoritative-zero contract), but the slice MUST be empty —
	// the prefetcher must not have bucketed the Stage ARN.
	if infos, _ := cache.ForCFN("us-east-1", "AWS::ApiGatewayV2::Stage"); len(infos) != 0 {
		t.Errorf("Stage cached under its cfnType; len=%d, want 0 (known-skip)", len(infos))
	}
	// Belt-and-suspenders: walk the raw byType map and confirm no
	// known-skip cfnType key ever got created. An empty-string key
	// would be a different bug (rule.cfnType being used as a map
	// key without the skip guard).
	if _, exists := cache.byRegionAndType["us-east-1"][""]; exists {
		t.Error("known-skip ARN got bucketed under cfnType=\"\" (skip guard missing)")
	}
	// Walk every rgt warn and partition into the three signals we care about.
	var (
		stageWarn, orgWarn int
		messages           []string
	)
	for _, ev := range rec.snapshot() {
		if ev.Kind != "service_warn" || ev.Service != "rgt" {
			continue
		}
		messages = append(messages, ev.Message)
		switch {
		case strings.Contains(ev.Message, "apigateway/apis"):
			stageWarn++
		case strings.Contains(ev.Message, "organizations/account"):
			orgWarn++
		}
	}
	if stageWarn != 0 {
		t.Errorf("known-skip emitted warn: %d (Stage should be silent); messages=%v", stageWarn, messages)
	}
	if orgWarn != 1 {
		t.Errorf("unmapped organizations warn: got %d, want 1 (independence pin); messages=%v", orgWarn, messages)
	}
	if len(messages) != 1 {
		t.Errorf("total rgt warns: got %d, want 1 (just the unmapped org pair); messages=%v", len(messages), messages)
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

func TestRGTCache_ForCFN_UnknownRegionMisses(t *testing.T) {
	t.Parallel()
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {"AWS::EC2::VPC": {{ARN: "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-a"}}},
	}}

	if _, ok := cache.ForCFN("us-west-2", "AWS::EC2::VPC"); ok {
		t.Error("unknown region: ok=true, want false (RGT didn't run there, caller must fall back)")
	}
}

func TestRGTCache_ForCFN_KnownRegionEmptyForCFN_AuthoritativeZero(t *testing.T) {
	t.Parallel()
	// RGT ran for us-east-1 and saw one VPC but zero Subnets. The
	// Subnet query must return ok=true with an empty slice — the
	// caller treats that as "authoritatively zero of these," not as
	// "miss, fall back to ListResources." Many CC types (e.g.
	// AWS::EKS::PodIdentityAssociation) reject ListResources without
	// a parent ResourceModel, so the fallback would 400.
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {"AWS::EC2::VPC": {{ARN: "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-a"}}},
	}}
	infos, ok := cache.ForCFN("us-east-1", "AWS::EC2::Subnet")
	if !ok {
		t.Fatal("known region + unknown cfnType: ok=false, want true (authoritative zero)")
	}
	if len(infos) != 0 {
		t.Errorf("infos=%v, want empty slice", infos)
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

func TestRGTPrefetcher_EmptyRegion_RecordedAsAuthoritativeZero(t *testing.T) {
	t.Parallel()
	// A region whose RGT call succeeded with zero results MUST appear
	// in the cache as an empty map. Downstream ForCFN returns (nil, true)
	// for that region+any-cfnType, signalling "authoritative zero, do
	// NOT fall back to ListResources." Stripping the key would make
	// ForCFN return ok=false → caller falls back to ListResources →
	// parent-scoped types like AWS::EKS::PodIdentityAssociation reject
	// the call with InvalidRequestException 400.
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
	byType, ok := cache.byRegionAndType["us-west-2"]
	if !ok {
		t.Fatal("empty region us-west-2 must appear in cache (authoritative zero signal)")
	}
	if len(byType) != 0 {
		t.Errorf("us-west-2 byType=%v, want empty map", byType)
	}
	// ForCFN through the public path must agree.
	infos, ok := cache.ForCFN("us-west-2", "AWS::EKS::PodIdentityAssociation")
	if !ok {
		t.Error("ForCFN on empty-but-succeeded region: ok=false, want true")
	}
	if len(infos) != 0 {
		t.Errorf("ForCFN infos=%v, want empty slice", infos)
	}
}

func TestRGTCache_ForGlobalCFN_NoRegions_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// Empty-but-non-nil byRegionAndType means no region's RGT call
	// succeeded (or none was attempted). ForGlobalCFN MUST return
	// ok=false so the discoverer falls back to ListResources for
	// global types. The post-fix code guards on len(byRegionAndType)==0
	// at rgt_prefetcher.go; without this test, a regression that
	// removes the guard would silently flip "no region succeeded" into
	// "authoritative zero" and suppress the only viable enumeration
	// path for global types.
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{}}
	if _, ok := cache.ForGlobalCFN("AWS::IAM::Role"); ok {
		t.Error("empty-but-non-nil byRegionAndType: ok=true, want false (no region succeeded → fall back)")
	}
}

func TestRGTCache_ForGlobalCFN_AuthoritativeEmptyWhenAnyRegionRan(t *testing.T) {
	t.Parallel()
	// At least one region's RGT call succeeded, but it found zero
	// global resources of this type. ForGlobalCFN must return
	// ok=true with an empty slice — same authoritative-zero contract
	// as ForCFN. Otherwise global parent-scoped types would 400 on
	// the ListResources fallback when the account legitimately has
	// none of them.
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {"AWS::EC2::VPC": {{ARN: "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-a"}}},
	}}
	infos, ok := cache.ForGlobalCFN("AWS::IAM::Role")
	if !ok {
		t.Fatal("at-least-one-region-ran + zero global hits: ok=false, want true")
	}
	if len(infos) != 0 {
		t.Errorf("infos=%v, want empty slice", infos)
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
