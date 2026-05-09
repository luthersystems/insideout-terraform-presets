package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// errLBSeed is the package-level sentinel for lb error propagation —
// see dynamodb_test.go errSeedListTables for the contract.
var errLBSeed = errors.New("AccessDenied")

type fakeLBClient struct {
	pages    []elasticloadbalancingv2.DescribeLoadBalancersOutput
	pageErr  error
	tagsByID map[string][]elbv2types.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []elasticloadbalancingv2.DescribeLoadBalancersInput
	tagCalls  [][]string
}

func (f *fakeLBClient) DescribeLoadBalancers(_ context.Context, in *elasticloadbalancingv2.DescribeLoadBalancersInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.pageErr != nil {
		return nil, f.pageErr
	}
	// Names / Arns single-result lookups (DiscoverByID path) — synthesize
	// from the first page's matching entry.
	if len(in.Names) == 1 || len(in.LoadBalancerArns) == 1 {
		var out elasticloadbalancingv2.DescribeLoadBalancersOutput
		for _, p := range f.pages {
			for _, lb := range p.LoadBalancers {
				switch {
				case len(in.Names) == 1 && aws.ToString(lb.LoadBalancerName) == in.Names[0]:
					out.LoadBalancers = []elbv2types.LoadBalancer{lb}
					return &out, nil
				case len(in.LoadBalancerArns) == 1 && aws.ToString(lb.LoadBalancerArn) == in.LoadBalancerArns[0]:
					out.LoadBalancers = []elbv2types.LoadBalancer{lb}
					return &out, nil
				}
			}
		}
		return nil, &elbv2types.LoadBalancerNotFoundException{}
	}
	if idx >= len(f.pages) {
		return &elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeLBClient) DescribeTags(_ context.Context, in *elasticloadbalancingv2.DescribeTagsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error) {
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, append([]string(nil), in.ResourceArns...))
	f.mu.Unlock()
	out := &elasticloadbalancingv2.DescribeTagsOutput{}
	for _, arn := range in.ResourceArns {
		if err, ok := f.tagsErr[arn]; ok {
			return nil, err
		}
		td := elbv2types.TagDescription{ResourceArn: aws.String(arn)}
		td.Tags = f.tagsByID[arn]
		out.TagDescriptions = append(out.TagDescriptions, td)
	}
	return out, nil
}

func lbFixture(name, arn, dns, vpc string, lbType elbv2types.LoadBalancerTypeEnum) elbv2types.LoadBalancer {
	return elbv2types.LoadBalancer{
		LoadBalancerName: aws.String(name),
		LoadBalancerArn:  aws.String(arn),
		DNSName:          aws.String(dns),
		VpcId:            aws.String(vpc),
		Type:             lbType,
	}
}

