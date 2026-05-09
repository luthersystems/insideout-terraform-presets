package awsdiscover

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
)

var errSeedListIndexes = errors.New("AccessDenied")

type fakeRE2IndexClient struct {
	pages    []resourceexplorer2.ListIndexesOutput
	listErr  error
	tagsByID map[string]map[string]string
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []resourceexplorer2.ListIndexesInput
	tagCalls  []string

	// GetIndex wiring.
	getOut             *resourceexplorer2.GetIndexOutput
	getErr             error
	getCalls           int
	getReturnsNotFound bool
}

func (f *fakeRE2IndexClient) ListIndexes(_ context.Context, in *resourceexplorer2.ListIndexesInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListIndexesOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &resourceexplorer2.ListIndexesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeRE2IndexClient) GetIndex(_ context.Context, _ *resourceexplorer2.GetIndexInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.GetIndexOutput, error) {
	f.mu.Lock()
	f.getCalls++
	f.mu.Unlock()
	if f.getReturnsNotFound {
		return nil, &re2types.ResourceNotFoundException{}
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getOut != nil {
		return f.getOut, nil
	}
	return nil, &re2types.ResourceNotFoundException{}
}

func (f *fakeRE2IndexClient) ListTagsForResource(_ context.Context, in *resourceexplorer2.ListTagsForResourceInput, _ ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &resourceexplorer2.ListTagsForResourceOutput{Tags: f.tagsByID[arn]}, nil
}

func re2Index(arn, region string, t re2types.IndexType) re2types.Index {
	return re2types.Index{Arn: aws.String(arn), Region: aws.String(region), Type: t}
}

// TestResourceExplorer2IndexDiscover_AdminPathNoFilter pins the rule
// documented at the top of resourceexplorer2_index.go: indexes are
// account-level setup primitives, NOT project-tagged. A non-empty
// args.Project must NOT filter them out.
func TestResourceExplorer2IndexDiscover_AdminPathNoFilter(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:us-east-1:123:index/abc"
	fake := &fakeRE2IndexClient{
		pages: []resourceexplorer2.ListIndexesOutput{
			{Indexes: []re2types.Index{re2Index(arn, "us-east-1", re2types.IndexTypeLocal)}},
		},
		tagsByID: map[string]map[string]string{arn: {"some": "tag"}},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	// Project is set but the index does not begin with that prefix —
	// must still come back.
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (no project filter)", len(got))
	}
	if got[0].Identity.ImportID != arn {
		t.Errorf("ImportID=%q, want %q", got[0].Identity.ImportID, arn)
	}
	if got[0].Identity.NameHint == "" {
		t.Error("NameHint empty (last ARN segment)")
	}
}

func TestResourceExplorer2IndexDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	a1 := "arn:aws:resource-explorer-2:us-east-1:123:index/idx-1"
	a2 := "arn:aws:resource-explorer-2:us-east-1:123:index/idx-2"
	fake := &fakeRE2IndexClient{
		pages: []resourceexplorer2.ListIndexesOutput{
			{Indexes: []re2types.Index{re2Index(a1, "us-east-1", re2types.IndexTypeLocal)}, NextToken: aws.String("nt1")},
			{Indexes: []re2types.Index{re2Index(a2, "us-east-1", re2types.IndexTypeLocal)}},
		},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if len(fake.listCalls) != 2 {
		t.Errorf("ListIndexes called %d, want 2", len(fake.listCalls))
	}
	if aws.ToString(fake.listCalls[1].NextToken) != "nt1" {
		t.Errorf("second call NextToken=%q, want nt1", aws.ToString(fake.listCalls[1].NextToken))
	}
}

func TestResourceExplorer2IndexDiscover_PropagatesListIndexesError(t *testing.T) {
	t.Parallel()
	fake := &fakeRE2IndexClient{listErr: errSeedListIndexes}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	_, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errSeedListIndexes) {
		t.Errorf("err=%v, want errors.Is(err, errSeedListIndexes)", err)
	}
}

