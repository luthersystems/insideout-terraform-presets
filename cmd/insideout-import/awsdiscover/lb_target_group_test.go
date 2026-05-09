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

// errLBTGSeed pins the same SDK-error propagation contract as
// errLBSeed for the target-group discoverer.
var errLBTGSeed = errors.New("AccessDenied")

type fakeLBTGClient struct {
	pages    []elasticloadbalancingv2.DescribeTargetGroupsOutput
	pageErr  error
	tagsByID map[string][]elbv2types.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []elasticloadbalancingv2.DescribeTargetGroupsInput
	tagCalls  [][]string
}

func (f *fakeLBTGClient) DescribeTargetGroups(_ context.Context, in *elasticloadbalancingv2.DescribeTargetGroupsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.pageErr != nil {
		return nil, f.pageErr
	}
	if len(in.Names) == 1 || len(in.TargetGroupArns) == 1 {
		var out elasticloadbalancingv2.DescribeTargetGroupsOutput
		for _, p := range f.pages {
			for _, tg := range p.TargetGroups {
				switch {
				case len(in.Names) == 1 && aws.ToString(tg.TargetGroupName) == in.Names[0]:
					out.TargetGroups = []elbv2types.TargetGroup{tg}
					return &out, nil
				case len(in.TargetGroupArns) == 1 && aws.ToString(tg.TargetGroupArn) == in.TargetGroupArns[0]:
					out.TargetGroups = []elbv2types.TargetGroup{tg}
					return &out, nil
				}
			}
		}
		return nil, &elbv2types.TargetGroupNotFoundException{}
	}
	if idx >= len(f.pages) {
		return &elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeLBTGClient) DescribeTags(_ context.Context, in *elasticloadbalancingv2.DescribeTagsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error) {
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, append([]string(nil), in.ResourceArns...))
	f.mu.Unlock()
	out := &elasticloadbalancingv2.DescribeTagsOutput{}
	for _, arn := range in.ResourceArns {
		if err, ok := f.tagsErr[arn]; ok {
			return nil, err
		}
		td := elbv2types.TagDescription{ResourceArn: aws.String(arn), Tags: f.tagsByID[arn]}
		out.TagDescriptions = append(out.TagDescriptions, td)
	}
	return out, nil
}

func tgFixture(name, arn, vpc string, port int32, protocol elbv2types.ProtocolEnum) elbv2types.TargetGroup {
	return elbv2types.TargetGroup{
		TargetGroupName: aws.String(name),
		TargetGroupArn:  aws.String(arn),
		VpcId:           aws.String(vpc),
		Port:            aws.Int32(port),
		Protocol:        protocol,
	}
}

func TestLBTargetGroupDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	tg1 := tgFixture("io-foo-tg-a", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-tg-a/aaa", "vpc-1", 80, elbv2types.ProtocolEnumHttp)
	tg2 := tgFixture("io-foo-tg-b", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-tg-b/bbb", "vpc-1", 443, elbv2types.ProtocolEnumHttps)
	d := &lbTargetGroupDiscoverer{
		new: func(_ string) lbTargetGroupClient {
			return &fakeLBTGClient{
				pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{tg1, tg2}}},
				tagsByID: map[string][]elbv2types.Tag{
					aws.ToString(tg1.TargetGroupArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(tg2.TargetGroupArn): {elbv2Tag("Project", "io-foo")},
				},
			}
		},
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != lbTargetGroupTFType {
			t.Errorf("Type=%q, want %q", ir.Identity.Type, lbTargetGroupTFType)
		}
		for _, k := range []string{"target_group_arn", "target_group_name", "vpc_id", "protocol", "port"} {
			if ir.Identity.NativeIDs[k] == "" {
				t.Errorf("NativeIDs[%s] empty for %q", k, ir.Identity.NameHint)
			}
		}
	}
	if got[0].Identity.NameHint != "io-foo-tg-a" {
		t.Errorf("first NameHint=%q, want io-foo-tg-a (sorted)", got[0].Identity.NameHint)
	}
	if got[0].Identity.NativeIDs["port"] != "80" {
		t.Errorf("first NativeIDs[port]=%q, want 80", got[0].Identity.NativeIDs["port"])
	}
}

