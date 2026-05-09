package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
)

// errResourceExplorer2ViewSeed is the package-level sentinel for
// ListViews error-propagation assertions (canonical err<Service>Seed
// naming).
var errResourceExplorer2ViewSeed = errors.New("AccessDenied")

type fakeRE2ViewClient struct {
	pages    []resourceexplorer2.ListViewsOutput
	listErr  error
	tagsByID map[string]map[string]string
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []resourceexplorer2.ListViewsInput
	tagCalls  []string

	getByARN           map[string]*re2types.View
	getErr             error
	getCalls           []string
	getReturnsNotFound bool
}

func (f *fakeRE2ViewClient) ListViews(_ context.Context, in *resourceexplorer2.ListViewsInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListViewsOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &resourceexplorer2.ListViewsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeRE2ViewClient) GetView(_ context.Context, in *resourceexplorer2.GetViewInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.GetViewOutput, error) {
	arn := aws.ToString(in.ViewArn)
	f.mu.Lock()
	f.getCalls = append(f.getCalls, arn)
	f.mu.Unlock()
	if f.getReturnsNotFound {
		return nil, &re2types.ResourceNotFoundException{}
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	if v, ok := f.getByARN[arn]; ok {
		return &resourceexplorer2.GetViewOutput{View: v}, nil
	}
	return nil, &re2types.ResourceNotFoundException{}
}

func (f *fakeRE2ViewClient) ListTagsForResource(_ context.Context, in *resourceexplorer2.ListTagsForResourceInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &resourceexplorer2.ListTagsForResourceOutput{Tags: f.tagsByID[arn]}, nil
}

func TestResourceExplorer2ViewDiscover_NoFilterAndParsesARN(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:view/team-search/abc-uuid"
	fake := &fakeRE2ViewClient{
		pages:    []resourceexplorer2.ListViewsOutput{{Views: []string{a}}},
		tagsByID: map[string]map[string]string{a: {}},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	got0 := got[0].Identity
	if got0.ImportID != a {
		t.Errorf("ImportID=%q, want %q", got0.ImportID, a)
	}
	if got0.NativeIDs["region"] != "us-east-1" {
		t.Errorf("NativeIDs[region]=%q, want us-east-1 (parsed from ARN)", got0.NativeIDs["region"])
	}
	if got0.NativeIDs["name"] != "team-search" {
		t.Errorf("NativeIDs[name]=%q, want team-search (parsed from ARN)", got0.NativeIDs["name"])
	}
	// NameHint should prefer the parsed view name over the unstable
	// trailing UUID segment (#335 P1-IMPL-3).
	if got0.NameHint != "team-search" {
		t.Errorf("NameHint=%q, want team-search (parsed view name, not UUID)", got0.NameHint)
	}
}

func TestResourceExplorer2ViewDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:view/a/uuid-1"
	b := "arn:aws:resource-explorer-2:us-east-1:123:view/b/uuid-2"
	c := "arn:aws:resource-explorer-2:us-east-1:123:view/c/uuid-3"
	fake := &fakeRE2ViewClient{
		pages: []resourceexplorer2.ListViewsOutput{
			{Views: []string{a}, NextToken: aws.String("nt1")},
			{Views: []string{b}, NextToken: aws.String("nt2")},
			{Views: []string{c}},
		},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	if len(fake.listCalls) < 3 {
		t.Errorf("ListViews called %d, want >=3", len(fake.listCalls))
	}
	if aws.ToString(fake.listCalls[1].NextToken) != "nt1" {
		t.Errorf("call[1].NextToken=%q, want nt1", aws.ToString(fake.listCalls[1].NextToken))
	}
	if aws.ToString(fake.listCalls[2].NextToken) != "nt2" {
		t.Errorf("call[2].NextToken=%q, want nt2", aws.ToString(fake.listCalls[2].NextToken))
	}
}

func TestResourceExplorer2ViewDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	fake := &fakeRE2ViewClient{listErr: errResourceExplorer2ViewSeed}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	_, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errResourceExplorer2ViewSeed) {
		t.Errorf("err=%v, want errors.Is(err, errResourceExplorer2ViewSeed)", err)
	}
}

