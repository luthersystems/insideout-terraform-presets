package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

// errBedrockGuardrailSeed is the package-level sentinel returned by the
// fake bedrock client in tests that want to assert ListGuardrails error
// propagation.
var errBedrockGuardrailSeed = errors.New("AccessDenied")

type fakeBedrockGuardrailClient struct {
	pages    []bedrock.ListGuardrailsOutput
	tagsByID map[string][]bedrocktypes.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []bedrock.ListGuardrailsInput
	tagCalls  []string
	listErr   error

	getByID  map[string]*bedrock.GetGuardrailOutput
	getErr   error
	getCalls []string
}

func (f *fakeBedrockGuardrailClient) ListGuardrails(_ context.Context, in *bedrock.ListGuardrailsInput, _ ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &bedrock.ListGuardrailsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeBedrockGuardrailClient) ListTagsForResource(_ context.Context, in *bedrock.ListTagsForResourceInput, _ ...func(*bedrock.Options)) (*bedrock.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceARN)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &bedrock.ListTagsForResourceOutput{Tags: f.tagsByID[arn]}, nil
}

func (f *fakeBedrockGuardrailClient) GetGuardrail(_ context.Context, in *bedrock.GetGuardrailInput, _ ...func(*bedrock.Options)) (*bedrock.GetGuardrailOutput, error) {
	id := aws.ToString(in.GuardrailIdentifier)
	f.mu.Lock()
	f.getCalls = append(f.getCalls, id)
	f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	if out, ok := f.getByID[id]; ok {
		return out, nil
	}
	return nil, &bedrocktypes.ResourceNotFoundException{}
}

func bedrockTagPair(k, v string) bedrocktypes.Tag {
	return bedrocktypes.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func bedrockGuardrailSummary(id, name, arn, version string) bedrocktypes.GuardrailSummary {
	gs := bedrocktypes.GuardrailSummary{
		Id:   aws.String(id),
		Name: aws.String(name),
		Arn:  aws.String(arn),
	}
	if version != "" {
		gs.Version = aws.String(version)
	}
	return gs
}

func TestBedrockGuardrailDiscover_PrefixThenTagFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockGuardrailClient{
		pages: []bedrock.ListGuardrailsOutput{{
			Guardrails: []bedrocktypes.GuardrailSummary{
				bedrockGuardrailSummary("g1", "io-foo-orders", "arn:aws:bedrock:us-east-1:123:guardrail/g1", "DRAFT"),
				bedrockGuardrailSummary("g2", "io-foo-events", "arn:aws:bedrock:us-east-1:123:guardrail/g2", ""),
				bedrockGuardrailSummary("g3", "other-guard", "arn:aws:bedrock:us-east-1:123:guardrail/g3", ""),
				bedrockGuardrailSummary("g4", "io-foo-untagged", "arn:aws:bedrock:us-east-1:123:guardrail/g4", ""),
			},
		}},
		tagsByID: map[string][]bedrocktypes.Tag{
			"arn:aws:bedrock:us-east-1:123:guardrail/g1": {bedrockTagPair("Project", "io-foo")},
			"arn:aws:bedrock:us-east-1:123:guardrail/g2": {bedrockTagPair("Project", "io-foo")},
			"arn:aws:bedrock:us-east-1:123:guardrail/g4": {bedrockTagPair("Owner", "team")},
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return fake }, maxConcurrency: 4}
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
		if ir.Identity.NativeIDs["guardrail_id"] == "" {
			t.Error("NativeIDs[guardrail_id] empty")
		}
	}
	// Pin: the prefix filter should gate the ListTagsForResource fan-out
	// to the 3 prefix-matching guardrails (not the unrelated 4th).
	if len(fake.tagCalls) != 3 {
		t.Errorf("expected ListTagsForResource only on the 3 prefix-matching guardrails; got %d call(s) on %v", len(fake.tagCalls), fake.tagCalls)
	}
	// Version surfaces only when set on the source.
	var foundVersionG1 bool
	for _, ir := range got {
		if ir.Identity.NativeIDs["guardrail_id"] == "g1" {
			foundVersionG1 = ir.Identity.NativeIDs["version"] == "DRAFT"
		}
	}
	if !foundVersionG1 {
		t.Error("expected NativeIDs[version]=DRAFT on g1")
	}
}

func TestBedrockGuardrailDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockGuardrailClient{
		pages: []bedrock.ListGuardrailsOutput{
			{
				Guardrails: []bedrocktypes.GuardrailSummary{bedrockGuardrailSummary("g1", "io-foo-a", "arn:aws:bedrock:us-east-1:123:guardrail/g1", "")},
				NextToken:  aws.String("tok1"),
			},
			{Guardrails: []bedrocktypes.GuardrailSummary{bedrockGuardrailSummary("g2", "io-foo-b", "arn:aws:bedrock:us-east-1:123:guardrail/g2", "")}}, // terminal
		},
		tagsByID: map[string][]bedrocktypes.Tag{
			"arn:aws:bedrock:us-east-1:123:guardrail/g1": {bedrockTagPair("Project", "io-foo")},
			"arn:aws:bedrock:us-east-1:123:guardrail/g2": {bedrockTagPair("Project", "io-foo")},
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
}

func TestBedrockGuardrailDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockGuardrailClient{
		pages: []bedrock.ListGuardrailsOutput{{
			Guardrails: []bedrocktypes.GuardrailSummary{
				bedrockGuardrailSummary("g1", "io-foo-good", "arn:aws:bedrock:us-east-1:123:guardrail/g1", ""),
				bedrockGuardrailSummary("g2", "io-foo-throttled", "arn:aws:bedrock:us-east-1:123:guardrail/g2", ""),
			},
		}},
		tagsByID: map[string][]bedrocktypes.Tag{
			"arn:aws:bedrock:us-east-1:123:guardrail/g1": {bedrockTagPair("Project", "io-foo")},
		},
		tagsErr: map[string]error{
			"arn:aws:bedrock:us-east-1:123:guardrail/g2": errors.New("Throttling"),
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return fake }, maxConcurrency: 4}
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

func TestBedrockGuardrailDiscover_PropagatesListError(t *testing.T) {
	t.Parallel()
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient {
		return &fakeBedrockGuardrailClient{listErr: errBedrockGuardrailSeed}
	}, maxConcurrency: 4}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errBedrockGuardrailSeed) {
		t.Errorf("err=%v, want errors.Is(err, errBedrockGuardrailSeed)", err)
	}
}

// blockingBedrockGuardrailClient supports the bounded-concurrency and
// context-cancellation tests.
type blockingBedrockGuardrailClient struct {
	pages []bedrock.ListGuardrailsOutput
	tags  map[string][]bedrocktypes.Tag

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int
	starts      chan string

	listIdx int
}

func (c *blockingBedrockGuardrailClient) ListGuardrails(_ context.Context, _ *bedrock.ListGuardrailsInput, _ ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &bedrock.ListGuardrailsOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingBedrockGuardrailClient) ListTagsForResource(ctx context.Context, in *bedrock.ListTagsForResourceInput, _ ...func(*bedrock.Options)) (*bedrock.ListTagsForResourceOutput, error) {
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
		return &bedrock.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *blockingBedrockGuardrailClient) GetGuardrail(_ context.Context, _ *bedrock.GetGuardrailInput, _ ...func(*bedrock.Options)) (*bedrock.GetGuardrailOutput, error) {
	return nil, errors.New("blockingBedrockGuardrailClient.GetGuardrail: unused")
}

func TestBedrockGuardrailDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4

	guards := make([]bedrocktypes.GuardrailSummary, total)
	tags := make(map[string][]bedrocktypes.Tag, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("gid-%d", i)
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn:aws:bedrock:us-east-1:123:guardrail/%s", id)
		guards[i] = bedrockGuardrailSummary(id, name, arn, "")
		tags[arn] = []bedrocktypes.Tag{bedrockTagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingBedrockGuardrailClient{
		pages:   []bedrock.ListGuardrailsOutput{{Guardrails: guards}},
		tags:    tags,
		release: release,
	}
	d := &bedrockGuardrailDiscoverer{
		new:            func(_ string) bedrockGuardrailClient { return bc },
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

func TestBedrockGuardrailDiscover_ContextCancellationUnblocksSiblings(t *testing.T) {
	t.Parallel()
	const total = 5
	guards := make([]bedrocktypes.GuardrailSummary, total)
	tags := make(map[string][]bedrocktypes.Tag, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("gid-%d", i)
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn:aws:bedrock:us-east-1:123:guardrail/%s", id)
		guards[i] = bedrockGuardrailSummary(id, name, arn, "")
		tags[arn] = []bedrocktypes.Tag{bedrockTagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	starts := make(chan string, total)
	bc := &blockingBedrockGuardrailClient{
		pages:   []bedrock.ListGuardrailsOutput{{Guardrails: guards}},
		tags:    tags,
		release: release,
		starts:  starts,
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return bc }, maxConcurrency: total}

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

func TestBedrockGuardrailDiscoverByID_AcceptsBareID(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockGuardrailClient{
		getByID: map[string]*bedrock.GetGuardrailOutput{
			"g1": {
				GuardrailId:  aws.String("g1"),
				Name:         aws.String("io-foo-orders"),
				GuardrailArn: aws.String("arn:aws:bedrock:us-east-1:123:guardrail/g1"),
				Version:      aws.String("DRAFT"),
			},
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "g1", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != bedrockGuardrailTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-orders" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["guardrail_id"] != "g1" {
		t.Errorf("NativeIDs[guardrail_id]=%q", got.Identity.NativeIDs["guardrail_id"])
	}
	if got.Identity.NativeIDs["version"] != "DRAFT" {
		t.Errorf("NativeIDs[version]=%q", got.Identity.NativeIDs["version"])
	}
}

func TestBedrockGuardrailDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:bedrock:us-east-1:123:guardrail/g1"
	fake := &fakeBedrockGuardrailClient{
		getByID: map[string]*bedrock.GetGuardrailOutput{
			"g1": {
				GuardrailId:  aws.String("g1"),
				Name:         aws.String("io-foo-orders"),
				GuardrailArn: aws.String(arn),
			},
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NativeIDs["guardrail_id"] != "g1" {
		t.Errorf("NativeIDs[guardrail_id]=%q", got.Identity.NativeIDs["guardrail_id"])
	}
}

func TestBedrockGuardrailDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return &fakeBedrockGuardrailClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestBedrockGuardrailDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return &fakeBedrockGuardrailClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:bedrock:us-east-1:123:agent/abc",  // wrong resource type
		"arn:aws:bedrock:us-east-1:123:guardrail/", // empty resource id
		"id with space", // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

func TestBedrockGuardrailDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeBedrockGuardrailClient{
		"us-east-1": {
			pages: []bedrock.ListGuardrailsOutput{{Guardrails: []bedrocktypes.GuardrailSummary{
				bedrockGuardrailSummary("g1", "io-foo-east", "arn:aws:bedrock:us-east-1:123:guardrail/g1", ""),
			}}},
			tagsByID: map[string][]bedrocktypes.Tag{
				"arn:aws:bedrock:us-east-1:123:guardrail/g1": {bedrockTagPair("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []bedrock.ListGuardrailsOutput{{Guardrails: []bedrocktypes.GuardrailSummary{
				bedrockGuardrailSummary("g2", "io-foo-west", "arn:aws:bedrock:eu-west-1:123:guardrail/g2", ""),
			}}},
			tagsByID: map[string][]bedrocktypes.Tag{
				"arn:aws:bedrock:eu-west-1:123:guardrail/g2": {bedrockTagPair("Project", "io-foo")},
			},
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(region string) bedrockGuardrailClient { return fakes[region] }, maxConcurrency: 4}
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
			if e.Service != "bedrock_guardrail" {
				t.Errorf("service_start.service=%q, want bedrock_guardrail", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "bedrock_guardrail" {
				t.Errorf("service_finish.service=%q, want bedrock_guardrail", e.Service)
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

func TestBedrockGuardrailDiscover_EmitsItemFound_PerGuardrail(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockGuardrailClient{
		pages: []bedrock.ListGuardrailsOutput{{
			Guardrails: []bedrocktypes.GuardrailSummary{
				bedrockGuardrailSummary("g1", "io-foo-a", "arn:aws:bedrock:us-east-1:123:guardrail/g1", ""),
				bedrockGuardrailSummary("g2", "io-foo-b", "arn:aws:bedrock:us-east-1:123:guardrail/g2", ""),
				bedrockGuardrailSummary("g3", "io-foo-untagged", "arn:aws:bedrock:us-east-1:123:guardrail/g3", ""),
			},
		}},
		tagsByID: map[string][]bedrocktypes.Tag{
			"arn:aws:bedrock:us-east-1:123:guardrail/g1": {bedrockTagPair("Project", "io-foo")},
			"arn:aws:bedrock:us-east-1:123:guardrail/g2": {bedrockTagPair("Project", "io-foo")},
			"arn:aws:bedrock:us-east-1:123:guardrail/g3": {bedrockTagPair("Owner", "team")},
		},
	}
	d := &bedrockGuardrailDiscoverer{new: func(_ string) bedrockGuardrailClient { return fake }, maxConcurrency: 4}
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
		t.Errorf("item_found count=%d, want %d (one per emitted resource)", len(items), len(got))
	}
	wantIDs := map[string]bool{"g1,DRAFT": true, "g2,DRAFT": true}
	for _, it := range items {
		if it.Service != "bedrock_guardrail" {
			t.Errorf("item.service=%q, want bedrock_guardrail", it.Service)
		}
		if it.TFType != bedrockGuardrailTFType {
			t.Errorf("item.tf_type=%q, want %s", it.TFType, bedrockGuardrailTFType)
		}
		if !wantIDs[it.ImportID] {
			t.Errorf("item.import_id=%q not in expected set", it.ImportID)
		}
	}
}
