package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// errDBParameterGroupSeed is the package-level sentinel for
// db_parameter_group error propagation.
var errDBParameterGroupSeed = errors.New("AccessDenied")

type fakeDBParameterGroupClient struct {
	pages    []rds.DescribeDBParameterGroupsOutput
	tagsByID map[string][]rdstypes.Tag
	tagsErr  map[string]error
	err      error

	mu       sync.Mutex
	calls    []rds.DescribeDBParameterGroupsInput
	tagCalls []string
}

func (f *fakeDBParameterGroupClient) DescribeDBParameterGroups(_ context.Context, in *rds.DescribeDBParameterGroupsInput, _ ...func(*rds.Options)) (*rds.DescribeDBParameterGroupsOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, *in)
	idx := len(f.calls) - 1
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if idx >= len(f.pages) {
		return &rds.DescribeDBParameterGroupsOutput{}, nil
	}
	out := f.pages[idx]
	return &out, nil
}

func (f *fakeDBParameterGroupClient) ListTagsForResource(_ context.Context, in *rds.ListTagsForResourceInput, _ ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceName)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &rds.ListTagsForResourceOutput{TagList: f.tagsByID[arn]}, nil
}

func dbParameterGroupFixture(name, arn, family string) rdstypes.DBParameterGroup {
	return rdstypes.DBParameterGroup{
		DBParameterGroupName:   aws.String(name),
		DBParameterGroupArn:    aws.String(arn),
		DBParameterGroupFamily: aws.String(family),
	}
}

func TestDBParameterGroupDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{
				pages: []rds.DescribeDBParameterGroupsOutput{
					{DBParameterGroups: []rdstypes.DBParameterGroup{
						dbParameterGroupFixture("io-foo-rds0-pg", "arn:aws:rds:us-east-1:123:pg:io-foo-rds0-pg", "postgres15"),
						dbParameterGroupFixture("io-foo-rds1-pg", "arn:aws:rds:us-east-1:123:pg:io-foo-rds1-pg", "postgres15"),
					}},
				},
				tagsByID: map[string][]rdstypes.Tag{
					"arn:aws:rds:us-east-1:123:pg:io-foo-rds0-pg": {rdsTag("Project", "io-foo")},
					"arn:aws:rds:us-east-1:123:pg:io-foo-rds1-pg": {rdsTag("Project", "io-foo")},
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
		if ir.Identity.Type != dbParameterGroupTFType {
			t.Errorf("Type=%q, want %q", ir.Identity.Type, dbParameterGroupTFType)
		}
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Error("NativeIDs[arn] empty")
		}
		if ir.Identity.NativeIDs["family"] != "postgres15" {
			t.Errorf("family=%q, want postgres15", ir.Identity.NativeIDs["family"])
		}
	}
	if got[0].Identity.ImportID != "io-foo-rds0-pg" {
		t.Errorf("first ImportID=%q, want io-foo-rds0-pg (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestDBParameterGroupDiscover_PaginatesUntilNoMarker(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{
				pages: []rds.DescribeDBParameterGroupsOutput{
					{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-a-pg", "arn-a", "postgres15")}, Marker: aws.String("m1")},
					{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-b-pg", "arn-b", "postgres15")}, Marker: aws.String("m2")},
					{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-c-pg", "arn-c", "postgres15")}}, // terminal
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
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
}

func TestDBParameterGroupDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeDBParameterGroupClient{
		pages: []rds.DescribeDBParameterGroupsOutput{
			{DBParameterGroups: []rdstypes.DBParameterGroup{
				dbParameterGroupFixture("io-foo-a-pg", "arn-a", "postgres15"),
				dbParameterGroupFixture("other-pg", "arn-b", "postgres15"),
				dbParameterGroupFixture("io-foo-c-pg", "arn-c", "postgres15"),
			}},
		},
	}
	d := &dbParameterGroupDiscoverer{new: func(_ string) dbParameterGroupClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix filter)", len(got))
	}
	for _, c := range fake.tagCalls {
		if c == "arn-b" {
			t.Errorf("unexpected ListTagsForResource on non-prefix-matching arn-b")
		}
	}
}

func TestDBParameterGroupDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{err: errDBParameterGroupSeed}
		},
		maxConcurrency: 4,
	}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errDBParameterGroupSeed) {
		t.Errorf("err=%v, want errors.Is(err, errDBParameterGroupSeed)", err)
	}
}

func TestDBParameterGroupDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{
				pages: []rds.DescribeDBParameterGroupsOutput{
					{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-rds0-pg", "arn:aws:rds:us-east-1:123:pg:io-foo-rds0-pg", "postgres15")}},
				},
			}
		},
		maxConcurrency: 4,
	}
	got, err := d.DiscoverByID(context.Background(), "io-foo-rds0-pg", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != dbParameterGroupTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "io-foo-rds0-pg" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["family"] != "postgres15" {
		t.Errorf("family=%q", got.Identity.NativeIDs["family"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestDBParameterGroupDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{err: &rdstypes.DBParameterGroupNotFoundFault{Message: aws.String("not found")}}
		},
		maxConcurrency: 4,
	}
	_, err := d.DiscoverByID(context.Background(), "io-foo-missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestDBParameterGroupDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDBParameterGroupClient{
		"us-east-1": {pages: []rds.DescribeDBParameterGroupsOutput{
			{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-east", "arn-east", "postgres15")}},
		}},
		"eu-west-1": {pages: []rds.DescribeDBParameterGroupsOutput{
			{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-west", "arn-west", "postgres15")}},
		}},
	}
	var seenRegions []string
	d := &dbParameterGroupDiscoverer{
		new: func(region string) dbParameterGroupClient {
			seenRegions = append(seenRegions, region)
			return fakes[region]
		},
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 {
		t.Errorf("region closure invocations=%v, want 2", seenRegions)
	}
	if len(fakes["us-east-1"].calls) == 0 || len(fakes["eu-west-1"].calls) == 0 {
		t.Error("expected one DescribeDBParameterGroups call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestDBParameterGroupDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{
				pages: []rds.DescribeDBParameterGroupsOutput{
					{DBParameterGroups: []rdstypes.DBParameterGroup{
						dbParameterGroupFixture("io-foo-prod-pg", "arn-prod", "postgres15"),
						dbParameterGroupFixture("io-foo-stag-pg", "arn-stag", "postgres15"),
					}},
				},
				tagsByID: map[string][]rdstypes.Tag{
					"arn-prod": {rdsTag("env", "prod")},
					"arn-stag": {rdsTag("env", "staging")},
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
		t.Fatalf("len=%d, want 1 (env=prod only)", len(got))
	}
	if got[0].Identity.ImportID != "io-foo-prod-pg" {
		t.Errorf("ImportID=%q, want io-foo-prod-pg", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

func TestDBParameterGroupDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDBParameterGroupClient{
		"us-east-1": {pages: []rds.DescribeDBParameterGroupsOutput{
			{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-east-pg", "arn-east", "postgres15")}},
		}},
		"eu-west-1": {pages: []rds.DescribeDBParameterGroupsOutput{
			{DBParameterGroups: []rdstypes.DBParameterGroup{dbParameterGroupFixture("io-foo-west-pg", "arn-west", "postgres15")}},
		}},
	}
	d := &dbParameterGroupDiscoverer{new: func(region string) dbParameterGroupClient { return fakes[region] }, maxConcurrency: 4}
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
			if e.Service != dbParameterGroupSlug {
				t.Errorf("service_start.service=%q, want %s", e.Service, dbParameterGroupSlug)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != dbParameterGroupSlug {
				t.Errorf("service_finish.service=%q, want %s", e.Service, dbParameterGroupSlug)
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

func TestDBParameterGroupDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new: func(_ string) dbParameterGroupClient {
			return &fakeDBParameterGroupClient{
				pages: []rds.DescribeDBParameterGroupsOutput{
					{DBParameterGroups: []rdstypes.DBParameterGroup{
						dbParameterGroupFixture("io-foo-a-pg", "arn-a", "postgres15"),
						dbParameterGroupFixture("io-foo-b-pg", "arn-b", "postgres15"),
					}},
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
		if it.Service != dbParameterGroupSlug {
			t.Errorf("item.service=%q, want %s", it.Service, dbParameterGroupSlug)
		}
		if it.TFType != dbParameterGroupTFType {
			t.Errorf("item.tf_type=%q, want %s", it.TFType, dbParameterGroupTFType)
		}
	}
}

func TestDBParameterGroupDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &dbParameterGroupDiscoverer{
		new:            func(_ string) dbParameterGroupClient { return &fakeDBParameterGroupClient{} },
		maxConcurrency: 4,
	}
	cases := []string{
		"",
		"   ",
		"name with spaces",
		"name/slash",
		"arn:aws:rds:us-east-1:123:pg:something",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestDBParameterGroupDiscover_SkipsAWSDefaults pins the default.*
// skip-list — AWS-managed default parameter groups (e.g.
// `default.postgres15`) cannot be imported, so they are dropped
// before tag-fetch fan-out and never emitted. The test also asserts
// no ListTagsForResource is issued against the default rows so we
// don't pay the API cost on tombstone candidates.
func TestDBParameterGroupDiscover_SkipsAWSDefaults(t *testing.T) {
	t.Parallel()
	fake := &fakeDBParameterGroupClient{
		pages: []rds.DescribeDBParameterGroupsOutput{
			{DBParameterGroups: []rdstypes.DBParameterGroup{
				dbParameterGroupFixture("default.postgres15", "arn:default-pg15", "postgres15"),
				dbParameterGroupFixture("default.aurora-postgresql15", "arn:default-aurora-pg15", "aurora-postgresql15"),
				dbParameterGroupFixture("io-foo-rds0-pg", "arn:io-foo-rds0-pg", "postgres15"),
			}},
		},
		tagsByID: map[string][]rdstypes.Tag{
			"arn:io-foo-rds0-pg": {rdsTag("Project", "io-foo")},
		},
	}
	d := &dbParameterGroupDiscoverer{new: func(_ string) dbParameterGroupClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only io-foo-rds0-pg should pass)", len(got))
	}
	if got[0].Identity.ImportID != "io-foo-rds0-pg" {
		t.Errorf("ImportID=%q, want io-foo-rds0-pg", got[0].Identity.ImportID)
	}
	for _, c := range fake.tagCalls {
		if c == "arn:default-pg15" || c == "arn:default-aurora-pg15" {
			t.Errorf("unexpected ListTagsForResource on default.* row: %s", c)
		}
	}
}

// blockingDBParameterGroupClient signals when each
// ListTagsForResource call enters and blocks until release is
// closed (or ctx is cancelled). Used by the bounded-concurrency test.
type blockingDBParameterGroupClient struct {
	pages   []rds.DescribeDBParameterGroupsOutput
	tags    map[string][]rdstypes.Tag
	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int

	listIdx int
}

func (c *blockingDBParameterGroupClient) DescribeDBParameterGroups(_ context.Context, _ *rds.DescribeDBParameterGroupsInput, _ ...func(*rds.Options)) (*rds.DescribeDBParameterGroupsOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &rds.DescribeDBParameterGroupsOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingDBParameterGroupClient) ListTagsForResource(ctx context.Context, in *rds.ListTagsForResourceInput, _ ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceName)
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
		return &rds.ListTagsForResourceOutput{TagList: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDBParameterGroupDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	groups := make([]rdstypes.DBParameterGroup, 0, total)
	tags := make(map[string][]rdstypes.Tag, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%02d-pg", i)
		arn := "arn-" + name
		groups = append(groups, dbParameterGroupFixture(name, arn, "postgres15"))
		tags[arn] = []rdstypes.Tag{rdsTag("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingDBParameterGroupClient{
		pages:   []rds.DescribeDBParameterGroupsOutput{{DBParameterGroups: groups}},
		tags:    tags,
		release: release,
	}
	d := &dbParameterGroupDiscoverer{
		new:            func(_ string) dbParameterGroupClient { return bc },
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
