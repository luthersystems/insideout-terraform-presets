package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	aosstypes "github.com/aws/aws-sdk-go-v2/service/opensearchserverless/types"
)

var errOASCollectionSeed = errors.New("AccessDenied")

type fakeOASCollectionClient struct {
	pages    []opensearchserverless.ListCollectionsOutput
	tagsByID map[string][]aosstypes.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []opensearchserverless.ListCollectionsInput
	tagCalls  []string
	listErr   error

	batchByID  map[string]*aosstypes.CollectionDetail
	batchErr   error
	batchCalls []string
}

func (f *fakeOASCollectionClient) ListCollections(_ context.Context, in *opensearchserverless.ListCollectionsInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionsOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &opensearchserverless.ListCollectionsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeOASCollectionClient) ListTagsForResource(_ context.Context, in *opensearchserverless.ListTagsForResourceInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &opensearchserverless.ListTagsForResourceOutput{Tags: f.tagsByID[arn]}, nil
}

func (f *fakeOASCollectionClient) BatchGetCollection(_ context.Context, in *opensearchserverless.BatchGetCollectionInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.BatchGetCollectionOutput, error) {
	f.mu.Lock()
	for _, id := range in.Ids {
		f.batchCalls = append(f.batchCalls, id)
	}
	f.mu.Unlock()
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	out := &opensearchserverless.BatchGetCollectionOutput{}
	for _, id := range in.Ids {
		if d, ok := f.batchByID[id]; ok {
			out.CollectionDetails = append(out.CollectionDetails, *d)
		}
	}
	return out, nil
}

func aossTagPair(k, v string) aosstypes.Tag {
	return aosstypes.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func aossSummary(id, name, arn string) aosstypes.CollectionSummary {
	return aosstypes.CollectionSummary{
		Id:   aws.String(id),
		Name: aws.String(name),
		Arn:  aws.String(arn),
	}
}

func TestOASCollectionDiscover_PrefixThenTagFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeOASCollectionClient{
		pages: []opensearchserverless.ListCollectionsOutput{{
			CollectionSummaries: []aosstypes.CollectionSummary{
				aossSummary("c1", "io-foo-orders", "arn:aws:aoss:us-east-1:123:collection/c1"),
				aossSummary("c2", "io-foo-events", "arn:aws:aoss:us-east-1:123:collection/c2"),
				aossSummary("c3", "other-coll", "arn:aws:aoss:us-east-1:123:collection/c3"),
				aossSummary("c4", "io-foo-untagged", "arn:aws:aoss:us-east-1:123:collection/c4"),
			},
		}},
		tagsByID: map[string][]aosstypes.Tag{
			"arn:aws:aoss:us-east-1:123:collection/c1": {aossTagPair("Project", "io-foo")},
			"arn:aws:aoss:us-east-1:123:collection/c2": {aossTagPair("Project", "io-foo")},
			"arn:aws:aoss:us-east-1:123:collection/c4": {aossTagPair("Owner", "team")},
		},
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix + tag filter)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Error("NativeIDs[arn] empty")
		}
		if ir.Identity.NativeIDs["collection_id"] == "" {
			t.Error("NativeIDs[collection_id] empty")
		}
	}
	// Pin: prefix should gate the tag fan-out.
	if len(fake.tagCalls) != 3 {
		t.Errorf("expected ListTagsForResource only on the 3 prefix-matching collections; got %d call(s) on %v", len(fake.tagCalls), fake.tagCalls)
	}
}

func TestOASCollectionDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeOASCollectionClient{
		pages: []opensearchserverless.ListCollectionsOutput{
			{
				CollectionSummaries: []aosstypes.CollectionSummary{aossSummary("c1", "io-foo-a", "arn:aws:aoss:us-east-1:123:collection/c1")},
				NextToken:           aws.String("tok1"),
			},
			{CollectionSummaries: []aosstypes.CollectionSummary{aossSummary("c2", "io-foo-b", "arn:aws:aoss:us-east-1:123:collection/c2")}}, // terminal
		},
		tagsByID: map[string][]aosstypes.Tag{
			"arn:aws:aoss:us-east-1:123:collection/c1": {aossTagPair("Project", "io-foo")},
			"arn:aws:aoss:us-east-1:123:collection/c2": {aossTagPair("Project", "io-foo")},
		},
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
}

func TestOASCollectionDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	fake := &fakeOASCollectionClient{
		pages: []opensearchserverless.ListCollectionsOutput{{
			CollectionSummaries: []aosstypes.CollectionSummary{
				aossSummary("c1", "io-foo-good", "arn:aws:aoss:us-east-1:123:collection/c1"),
				aossSummary("c2", "io-foo-throttled", "arn:aws:aoss:us-east-1:123:collection/c2"),
			},
		}},
		tagsByID: map[string][]aosstypes.Tag{
			"arn:aws:aoss:us-east-1:123:collection/c1": {aossTagPair("Project", "io-foo")},
		},
		tagsErr: map[string]error{
			"arn:aws:aoss:us-east-1:123:collection/c2": errors.New("Throttling"),
		},
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (throttled skipped)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-good" {
		t.Errorf("NameHint=%q, want io-foo-good", got[0].Identity.NameHint)
	}
}

func TestOASCollectionDiscover_PropagatesListError(t *testing.T) {
	t.Parallel()
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient {
		return &fakeOASCollectionClient{listErr: errOASCollectionSeed}
	}, maxConcurrency: 4}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errOASCollectionSeed) {
		t.Errorf("err=%v, want errors.Is(err, errOASCollectionSeed)", err)
	}
}

type blockingOASCollectionClient struct {
	pages []opensearchserverless.ListCollectionsOutput
	tags  map[string][]aosstypes.Tag

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int
	starts      chan string

	listIdx int
}

func (c *blockingOASCollectionClient) ListCollections(_ context.Context, _ *opensearchserverless.ListCollectionsInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionsOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &opensearchserverless.ListCollectionsOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingOASCollectionClient) ListTagsForResource(ctx context.Context, in *opensearchserverless.ListTagsForResourceInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
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
		return &opensearchserverless.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *blockingOASCollectionClient) BatchGetCollection(_ context.Context, _ *opensearchserverless.BatchGetCollectionInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.BatchGetCollectionOutput, error) {
	return nil, errors.New("blockingOASCollectionClient.BatchGetCollection: unused")
}

func TestOASCollectionDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4

	cs := make([]aosstypes.CollectionSummary, total)
	tags := make(map[string][]aosstypes.Tag, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("cid-%d", i)
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn:aws:aoss:us-east-1:123:collection/%s", id)
		cs[i] = aossSummary(id, name, arn)
		tags[arn] = []aosstypes.Tag{aossTagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingOASCollectionClient{
		pages:   []opensearchserverless.ListCollectionsOutput{{CollectionSummaries: cs}},
		tags:    tags,
		release: release,
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return bc }, maxConcurrency: limit}
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

func TestOASCollectionDiscover_ContextCancellationUnblocksSiblings(t *testing.T) {
	t.Parallel()
	const total = 5
	cs := make([]aosstypes.CollectionSummary, total)
	tags := make(map[string][]aosstypes.Tag, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("cid-%d", i)
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn:aws:aoss:us-east-1:123:collection/%s", id)
		cs[i] = aossSummary(id, name, arn)
		tags[arn] = []aosstypes.Tag{aossTagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	starts := make(chan string, total)
	bc := &blockingOASCollectionClient{
		pages:   []opensearchserverless.ListCollectionsOutput{{CollectionSummaries: cs}},
		tags:    tags,
		release: release,
		starts:  starts,
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return bc }, maxConcurrency: total}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(ctx, DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
		done <- err
	}()
	for i := 0; i < total; i++ {
		select {
		case <-starts:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d goroutines entered ListTagsForResource before timeout", i)
		}
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancelled-context error; got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err=%v, want context.Canceled (wrapped is OK)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Discover did not return after parent ctx cancelled")
	}
}

func TestOASCollectionDiscoverByID_AcceptsBareID(t *testing.T) {
	t.Parallel()
	fake := &fakeOASCollectionClient{
		batchByID: map[string]*aosstypes.CollectionDetail{
			"54twtpfw8h10ppzax9ad": {
				Id:   aws.String("54twtpfw8h10ppzax9ad"),
				Name: aws.String("io-foo-orders"),
				Arn:  aws.String("arn:aws:aoss:us-east-1:123:collection/54twtpfw8h10ppzax9ad"),
			},
		},
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "54twtpfw8h10ppzax9ad", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != oasCollectionTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NativeIDs["collection_id"] != "54twtpfw8h10ppzax9ad" {
		t.Errorf("NativeIDs[collection_id]=%q", got.Identity.NativeIDs["collection_id"])
	}
	if got.Identity.NameHint != "io-foo-orders" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
}

func TestOASCollectionDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:aoss:us-east-1:123:collection/54twtpfw8h10ppzax9ad"
	fake := &fakeOASCollectionClient{
		batchByID: map[string]*aosstypes.CollectionDetail{
			"54twtpfw8h10ppzax9ad": {
				Id:   aws.String("54twtpfw8h10ppzax9ad"),
				Name: aws.String("io-foo-orders"),
				Arn:  aws.String(arn),
			},
		},
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NativeIDs["collection_id"] != "54twtpfw8h10ppzax9ad" {
		t.Errorf("NativeIDs[collection_id]=%q", got.Identity.NativeIDs["collection_id"])
	}
}

func TestOASCollectionDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return &fakeOASCollectionClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestOASCollectionDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return &fakeOASCollectionClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:aoss:us-east-1:123:dashboard/abc", // wrong resource type
		"arn:aws:aoss:us-east-1:123:collection/",   // empty id
		"id with space",                            // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

func TestOASCollectionDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeOASCollectionClient{
		"us-east-1": {
			pages: []opensearchserverless.ListCollectionsOutput{{CollectionSummaries: []aosstypes.CollectionSummary{
				aossSummary("c1", "io-foo-east", "arn:aws:aoss:us-east-1:123:collection/c1"),
			}}},
			tagsByID: map[string][]aosstypes.Tag{
				"arn:aws:aoss:us-east-1:123:collection/c1": {aossTagPair("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []opensearchserverless.ListCollectionsOutput{{CollectionSummaries: []aosstypes.CollectionSummary{
				aossSummary("c2", "io-foo-west", "arn:aws:aoss:eu-west-1:123:collection/c2"),
			}}},
			tagsByID: map[string][]aosstypes.Tag{
				"arn:aws:aoss:eu-west-1:123:collection/c2": {aossTagPair("Project", "io-foo")},
			},
		},
	}
	d := &oasCollectionDiscoverer{new: func(region string) oasCollectionClient { return fakes[region] }, maxConcurrency: 4}
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
			if e.Service != "opensearchserverless_collection" {
				t.Errorf("service_start.service=%q, want opensearchserverless_collection", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "opensearchserverless_collection" {
				t.Errorf("service_finish.service=%q, want opensearchserverless_collection", e.Service)
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

func TestOASCollectionDiscover_EmitsItemFound_PerCollection(t *testing.T) {
	t.Parallel()
	fake := &fakeOASCollectionClient{
		pages: []opensearchserverless.ListCollectionsOutput{{CollectionSummaries: []aosstypes.CollectionSummary{
			aossSummary("c1", "io-foo-a", "arn:aws:aoss:us-east-1:123:collection/c1"),
			aossSummary("c2", "io-foo-b", "arn:aws:aoss:us-east-1:123:collection/c2"),
			aossSummary("c3", "io-foo-untagged", "arn:aws:aoss:us-east-1:123:collection/c3"),
		}}},
		tagsByID: map[string][]aosstypes.Tag{
			"arn:aws:aoss:us-east-1:123:collection/c1": {aossTagPair("Project", "io-foo")},
			"arn:aws:aoss:us-east-1:123:collection/c2": {aossTagPair("Project", "io-foo")},
			"arn:aws:aoss:us-east-1:123:collection/c3": {aossTagPair("Owner", "team")},
		},
	}
	d := &oasCollectionDiscoverer{new: func(_ string) oasCollectionClient { return fake }, maxConcurrency: 4}
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
	wantIDs := map[string]bool{"c1": true, "c2": true}
	for _, it := range items {
		if it.Service != "opensearchserverless_collection" {
			t.Errorf("item.service=%q", it.Service)
		}
		if it.TFType != oasCollectionTFType {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
		if !wantIDs[it.ImportID] {
			t.Errorf("item.import_id=%q not in expected set", it.ImportID)
		}
	}
}
