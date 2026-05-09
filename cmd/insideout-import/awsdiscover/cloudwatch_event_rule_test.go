package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

var errSeedListRules = errors.New("AccessDenied")

type fakeEventBridgeClient struct {
	pages    []eventbridge.ListRulesOutput
	listErr  error
	tagsByID map[string][]ebtypes.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []eventbridge.ListRulesInput
	tagCalls  []string

	describeByName     map[string]*eventbridge.DescribeRuleOutput
	describeErr        error
	describeCalls      []string
	describeReturnsErr bool
}

func (f *fakeEventBridgeClient) ListRules(_ context.Context, in *eventbridge.ListRulesInput, _ ...func(*eventbridge.Options)) (*eventbridge.ListRulesOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &eventbridge.ListRulesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeEventBridgeClient) DescribeRule(_ context.Context, in *eventbridge.DescribeRuleInput, _ ...func(*eventbridge.Options)) (*eventbridge.DescribeRuleOutput, error) {
	name := aws.ToString(in.Name)
	f.mu.Lock()
	f.describeCalls = append(f.describeCalls, name)
	f.mu.Unlock()
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if out, ok := f.describeByName[name]; ok {
		return out, nil
	}
	return nil, &ebtypes.ResourceNotFoundException{}
}

func (f *fakeEventBridgeClient) ListTagsForResource(_ context.Context, in *eventbridge.ListTagsForResourceInput, _ ...func(*eventbridge.Options)) (*eventbridge.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceARN)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &eventbridge.ListTagsForResourceOutput{Tags: f.tagsByID[arn]}, nil
}

func ebTagPair(k, v string) ebtypes.Tag {
	return ebtypes.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func ebRule(name, arn string) ebtypes.Rule {
	return ebtypes.Rule{Name: aws.String(name), Arn: aws.String(arn)}
}

func TestCloudWatchEventRuleDiscover_FiltersByNamePrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		pages: []eventbridge.ListRulesOutput{
			{Rules: []ebtypes.Rule{
				ebRule("io-foo-orders", "arn-orders"),
				ebRule("io-foo-events", "arn-events"),
				ebRule("AutoScalingManagedRule", "arn-asg"),
				ebRule("other-thing", "arn-other"),
			}},
		},
		tagsByID: map[string][]ebtypes.Tag{
			"arn-orders": {ebTagPair("Project", "io-foo")},
			"arn-events": {},
		},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (io-foo-* only)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NameHint == "AutoScalingManagedRule" {
			t.Error("AWS-managed rule leaked through prefix filter")
		}
		if ir.Identity.NameHint == "other-thing" {
			t.Error("non-prefix-matching rule leaked through filter")
		}
		if ir.Identity.NativeIDs["event_bus_name"] == "" {
			t.Error("event_bus_name native id missing")
		}
	}
	// Pin: ListTagsForResource only fires on the prefix-matching rules.
	if len(fake.tagCalls) != 2 {
		t.Errorf("expected 2 ListTagsForResource calls (prefix gating); got %d on %v", len(fake.tagCalls), fake.tagCalls)
	}
}

func TestCloudWatchEventRuleDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		pages: []eventbridge.ListRulesOutput{
			{Rules: []ebtypes.Rule{
				ebRule("rule-a", "arn-a"),
				ebRule("rule-b", "arn-b"),
			}},
		},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no project filter)", len(got))
	}
}

func TestCloudWatchEventRuleDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		pages: []eventbridge.ListRulesOutput{
			{Rules: []ebtypes.Rule{ebRule("io-foo-a", "arn-a")}, NextToken: aws.String("nt1")},
			{Rules: []ebtypes.Rule{ebRule("io-foo-b", "arn-b")}}, // terminal
		},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if len(fake.listCalls) != 2 {
		t.Fatalf("ListRules called %d time(s); want 2", len(fake.listCalls))
	}
	if aws.ToString(fake.listCalls[1].NextToken) != "nt1" {
		t.Errorf("second ListRules call NextToken=%q, want nt1", aws.ToString(fake.listCalls[1].NextToken))
	}
}

func TestCloudWatchEventRuleDiscover_PropagatesListRulesError(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{listErr: errSeedListRules}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errSeedListRules) {
		t.Errorf("err=%v, want errors.Is(err, errSeedListRules)", err)
	}
}

func TestCloudWatchEventRuleDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		pages: []eventbridge.ListRulesOutput{
			{Rules: []ebtypes.Rule{
				ebRule("io-foo-good", "arn-good"),
				ebRule("io-foo-throttled", "arn-throttled"),
			}},
		},
		tagsByID: map[string][]ebtypes.Tag{"arn-good": {}},
		tagsErr:  map[string]error{"arn-throttled": errors.New("Throttling")},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (throttled skipped)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-good" {
		t.Errorf("NameHint=%q", got[0].Identity.NameHint)
	}
}