func TestResourceExplorer2IndexDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	good := "arn:aws:resource-explorer-2:us-east-1:123:index/good"
	bad := "arn:aws:resource-explorer-2:us-east-1:123:index/bad"
	fake := &fakeRE2IndexClient{
		pages: []resourceexplorer2.ListIndexesOutput{{
			Indexes: []re2types.Index{
				re2Index(good, "us-east-1", re2types.IndexTypeLocal),
				re2Index(bad, "us-east-1", re2types.IndexTypeLocal),
			},
		}},
		tagsByID: map[string]map[string]string{good: {}},
		tagsErr:  map[string]error{bad: errors.New("Throttling")},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (throttled skipped)", len(got))
	}
	if got[0].Identity.NativeIDs["arn"] != good {
		t.Errorf("kept the wrong arn: %q", got[0].Identity.NativeIDs["arn"])
	}
}

func TestResourceExplorer2IndexDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	a1 := "arn:aws:resource-explorer-2:us-east-1:123:index/yes"
	a2 := "arn:aws:resource-explorer-2:us-east-1:123:index/no"
	fake := &fakeRE2IndexClient{
		pages: []resourceexplorer2.ListIndexesOutput{{
			Indexes: []re2types.Index{
				re2Index(a1, "us-east-1", re2types.IndexTypeLocal),
				re2Index(a2, "us-east-1", re2types.IndexTypeLocal),
			},
		}},
		tagsByID: map[string]map[string]string{
			a1: {"team": "growth"},
			a2: {"team": "infra"},
		},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:      []string{"us-east-1"},
		AccountID:    "123",
		TagSelectors: []TagSelector{{Key: "team", Value: "growth"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (tag selector filtered)", len(got))
	}
	if got[0].Identity.ImportID != a1 {
		t.Errorf("ImportID=%q, want %q", got[0].Identity.ImportID, a1)
	}
}

func TestResourceExplorer2IndexDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	a1 := "arn:aws:resource-explorer-2:us-east-1:123:index/east"
	a2 := "arn:aws:resource-explorer-2:eu-west-1:123:index/west"
	fakes := map[string]*fakeRE2IndexClient{
		"us-east-1": {pages: []resourceexplorer2.ListIndexesOutput{{Indexes: []re2types.Index{re2Index(a1, "us-east-1", re2types.IndexTypeLocal)}}}},
		"eu-west-1": {pages: []resourceexplorer2.ListIndexesOutput{{Indexes: []re2types.Index{re2Index(a2, "eu-west-1", re2types.IndexTypeAggregator)}}}},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(region string) resourceExplorer2IndexClient { return fakes[region] }, maxConcurrency: 4}
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
			if e.Service != "resourceexplorer2_index" {
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

func TestResourceExplorer2IndexDiscover_EmitsItemFound_PerIndex(t *testing.T) {
	t.Parallel()
	a := "arn:aws:resource-explorer-2:us-east-1:123:index/a"
	b := "arn:aws:resource-explorer-2:us-east-1:123:index/b"
	fake := &fakeRE2IndexClient{
		pages: []resourceexplorer2.ListIndexesOutput{{
			Indexes: []re2types.Index{
				re2Index(a, "us-east-1", re2types.IndexTypeLocal),
				re2Index(b, "us-east-1", re2types.IndexTypeLocal),
			},
		}},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }, maxConcurrency: 4}
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
		if it.TFType != "aws_resourceexplorer2_index" {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
	}
}

func TestResourceExplorer2IndexDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:us-east-1:123:index/abc"
	fake := &fakeRE2IndexClient{
		getOut: &resourceexplorer2.GetIndexOutput{Arn: aws.String(arn), Type: re2types.IndexTypeLocal},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_resourceexplorer2_index" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != arn {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint == "" {
		t.Error("NameHint empty")
	}
}

func TestResourceExplorer2IndexDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:us-east-1:123:index/abc"
	fake := &fakeRE2IndexClient{getReturnsNotFound: true}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	_, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestResourceExplorer2IndexDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return &fakeRE2IndexClient{} }}
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