func elbv2Tag(k, v string) elbv2types.Tag {
	return elbv2types.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func TestLBDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	// 3 fixtures so that a regression to length-based sort would not
	// accidentally produce the same ordering as a name-based sort.
	lb1 := lbFixture("io-foo-app", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc", "io-foo-app.elb.amazonaws.com", "vpc-1", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("io-foo-net", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/net/io-foo-net/def", "io-foo-net.elb.amazonaws.com", "vpc-2", elbv2types.LoadBalancerTypeEnumNetwork)
	lb3 := lbFixture("io-foo-gw", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/gwy/io-foo-gw/ghi", "io-foo-gw.elb.amazonaws.com", "vpc-3", elbv2types.LoadBalancerTypeEnumGateway)
	d := &lbDiscoverer{
		new: func(_ string) lbClient {
			return &fakeLBClient{
				pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{
					{LoadBalancers: []elbv2types.LoadBalancer{lb1, lb2, lb3}},
				},
				tagsByID: map[string][]elbv2types.Tag{
					aws.ToString(lb1.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(lb2.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(lb3.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
				},
			}
		},
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != lbTFType {
			t.Errorf("Type=%q, want %q", ir.Identity.Type, lbTFType)
		}
		if ir.Identity.NativeIDs["lb_arn"] == "" {
			t.Error("NativeIDs[lb_arn] empty")
		}
		if ir.Identity.NativeIDs["lb_name"] == "" {
			t.Error("NativeIDs[lb_name] empty")
		}
		if ir.Identity.NativeIDs["dns_name"] == "" {
			t.Error("NativeIDs[dns_name] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] == "" {
			t.Error("NativeIDs[vpc_id] empty")
		}
		if ir.Identity.NativeIDs["type"] == "" {
			t.Error("NativeIDs[type] empty")
		}
	}
	// Sorted by LB name: io-foo-app < io-foo-gw < io-foo-net.
	if got[0].Identity.NameHint != "io-foo-app" {
		t.Errorf("first NameHint=%q, want io-foo-app (sorted)", got[0].Identity.NameHint)
	}
	if got[1].Identity.NameHint != "io-foo-gw" {
		t.Errorf("second NameHint=%q, want io-foo-gw (sorted)", got[1].Identity.NameHint)
	}
	if got[2].Identity.NameHint != "io-foo-net" {
		t.Errorf("third NameHint=%q, want io-foo-net (sorted)", got[2].Identity.NameHint)
	}
	if got[0].Identity.NativeIDs["type"] != "application" {
		t.Errorf("first NativeIDs[type]=%q, want application", got[0].Identity.NativeIDs["type"])
	}
}

func TestLBDiscover_PaginatesUntilNoMarker(t *testing.T) {
	t.Parallel()
	lb1 := lbFixture("io-foo-a", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-a/aaa", "a.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("io-foo-b", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-b/bbb", "b.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb3 := lbFixture("io-foo-c", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-c/ccc", "c.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	fake := &fakeLBClient{
		pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{
			{LoadBalancers: []elbv2types.LoadBalancer{lb1}, NextMarker: aws.String("page2")},
			{LoadBalancers: []elbv2types.LoadBalancer{lb2}, NextMarker: aws.String("page3")},
			{LoadBalancers: []elbv2types.LoadBalancer{lb3}}, // terminal
		},
		tagsByID: map[string][]elbv2types.Tag{
			aws.ToString(lb1.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
			aws.ToString(lb2.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
			aws.ToString(lb3.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
		},
	}
	d := &lbDiscoverer{
		new:            func(_ string) lbClient { return fake },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	// Pin: page-N+1 must thread page-N's Marker.
	if len(fake.listCalls) < 3 {
		t.Fatalf("DescribeLoadBalancers calls=%d, want >=3", len(fake.listCalls))
	}
	if aws.ToString(fake.listCalls[1].Marker) != "page2" {
		t.Errorf("call[1].Marker=%q, want page2 (paginators must thread Marker, not NextMarker)", aws.ToString(fake.listCalls[1].Marker))
	}
	if aws.ToString(fake.listCalls[2].Marker) != "page3" {
		t.Errorf("call[2].Marker=%q, want page3", aws.ToString(fake.listCalls[2].Marker))
	}
}

func TestLBDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	lb1 := lbFixture("io-foo-app", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc", "a.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("other-team-app", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/other-team-app/def", "o.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	fake := &fakeLBClient{
		pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{
			{LoadBalancers: []elbv2types.LoadBalancer{lb1, lb2}},
		},
		tagsByID: map[string][]elbv2types.Tag{
			aws.ToString(lb1.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
		},
	}
	d := &lbDiscoverer{new: func(_ string) lbClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only io-foo prefix-matching LB)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-app" {
		t.Errorf("NameHint=%q, want io-foo-app", got[0].Identity.NameHint)
	}
	// Pin: prefix gates DescribeTags fan-out — non-matching LB must not
	// have generated a DescribeTags call.
	if len(fake.tagCalls) != 1 {
		t.Errorf("expected DescribeTags only on the prefix-matching LB; got %d call(s) on %v", len(fake.tagCalls), fake.tagCalls)
	}
}

func TestLBDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &lbDiscoverer{
		new: func(_ string) lbClient {
			return &fakeLBClient{pageErr: errLBSeed}
		},
		maxConcurrency: 4,
	}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errLBSeed) {
		t.Errorf("err=%v, want errors.Is(err, errLBSeed) — discover swallowed the SDK error", err)
	}
}

func TestLBDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc123"
	lb := lbFixture("io-foo-app", arn, "io-foo-app.elb.amazonaws.com", "vpc-1", elbv2types.LoadBalancerTypeEnumApplication)
	d := &lbDiscoverer{
		new: func(_ string) lbClient {
			return &fakeLBClient{
				pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{
					{LoadBalancers: []elbv2types.LoadBalancer{lb}},
				},
			}
		},
	}
	// ARN form.
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != lbTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-app" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != arn {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, arn)
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
	// Bare-name form.
	got2, err := d.DiscoverByID(context.Background(), "io-foo-app", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Identity.NameHint != "io-foo-app" {
		t.Errorf("bare-name NameHint=%q", got2.Identity.NameHint)
	}
}

func TestLBDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &lbDiscoverer{new: func(_ string) lbClient {
		return &fakeLBClient{} // empty pages -> Names lookup returns LoadBalancerNotFoundException
	}}
	_, err := d.DiscoverByID(context.Background(), "missing-name", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestLBDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	clients := map[string]*fakeLBClient{
		"us-east-1": {
			pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{
				lbFixture("io-foo-east", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-east/aaa", "east.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-east/aaa": {elbv2Tag("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{
				lbFixture("io-foo-west", "arn:aws:elasticloadbalancing:eu-west-1:123:loadbalancer/app/io-foo-west/bbb", "west.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:eu-west-1:123:loadbalancer/app/io-foo-west/bbb": {elbv2Tag("Project", "io-foo")},
			},
		},
	}
	var seenRegions []string
	d := &lbDiscoverer{new: func(region string) lbClient {
		seenRegions = append(seenRegions, region)
		return clients[region]
	}, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seenRegions)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one LB per region)", len(got))
	}
	for region, fake := range clients {
		if len(fake.listCalls) < 1 {
			t.Errorf("region=%s: expected >=1 DescribeLoadBalancers call; got %d", region, len(fake.listCalls))
		}
	}
}

func TestLBDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	lb1 := lbFixture("io-foo-prod", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-prod/aaa", "prod.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("io-foo-stag", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-stag/bbb", "stag.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	d := &lbDiscoverer{
		new: func(_ string) lbClient {
			return &fakeLBClient{
				pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb1, lb2}}},
				tagsByID: map[string][]elbv2types.Tag{
					aws.ToString(lb1.LoadBalancerArn): {elbv2Tag("env", "prod")},
					aws.ToString(lb2.LoadBalancerArn): {elbv2Tag("env", "staging")},
				},
			}
		},
		maxConcurrency: 4,
	}
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
		t.Fatalf("len=%d, want 1 (only env=prod LB)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-prod" {
		t.Errorf("NameHint=%q, want io-foo-prod", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist)", got[0].Identity.Tags["env"])
	}
}

func TestLBDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	clients := map[string]*fakeLBClient{
		"us-east-1": {
			pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{
				lbFixture("io-foo-east", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-east/aaa", "east.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-east/aaa": {elbv2Tag("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{
				lbFixture("io-foo-west", "arn:aws:elasticloadbalancing:eu-west-1:123:loadbalancer/app/io-foo-west/bbb", "west.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:eu-west-1:123:loadbalancer/app/io-foo-west/bbb": {elbv2Tag("Project", "io-foo")},
			},
		},
	}
	d := &lbDiscoverer{new: func(region string) lbClient { return clients[region] }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != lbSlug {
				t.Errorf("service_start.service=%q, want %q", e.Service, lbSlug)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != lbSlug {
				t.Errorf("service_finish.service=%q, want %q", e.Service, lbSlug)
			}
			finishes[e.Region]++
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if starts[region] != 1 {
			t.Errorf("region=%s: service_start count=%d, want 1", region, starts[region])
		}
		if finishes[region] != 1 {
			t.Errorf("region=%s: service_finish count=%d, want 1", region, finishes[region])
		}
	}
}

func TestLBDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	lb1 := lbFixture("io-foo-a", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-a/aaa", "a.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("io-foo-b", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-b/bbb", "b.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb3 := lbFixture("io-foo-c", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-c/ccc", "c.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	d := &lbDiscoverer{
		new: func(_ string) lbClient {
			return &fakeLBClient{
				pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb1, lb2, lb3}}},
				tagsByID: map[string][]elbv2types.Tag{
					aws.ToString(lb1.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(lb2.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(lb3.LoadBalancerArn): {elbv2Tag("Project", "io-foo")},
				},
			}
		},
		maxConcurrency: 4,
	}
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
		t.Errorf("item_found count=%d, want %d", len(items), len(got))
	}
	for _, it := range items {
		if it.Service != lbSlug {
			t.Errorf("item.service=%q, want %q", it.Service, lbSlug)
		}
		if it.TFType != lbTFType {
			t.Errorf("item.tf_type=%q, want %q", it.TFType, lbTFType)
		}
		if it.Region != "us-east-1" {
			t.Errorf("item.region=%q, want us-east-1", it.Region)
		}
	}
	for _, e := range rec.snapshot() {
		if e.Kind == "service_finish" && e.Count != len(got) {
			t.Errorf("service_finish.count=%d, want %d", e.Count, len(got))
		}
	}
}

func TestLBDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &lbDiscoverer{new: func(_ string) lbClient { return &fakeLBClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/something-not-app-net-gwy", // wrong shape
		"arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/foo/bar",                    // not a loadbalancer ARN
		"name with spaces", // invalid bare-name chars
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestLBDiscover_EmptyProjectReturnsAll pins that an empty Project
// disables the LB-name prefix filter — every LB returned by
// DescribeLoadBalancers passes through.
func TestLBDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	lb1 := lbFixture("io-foo-app", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc", "a.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("other-app", "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/other-app/def", "o.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	fake := &fakeLBClient{
		pages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb1, lb2}}},
		tagsByID: map[string][]elbv2types.Tag{
			aws.ToString(lb1.LoadBalancerArn): {},
			aws.ToString(lb2.LoadBalancerArn): {},
		},
	}
	d := &lbDiscoverer{new: func(_ string) lbClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no prefix filter)", len(got))
	}
}

// blockingLBClient mirrors blockingDynamoClient — used for the
// bounded-concurrency test below.
type blockingLBClient struct {
	pages []elasticloadbalancingv2.DescribeLoadBalancersOutput
	tags  map[string][]elbv2types.Tag

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int

	listIdx int
}

func (c *blockingLBClient) DescribeLoadBalancers(_ context.Context, _ *elasticloadbalancingv2.DescribeLoadBalancersInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingLBClient) DescribeTags(ctx context.Context, in *elasticloadbalancingv2.DescribeTagsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error) {
	c.mu.Lock()
	c.inflight++
	if c.inflight > c.maxInflight {
		c.maxInflight = c.inflight
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inflight--
		c.mu.Unlock()
	}()
	select {
	case <-c.release:
		out := &elasticloadbalancingv2.DescribeTagsOutput{}
		for _, arn := range in.ResourceArns {
			td := elbv2types.TagDescription{ResourceArn: aws.String(arn), Tags: c.tags[arn]}
			out.TagDescriptions = append(out.TagDescriptions, td)
		}
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestLBDiscover_BoundedConcurrency mirrors dynamodb_test.go: per-LB
// DescribeTags fan-out must respect the configured concurrency limit.
func TestLBDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	lbs := make([]elbv2types.LoadBalancer, total)
	tags := make(map[string][]elbv2types.Tag, total)
	for i := 0; i < total; i++ {
		arn := fmt.Sprintf("arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-%d/abc-%d", i, i)
		lbs[i] = lbFixture(fmt.Sprintf("io-foo-%d", i), arn, "x.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
		tags[arn] = []elbv2types.Tag{elbv2Tag("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingLBClient{
		pages:   []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: lbs}},
		tags:    tags,
		release: release,
	}
	d := &lbDiscoverer{new: func(_ string) lbClient { return bc }, maxConcurrency: limit}
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
		done <- err
	}()
	deadline := time.After(2 * time.Second)
	for {
		bc.mu.Lock()
		got := bc.inflight
		bc.mu.Unlock()
		if got >= limit {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("never reached %d in-flight; saw %d", limit, got)
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	bc.mu.Lock()
	peak := bc.maxInflight
	bc.mu.Unlock()
	if peak > limit {
		t.Errorf("peak in-flight=%d exceeded limit=%d", peak, limit)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
}