func TestResourceExplorer2ViewDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	good := "arn:aws:resource-explorer-2:us-east-1:123:view/g/uuid-g"
	bad := "arn:aws:resource-explorer-2:us-east-1:123:view/b/uuid-b"
	fake := &fakeRE2ViewClient{
		pages:    []resourceexplorer2.ListViewsOutput{{Views: []string{good, bad}}},
		tagsByID: map[string]map[string]string{good: {}},
		tagsErr:  map[string]error{bad: errors.New("Throttling")},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.ImportID != good {
		t.Errorf("kept the wrong arn: %q", got[0].Identity.ImportID)
	}
}

func TestResourceExplorer2ViewDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:view/yes/uuid-y"
	b := "arn:aws:resource-explorer-2:us-east-1:123:view/no/uuid-n"
	fake := &fakeRE2ViewClient{
		pages: []resourceexplorer2.ListViewsOutput{{Views: []string{a, b}}},
		tagsByID: map[string]map[string]string{
			a: {"team": "growth"},
			b: {"team": "infra"},
		},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions: []string{"us-east-1"}, AccountID: "123",
		TagSelectors: []TagSelector{{Key: "team", Value: "growth"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.ImportID != a {
		t.Errorf("kept wrong view: %q", got[0].Identity.ImportID)
	}
}

func TestResourceExplorer2ViewDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:view/east/uuid-e"
	b := "arn:aws:resource-explorer-2:eu-west-1:123:view/west/uuid-w"
	fakes := map[string]*fakeRE2ViewClient{
		"us-east-1": {pages: []resourceexplorer2.ListViewsOutput{{Views: []string{a}}}},
		"eu-west-1": {pages: []resourceexplorer2.ListViewsOutput{{Views: []string{b}}}},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(region string) resourceExplorer2ViewClient { return fakes[region] }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123", Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != "resourceexplorer2_view" {
				t.Errorf("service_start.service=%q", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			finishes[e.Region]++
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if starts[region] != 1 || finishes[region] != 1 {
			t.Errorf("region=%s: starts=%d finishes=%d", region, starts[region], finishes[region])
		}
	}
}

func TestResourceExplorer2ViewDiscover_EmitsItemFound_PerView(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:view/a/uuid-a"
	b := "arn:aws:resource-explorer-2:us-east-1:123:view/b/uuid-b"
	fake := &fakeRE2ViewClient{pages: []resourceexplorer2.ListViewsOutput{{Views: []string{a, b}}}}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123", Emitter: rec})
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
		if it.TFType != "aws_resourceexplorer2_view" {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
	}
}

func TestResourceExplorer2ViewDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:us-east-1:123:view/abc/uuid"
	fake := &fakeRE2ViewClient{
		getByARN: map[string]*re2types.View{arn: {ViewArn: aws.String(arn)}},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_resourceexplorer2_view" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != arn {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestResourceExplorer2ViewDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:us-east-1:123:view/abc/uuid"
	fake := &fakeRE2ViewClient{getReturnsNotFound: true}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	_, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestResourceExplorer2ViewDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return &fakeRE2ViewClient{} }}
	cases := []string{
		"",
		"not-an-arn",
		"arn:aws:s3:::bucket",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestResourceExplorer2ViewDiscover_MultiRegionTriggersOneSDKCallPerRegion
// pins the per-region loop. See sqs_test.go for the canonical contract.
func TestResourceExplorer2ViewDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:view/east/uuid-e"
	b := "arn:aws:resource-explorer-2:eu-west-1:123:view/west/uuid-w"
	fakes := map[string]*fakeRE2ViewClient{
		"us-east-1": {pages: []resourceexplorer2.ListViewsOutput{{Views: []string{a}}}},
		"eu-west-1": {pages: []resourceexplorer2.ListViewsOutput{{Views: []string{b}}}},
	}
	var seenRegions []string
	d := &resourceExplorer2ViewDiscoverer{new: func(region string) resourceExplorer2ViewClient {
		seenRegions = append(seenRegions, region)
		f, ok := fakes[region]
		if !ok {
			t.Fatalf("closure called with unexpected region %q", region)
		}
		return f
	}, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seenRegions)
	}
	if len(fakes["us-east-1"].listCalls) == 0 {
		t.Error("us-east-1 fake never received ListViews")
	}
	if len(fakes["eu-west-1"].listCalls) == 0 {
		t.Error("eu-west-1 fake never received ListViews")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
}

// TestResourceExplorer2ViewDiscover_SkipsCrossRegionARNs pins the
// fix for issue #336: ListViews returns ARNs from every region in
// the account regardless of the SDK client's region. Per-region
// clients can't tag-fetch foreign ARNs (BadRequestException), and
// emitting them would produce duplicates because addressBook keys
// on the outer-loop region. ARNs whose ARN-region != outer-loop
// region must be dropped before the tag-fetch and before emission.
func TestResourceExplorer2ViewDiscover_SkipsCrossRegionARNs(t *testing.T) {
	t.Parallel()
	const homeARN = "arn:aws:resource-explorer-2:us-east-1:123:view/home/home-uuid"
	const foreignARN = "arn:aws:resource-explorer-2:eu-central-1:123:view/foreign/foreign-uuid"
	fake := &fakeRE2ViewClient{
		pages: []resourceexplorer2.ListViewsOutput{{
			Views: []string{homeARN, foreignARN},
		}},
		tagsByID: map[string]map[string]string{
			homeARN:    {},
			foreignARN: {},
		},
	}
	d := &resourceExplorer2ViewDiscoverer{
		new:            func(_ string) resourceExplorer2ViewClient { return fake },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Tag-fetch must happen for the home-region ARN only.
	if len(fake.tagCalls) != 1 {
		t.Fatalf("ListTagsForResource called %d times, want 1 (cross-region must be skipped): %v", len(fake.tagCalls), fake.tagCalls)
	}
	if fake.tagCalls[0] != homeARN {
		t.Errorf("tag-fetch ARN=%q, want %q", fake.tagCalls[0], homeARN)
	}
	// Emission must include the home-region ARN only.
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (cross-region must not be emitted)", len(got))
	}
	if got[0].Identity.ImportID != homeARN {
		t.Errorf("ImportID=%q, want %q", got[0].Identity.ImportID, homeARN)
	}
}

// blockingRE2ViewClient mirrors blockingDynamoClient — used for the
// bounded-concurrency test below.
type blockingRE2ViewClient struct {
	pages []resourceexplorer2.ListViewsOutput
	tags  map[string]map[string]string

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int

	listIdx int
}

func (c *blockingRE2ViewClient) ListViews(_ context.Context, _ *resourceexplorer2.ListViewsInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListViewsOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &resourceexplorer2.ListViewsOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingRE2ViewClient) GetView(_ context.Context, _ *resourceexplorer2.GetViewInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.GetViewOutput, error) {
	return nil, errors.New("blockingRE2ViewClient.GetView: unused")
}

func (c *blockingRE2ViewClient) ListTagsForResource(ctx context.Context, in *resourceexplorer2.ListTagsForResourceInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
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
		return &resourceexplorer2.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestResourceExplorer2ViewDiscover_BoundedConcurrency mirrors the
// dynamodb canonical: per-item tag fetches must respect the limit.
func TestResourceExplorer2ViewDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	views := make([]string, total)
	tags := make(map[string]map[string]string, total)
	for i := 0; i < total; i++ {
		arn := fmt.Sprintf("arn:aws:resource-explorer-2:us-east-1:123:view/v-%d/uuid-%d", i, i)
		views[i] = arn
		tags[arn] = map[string]string{}
	}
	release := make(chan struct{})
	bc := &blockingRE2ViewClient{
		pages:   []resourceexplorer2.ListViewsOutput{{Views: views}},
		tags:    tags,
		release: release,
	}
	d := &resourceExplorer2ViewDiscoverer{
		new:            func(_ string) resourceExplorer2ViewClient { return bc },
		maxConcurrency: limit,
	}
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
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