func TestLBTargetGroupDiscover_PaginatesUntilNoMarker(t *testing.T) {
	t.Parallel()
	tg1 := tgFixture("io-foo-a", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-a/aaa", "vpc-1", 80, elbv2types.ProtocolEnumHttp)
	tg2 := tgFixture("io-foo-b", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-b/bbb", "vpc-1", 81, elbv2types.ProtocolEnumHttp)
	tg3 := tgFixture("io-foo-c", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-c/ccc", "vpc-1", 82, elbv2types.ProtocolEnumHttp)
	fake := &fakeLBTGClient{
		pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{
			{TargetGroups: []elbv2types.TargetGroup{tg1}, NextMarker: aws.String("p2")},
			{TargetGroups: []elbv2types.TargetGroup{tg2}, NextMarker: aws.String("p3")},
			{TargetGroups: []elbv2types.TargetGroup{tg3}},
		},
		tagsByID: map[string][]elbv2types.Tag{
			aws.ToString(tg1.TargetGroupArn): {elbv2Tag("Project", "io-foo")},
			aws.ToString(tg2.TargetGroupArn): {elbv2Tag("Project", "io-foo")},
			aws.ToString(tg3.TargetGroupArn): {elbv2Tag("Project", "io-foo")},
		},
	}
	d := &lbTargetGroupDiscoverer{
		new:            func(_ string) lbTargetGroupClient { return fake },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.listCalls) < 3 {
		t.Fatalf("DescribeTargetGroups calls=%d, want >=3", len(fake.listCalls))
	}
	if aws.ToString(fake.listCalls[1].Marker) != "p2" {
		t.Errorf("call[1].Marker=%q, want p2", aws.ToString(fake.listCalls[1].Marker))
	}
	if aws.ToString(fake.listCalls[2].Marker) != "p3" {
		t.Errorf("call[2].Marker=%q, want p3", aws.ToString(fake.listCalls[2].Marker))
	}
}

func TestLBTargetGroupDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	tg1 := tgFixture("io-foo-keep", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-keep/aaa", "vpc", 80, elbv2types.ProtocolEnumHttp)
	tg2 := tgFixture("other-team-tg", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/other-team-tg/bbb", "vpc", 80, elbv2types.ProtocolEnumHttp)
	fake := &fakeLBTGClient{
		pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{tg1, tg2}}},
		tagsByID: map[string][]elbv2types.Tag{
			aws.ToString(tg1.TargetGroupArn): {elbv2Tag("Project", "io-foo")},
		},
	}
	d := &lbTargetGroupDiscoverer{new: func(_ string) lbTargetGroupClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-keep" {
		t.Errorf("NameHint=%q", got[0].Identity.NameHint)
	}
	if len(fake.tagCalls) != 1 {
		t.Errorf("expected DescribeTags only on prefix-matching TG; got %d call(s)", len(fake.tagCalls))
	}
}

func TestLBTargetGroupDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &lbTargetGroupDiscoverer{
		new: func(_ string) lbTargetGroupClient {
			return &fakeLBTGClient{pageErr: errLBTGSeed}
		},
		maxConcurrency: 4,
	}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errLBTGSeed) {
		t.Errorf("err=%v, want errors.Is(err, errLBTGSeed)", err)
	}
}

func TestLBTargetGroupDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-tg/aaa"
	tg := tgFixture("io-foo-tg", arn, "vpc-1", 80, elbv2types.ProtocolEnumHttp)
	d := &lbTargetGroupDiscoverer{
		new: func(_ string) lbTargetGroupClient {
			return &fakeLBTGClient{
				pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{tg}}},
			}
		},
	}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != lbTargetGroupTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-tg" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != arn {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, arn)
	}
	// Bare-name form.
	got2, err := d.DiscoverByID(context.Background(), "io-foo-tg", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Identity.NameHint != "io-foo-tg" {
		t.Errorf("bare-name NameHint=%q", got2.Identity.NameHint)
	}
}

func TestLBTargetGroupDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &lbTargetGroupDiscoverer{new: func(_ string) lbTargetGroupClient { return &fakeLBTGClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing-tg", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestLBTargetGroupDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	clients := map[string]*fakeLBTGClient{
		"us-east-1": {
			pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{
				tgFixture("io-foo-east", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-east/aaa", "vpc", 80, elbv2types.ProtocolEnumHttp),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-east/aaa": {elbv2Tag("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{
				tgFixture("io-foo-west", "arn:aws:elasticloadbalancing:eu-west-1:123:targetgroup/io-foo-west/bbb", "vpc", 80, elbv2types.ProtocolEnumHttp),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:eu-west-1:123:targetgroup/io-foo-west/bbb": {elbv2Tag("Project", "io-foo")},
			},
		},
	}
	var seenRegions []string
	d := &lbTargetGroupDiscoverer{new: func(region string) lbTargetGroupClient {
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
		t.Fatalf("len=%d, want 2", len(got))
	}
	for region, fake := range clients {
		if len(fake.listCalls) < 1 {
			t.Errorf("region=%s: expected >=1 DescribeTargetGroups call; got %d", region, len(fake.listCalls))
		}
	}
}

func TestLBTargetGroupDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	tg1 := tgFixture("io-foo-prod", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-prod/aaa", "vpc", 80, elbv2types.ProtocolEnumHttp)
	tg2 := tgFixture("io-foo-stag", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-stag/bbb", "vpc", 80, elbv2types.ProtocolEnumHttp)
	d := &lbTargetGroupDiscoverer{
		new: func(_ string) lbTargetGroupClient {
			return &fakeLBTGClient{
				pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{tg1, tg2}}},
				tagsByID: map[string][]elbv2types.Tag{
					aws.ToString(tg1.TargetGroupArn): {elbv2Tag("env", "prod")},
					aws.ToString(tg2.TargetGroupArn): {elbv2Tag("env", "staging")},
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
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-prod" {
		t.Errorf("NameHint=%q", got[0].Identity.NameHint)
	}
}

func TestLBTargetGroupDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	clients := map[string]*fakeLBTGClient{
		"us-east-1": {
			pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{
				tgFixture("io-foo-east", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-east/aaa", "vpc", 80, elbv2types.ProtocolEnumHttp),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-east/aaa": {elbv2Tag("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{
				tgFixture("io-foo-west", "arn:aws:elasticloadbalancing:eu-west-1:123:targetgroup/io-foo-west/bbb", "vpc", 80, elbv2types.ProtocolEnumHttp),
			}}},
			tagsByID: map[string][]elbv2types.Tag{
				"arn:aws:elasticloadbalancing:eu-west-1:123:targetgroup/io-foo-west/bbb": {elbv2Tag("Project", "io-foo")},
			},
		},
	}
	d := &lbTargetGroupDiscoverer{new: func(region string) lbTargetGroupClient { return clients[region] }, maxConcurrency: 4}
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
			if e.Service != lbTargetGroupSlug {
				t.Errorf("service_start.service=%q, want %q", e.Service, lbTargetGroupSlug)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != lbTargetGroupSlug {
				t.Errorf("service_finish.service=%q, want %q", e.Service, lbTargetGroupSlug)
			}
			finishes[e.Region]++
		}
	}
	for _, r := range []string{"us-east-1", "eu-west-1"} {
		if starts[r] != 1 {
			t.Errorf("region=%s: service_start=%d, want 1", r, starts[r])
		}
		if finishes[r] != 1 {
			t.Errorf("region=%s: service_finish=%d, want 1", r, finishes[r])
		}
	}
}

func TestLBTargetGroupDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	tgs := []elbv2types.TargetGroup{
		tgFixture("io-foo-a", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-a/aaa", "vpc", 80, elbv2types.ProtocolEnumHttp),
		tgFixture("io-foo-b", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-b/bbb", "vpc", 81, elbv2types.ProtocolEnumHttp),
		tgFixture("io-foo-c", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-c/ccc", "vpc", 82, elbv2types.ProtocolEnumHttp),
	}
	tagsByID := map[string][]elbv2types.Tag{}
	for _, tg := range tgs {
		tagsByID[aws.ToString(tg.TargetGroupArn)] = []elbv2types.Tag{elbv2Tag("Project", "io-foo")}
	}
	d := &lbTargetGroupDiscoverer{
		new: func(_ string) lbTargetGroupClient {
			return &fakeLBTGClient{
				pages:    []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: tgs}},
				tagsByID: tagsByID,
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
		if it.Service != lbTargetGroupSlug {
			t.Errorf("item.service=%q, want %q", it.Service, lbTargetGroupSlug)
		}
		if it.TFType != lbTargetGroupTFType {
			t.Errorf("item.tf_type=%q, want %q", it.TFType, lbTargetGroupTFType)
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

func TestLBTargetGroupDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &lbTargetGroupDiscoverer{new: func(_ string) lbTargetGroupClient { return &fakeLBTGClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc", // wrong shape
		"name with spaces", // invalid bare-name
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestLBTargetGroupDiscover_EmptyProjectReturnsAll pins that an empty
// Project disables the prefix filter.
func TestLBTargetGroupDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	tg1 := tgFixture("io-foo-a", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-a/aaa", "vpc", 80, elbv2types.ProtocolEnumHttp)
	tg2 := tgFixture("other-tg", "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/other-tg/bbb", "vpc", 80, elbv2types.ProtocolEnumHttp)
	fake := &fakeLBTGClient{
		pages: []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: []elbv2types.TargetGroup{tg1, tg2}}},
		tagsByID: map[string][]elbv2types.Tag{
			aws.ToString(tg1.TargetGroupArn): {},
			aws.ToString(tg2.TargetGroupArn): {},
		},
	}
	d := &lbTargetGroupDiscoverer{new: func(_ string) lbTargetGroupClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no prefix filter)", len(got))
	}
}

// blockingLBTGClient mirrors blockingDynamoClient — used for the
// bounded-concurrency test below.
type blockingLBTGClient struct {
	pages []elasticloadbalancingv2.DescribeTargetGroupsOutput
	tags  map[string][]elbv2types.Tag

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int

	listIdx int
}

func (c *blockingLBTGClient) DescribeTargetGroups(_ context.Context, _ *elasticloadbalancingv2.DescribeTargetGroupsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingLBTGClient) DescribeTags(ctx context.Context, in *elasticloadbalancingv2.DescribeTagsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error) {
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

// TestLBTargetGroupDiscover_BoundedConcurrency mirrors dynamodb_test.go:
// per-TG DescribeTags fan-out must respect the configured limit.
func TestLBTargetGroupDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	tgs := make([]elbv2types.TargetGroup, total)
	tags := make(map[string][]elbv2types.Tag, total)
	for i := 0; i < total; i++ {
		arn := fmt.Sprintf("arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/io-foo-%d/abc-%d", i, i)
		tgs[i] = tgFixture(fmt.Sprintf("io-foo-%d", i), arn, "vpc", 80, elbv2types.ProtocolEnumHttp)
		tags[arn] = []elbv2types.Tag{elbv2Tag("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingLBTGClient{
		pages:   []elasticloadbalancingv2.DescribeTargetGroupsOutput{{TargetGroups: tgs}},
		tags:    tags,
		release: release,
	}
	d := &lbTargetGroupDiscoverer{new: func(_ string) lbTargetGroupClient { return bc }, maxConcurrency: limit}
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
