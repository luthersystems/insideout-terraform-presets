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

// errDBSubnetGroupSeed is the package-level sentinel for db_subnet_group
// error propagation.
var errDBSubnetGroupSeed = errors.New("AccessDenied")

type fakeDBSubnetGroupClient struct {
	pages    []rds.DescribeDBSubnetGroupsOutput
	tagsByID map[string][]rdstypes.Tag
	tagsErr  map[string]error
	err      error

	mu       sync.Mutex
	calls    []rds.DescribeDBSubnetGroupsInput
	tagCalls []string
}

func (f *fakeDBSubnetGroupClient) DescribeDBSubnetGroups(_ context.Context, in *rds.DescribeDBSubnetGroupsInput, _ ...func(*rds.Options)) (*rds.DescribeDBSubnetGroupsOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, *in)
	idx := len(f.calls) - 1
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if idx >= len(f.pages) {
		return &rds.DescribeDBSubnetGroupsOutput{}, nil
	}
	out := f.pages[idx]
	return &out, nil
}

func (f *fakeDBSubnetGroupClient) ListTagsForResource(_ context.Context, in *rds.ListTagsForResourceInput, _ ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceName)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &rds.ListTagsForResourceOutput{TagList: f.tagsByID[arn]}, nil
}

func dbSubnetGroupFixture(name, arn, vpcID string) rdstypes.DBSubnetGroup {
	return rdstypes.DBSubnetGroup{
		DBSubnetGroupName: aws.String(name),
		DBSubnetGroupArn:  aws.String(arn),
		VpcId:             aws.String(vpcID),
	}
}

func TestDBSubnetGroupDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new: func(_ string) dbSubnetGroupClient {
			return &fakeDBSubnetGroupClient{
				pages: []rds.DescribeDBSubnetGroupsOutput{
					{DBSubnetGroups: []rdstypes.DBSubnetGroup{
						dbSubnetGroupFixture("io-foo-rds0-subnets", "arn:aws:rds:us-east-1:123:subgrp:io-foo-rds0-subnets", "vpc-abc"),
						dbSubnetGroupFixture("io-foo-rds1-subnets", "arn:aws:rds:us-east-1:123:subgrp:io-foo-rds1-subnets", "vpc-abc"),
					}},
				},
				tagsByID: map[string][]rdstypes.Tag{
					"arn:aws:rds:us-east-1:123:subgrp:io-foo-rds0-subnets": {rdsTag("Project", "io-foo")},
					"arn:aws:rds:us-east-1:123:subgrp:io-foo-rds1-subnets": {rdsTag("Project", "io-foo")},
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
		if ir.Identity.Type != dbSubnetGroupTFType {
			t.Errorf("Type=%q, want %q", ir.Identity.Type, dbSubnetGroupTFType)
		}
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Error("NativeIDs[arn] empty")
		}
		if ir.Identity.NativeIDs["vpc_id"] != "vpc-abc" {
			t.Errorf("vpc_id=%q", ir.Identity.NativeIDs["vpc_id"])
		}
	}
	// Sorted by name.
	if got[0].Identity.ImportID != "io-foo-rds0-subnets" {
		t.Errorf("first ImportID=%q, want io-foo-rds0-subnets (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestDBSubnetGroupDiscover_PaginatesUntilNoMarker(t *testing.T) {
	t.Parallel()
	fake := &fakeDBSubnetGroupClient{
		pages: []rds.DescribeDBSubnetGroupsOutput{
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-a", "arn-a", "vpc-1")}, Marker: aws.String("m1")},
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-b", "arn-b", "vpc-1")}, Marker: aws.String("m2")},
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-c", "arn-c", "vpc-1")}}, // terminal
		},
	}
	d := &dbSubnetGroupDiscoverer{
		new:            func(_ string) dbSubnetGroupClient { return fake },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.calls) < 3 {
		t.Fatalf("DescribeDBSubnetGroups calls=%d, want >=3", len(fake.calls))
	}
	if aws.ToString(fake.calls[1].Marker) != "m1" {
		t.Errorf("call[1].Marker=%q, want m1", aws.ToString(fake.calls[1].Marker))
	}
	if aws.ToString(fake.calls[2].Marker) != "m2" {
		t.Errorf("call[2].Marker=%q, want m2", aws.ToString(fake.calls[2].Marker))
	}
}

func TestDBSubnetGroupDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeDBSubnetGroupClient{
		pages: []rds.DescribeDBSubnetGroupsOutput{
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{
				dbSubnetGroupFixture("io-foo-a", "arn-a", "vpc-1"),
				dbSubnetGroupFixture("other-b", "arn-b", "vpc-1"),
				dbSubnetGroupFixture("io-foo-c", "arn-c", "vpc-1"),
			}},
		},
	}
	d := &dbSubnetGroupDiscoverer{new: func(_ string) dbSubnetGroupClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix filter)", len(got))
	}
	// The non-matching entry must NOT have triggered ListTagsForResource —
	// the filter is applied before fan-out.
	for _, c := range fake.tagCalls {
		if c == "arn-b" {
			t.Errorf("unexpected ListTagsForResource on non-prefix-matching arn-b")
		}
	}
}

func TestDBSubnetGroupDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new: func(_ string) dbSubnetGroupClient {
			return &fakeDBSubnetGroupClient{err: errDBSubnetGroupSeed}
		},
		maxConcurrency: 4,
	}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errDBSubnetGroupSeed) {
		t.Errorf("err=%v, want errors.Is(err, errDBSubnetGroupSeed)", err)
	}
}

func TestDBSubnetGroupDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new: func(_ string) dbSubnetGroupClient {
			return &fakeDBSubnetGroupClient{
				pages: []rds.DescribeDBSubnetGroupsOutput{
					{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-rds0-subnets", "arn:aws:rds:us-east-1:123:subgrp:io-foo-rds0-subnets", "vpc-1")}},
				},
			}
		},
		maxConcurrency: 4,
	}
	got, err := d.DiscoverByID(context.Background(), "io-foo-rds0-subnets", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != dbSubnetGroupTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "io-foo-rds0-subnets" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-1" {
		t.Errorf("vpc_id=%q", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestDBSubnetGroupDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new: func(_ string) dbSubnetGroupClient {
			return &fakeDBSubnetGroupClient{err: &rdstypes.DBSubnetGroupNotFoundFault{Message: aws.String("not found")}}
		},
		maxConcurrency: 4,
	}
	_, err := d.DiscoverByID(context.Background(), "io-foo-missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestDBSubnetGroupDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDBSubnetGroupClient{
		"us-east-1": {pages: []rds.DescribeDBSubnetGroupsOutput{
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-east", "arn-east", "vpc-1")}},
		}},
		"eu-west-1": {pages: []rds.DescribeDBSubnetGroupsOutput{
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-west", "arn-west", "vpc-2")}},
		}},
	}
	var seenRegions []string
	d := &dbSubnetGroupDiscoverer{
		new: func(region string) dbSubnetGroupClient {
			seenRegions = append(seenRegions, region)
			return fakes[region]
		},
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations=%v, want [us-east-1 eu-west-1]", seenRegions)
	}
	if len(fakes["us-east-1"].calls) == 0 || len(fakes["eu-west-1"].calls) == 0 {
		t.Error("expected one DescribeDBSubnetGroups call per region")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestDBSubnetGroupDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new: func(_ string) dbSubnetGroupClient {
			return &fakeDBSubnetGroupClient{
				pages: []rds.DescribeDBSubnetGroupsOutput{
					{DBSubnetGroups: []rdstypes.DBSubnetGroup{
						dbSubnetGroupFixture("io-foo-prod", "arn-prod", "vpc-1"),
						dbSubnetGroupFixture("io-foo-stag", "arn-stag", "vpc-1"),
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
	if got[0].Identity.ImportID != "io-foo-prod" {
		t.Errorf("ImportID=%q, want io-foo-prod", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestDBSubnetGroupDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDBSubnetGroupClient{
		"us-east-1": {pages: []rds.DescribeDBSubnetGroupsOutput{
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-east", "arn-east", "vpc-1")}},
		}},
		"eu-west-1": {pages: []rds.DescribeDBSubnetGroupsOutput{
			{DBSubnetGroups: []rdstypes.DBSubnetGroup{dbSubnetGroupFixture("io-foo-west", "arn-west", "vpc-2")}},
		}},
	}
	d := &dbSubnetGroupDiscoverer{new: func(region string) dbSubnetGroupClient { return fakes[region] }, maxConcurrency: 4}
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
			if e.Service != dbSubnetGroupSlug {
				t.Errorf("service_start.service=%q, want %s", e.Service, dbSubnetGroupSlug)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != dbSubnetGroupSlug {
				t.Errorf("service_finish.service=%q, want %s", e.Service, dbSubnetGroupSlug)
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

func TestDBSubnetGroupDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new: func(_ string) dbSubnetGroupClient {
			return &fakeDBSubnetGroupClient{
				pages: []rds.DescribeDBSubnetGroupsOutput{
					{DBSubnetGroups: []rdstypes.DBSubnetGroup{
						dbSubnetGroupFixture("io-foo-a", "arn-a", "vpc-1"),
						dbSubnetGroupFixture("io-foo-b", "arn-b", "vpc-1"),
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
		if it.Service != dbSubnetGroupSlug {
			t.Errorf("item.service=%q, want %s", it.Service, dbSubnetGroupSlug)
		}
		if it.TFType != dbSubnetGroupTFType {
			t.Errorf("item.tf_type=%q, want %s", it.TFType, dbSubnetGroupTFType)
		}
	}
}

func TestDBSubnetGroupDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &dbSubnetGroupDiscoverer{
		new:            func(_ string) dbSubnetGroupClient { return &fakeDBSubnetGroupClient{} },
		maxConcurrency: 4,
	}
	cases := []string{
		"",
		"   ",
		"name with spaces",
		"name/slash",
		"arn:aws:rds:us-east-1:123:subgrp:something",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// blockingDBSubnetGroupClient signals when each ListTagsForResource
// call enters and blocks until release is closed (or ctx is
// cancelled). Used by the bounded-concurrency test.
type blockingDBSubnetGroupClient struct {
	pages   []rds.DescribeDBSubnetGroupsOutput
	tags    map[string][]rdstypes.Tag
	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int

	listIdx int
}

func (c *blockingDBSubnetGroupClient) DescribeDBSubnetGroups(_ context.Context, _ *rds.DescribeDBSubnetGroupsInput, _ ...func(*rds.Options)) (*rds.DescribeDBSubnetGroupsOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &rds.DescribeDBSubnetGroupsOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingDBSubnetGroupClient) ListTagsForResource(ctx context.Context, in *rds.ListTagsForResourceInput, _ ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error) {
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

func TestDBSubnetGroupDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	groups := make([]rdstypes.DBSubnetGroup, 0, total)
	tags := make(map[string][]rdstypes.Tag, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%02d", i)
		arn := "arn-" + name
		groups = append(groups, dbSubnetGroupFixture(name, arn, "vpc-1"))
		tags[arn] = []rdstypes.Tag{rdsTag("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingDBSubnetGroupClient{
		pages:   []rds.DescribeDBSubnetGroupsOutput{{DBSubnetGroups: groups}},
		tags:    tags,
		release: release,
	}
	d := &dbSubnetGroupDiscoverer{
		new:            func(_ string) dbSubnetGroupClient { return bc },
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