func TestCloudWatchEventRuleDiscover_DefaultEventBusName(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		pages: []eventbridge.ListRulesOutput{
			{Rules: []ebtypes.Rule{ebRule("io-foo-a", "arn-a")}},
		},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if bus := got[0].Identity.NativeIDs["event_bus_name"]; bus != "default" {
		t.Errorf("event_bus_name=%q, want default (nil EventBusName must default)", bus)
	}
}

// blockingEventBridgeClient mirrors blockingDynamoClient.
type blockingEventBridgeClient struct {
	pages   []eventbridge.ListRulesOutput
	tags    map[string][]ebtypes.Tag
	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int
	starts      chan string

	listIdx int
}

func (c *blockingEventBridgeClient) ListRules(_ context.Context, _ *eventbridge.ListRulesInput, _ ...func(*eventbridge.Options)) (*eventbridge.ListRulesOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &eventbridge.ListRulesOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingEventBridgeClient) DescribeRule(_ context.Context, _ *eventbridge.DescribeRuleInput, _ ...func(*eventbridge.Options)) (*eventbridge.DescribeRuleOutput, error) {
	return nil, errors.New("blockingEventBridgeClient.DescribeRule: not used in concurrency tests")
}

func (c *blockingEventBridgeClient) ListTagsForResource(ctx context.Context, in *eventbridge.ListTagsForResourceInput, _ ...func(*eventbridge.Options)) (*eventbridge.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceARN)
	c.mu.Lock()
	c.inflight++
	if c.inflight > c.maxInflight {
		c.maxInflight = c.inflight
	}
	c.mu.Unlock()
	if c.starts != nil {
		c.starts <- arn
	}
	defer func() {
		c.mu.Lock()
		c.inflight--
		c.mu.Unlock()
	}()
	select {
	case <-c.release:
		return &eventbridge.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCloudWatchEventRuleDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	rules := make([]ebtypes.Rule, total)
	tags := make(map[string][]ebtypes.Tag, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn-%d", i)
		rules[i] = ebRule(name, arn)
		tags[arn] = []ebtypes.Tag{ebTagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingEventBridgeClient{
		pages:   []eventbridge.ListRulesOutput{{Rules: rules}},
		tags:    tags,
		release: release,
	}
	d := &cloudwatchEventRuleDiscoverer{
		new:            func(_ string) cloudwatchEventRuleClient { return bc },
		maxConcurrency: limit,
	}
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

func TestCloudWatchEventRuleDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeEventBridgeClient{
		"us-east-1": {pages: []eventbridge.ListRulesOutput{{Rules: []ebtypes.Rule{ebRule("io-foo-east", "arn-east")}}}},
		"eu-west-1": {pages: []eventbridge.ListRulesOutput{{Rules: []ebtypes.Rule{ebRule("io-foo-west", "arn-west")}}}},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(region string) cloudwatchEventRuleClient { return fakes[region] }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123", Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != "cloudwatch_event_rule" {
				t.Errorf("service_start.service=%q", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			finishes[e.Region]++
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if starts[region] != 1 || finishes[region] != 1 {
			t.Errorf("region=%s: starts=%d finishes=%d, want 1/1", region, starts[region], finishes[region])
		}
	}
}

func TestCloudWatchEventRuleDiscover_EmitsItemFound_PerRule(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		pages: []eventbridge.ListRulesOutput{{Rules: []ebtypes.Rule{
			ebRule("io-foo-a", "arn-a"),
			ebRule("io-foo-b", "arn-b"),
		}}},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123", Emitter: rec,
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
		if it.Service != "cloudwatch_event_rule" {
			t.Errorf("item.service=%q", it.Service)
		}
		if it.TFType != "aws_cloudwatch_event_rule" {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
	}
}

func TestCloudWatchEventRuleDiscoverByID_AcceptsBareName(t *testing.T) {
	t.Parallel()
	fake := &fakeEventBridgeClient{
		describeByName: map[string]*eventbridge.DescribeRuleOutput{
			"io-foo-orders": {
				Name: aws.String("io-foo-orders"),
				Arn:  aws.String("arn:aws:events:us-east-1:123:rule/io-foo-orders"),
			},
		},
	}
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "io-foo-orders", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_cloudwatch_event_rule" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-orders" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] == "" {
		t.Error("arn missing")
	}
	if got.Identity.NativeIDs["event_bus_name"] != "default" {
		t.Errorf("event_bus_name=%q, want default", got.Identity.NativeIDs["event_bus_name"])
	}
}

func TestCloudWatchEventRuleDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return &fakeEventBridgeClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestCloudWatchEventRuleDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &cloudwatchEventRuleDiscoverer{new: func(_ string) cloudwatchEventRuleClient { return &fakeEventBridgeClient{} }}
	cases := []string{
		"",
		"arn:aws:events:us-east-1:123:rule/io-foo", // ARN not accepted
		"weird name with spaces",
		"a/b/c", // multi-segment path
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
