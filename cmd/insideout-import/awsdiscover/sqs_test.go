package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type fakeSQSClient struct {
	pages []sqs.ListQueuesOutput
	calls []sqs.ListQueuesInput
	err   error // when non-nil, every ListQueues call returns this

	// GetQueueUrl wiring: tests set getURLByName to control responses.
	getURLByName map[string]string
	getURLErr    error // when non-nil, GetQueueUrl returns this
	getURLCalls  []sqs.GetQueueUrlInput

	// ListQueueTags wiring (#291): tagsByURL maps queue URL → tag map.
	// Empty/nil for queues without tags. tagsErr is returned for every
	// ListQueueTags call when set.
	tagsByURL  map[string]map[string]string
	tagsErr    error
	tagsCalls  []string // observed queue URLs
	regionSeen []string // every region the new(...) closure observed
}

func (f *fakeSQSClient) ListQueues(_ context.Context, in *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		// out of pages; return empty terminal page
		return &sqs.ListQueuesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeSQSClient) GetQueueUrl(_ context.Context, in *sqs.GetQueueUrlInput, _ ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	f.getURLCalls = append(f.getURLCalls, *in)
	if f.getURLErr != nil {
		return nil, f.getURLErr
	}
	if url, ok := f.getURLByName[*in.QueueName]; ok {
		return &sqs.GetQueueUrlOutput{QueueUrl: &url}, nil
	}
	return nil, &sqstypes.QueueDoesNotExist{}
}

func (f *fakeSQSClient) ListQueueTags(_ context.Context, in *sqs.ListQueueTagsInput, _ ...func(*sqs.Options)) (*sqs.ListQueueTagsOutput, error) {
	f.tagsCalls = append(f.tagsCalls, *in.QueueUrl)
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if tags, ok := f.tagsByURL[*in.QueueUrl]; ok {
		return &sqs.ListQueueTagsOutput{Tags: tags}, nil
	}
	return &sqs.ListQueueTagsOutput{}, nil
}

func ptr(s string) *string { return &s }

func TestSQSDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &sqsDiscoverer{new: func(_ string) sqsClient {
		return &fakeSQSClient{
			pages: []sqs.ListQueuesOutput{
				{
					QueueUrls: []string{
						"https://sqs.us-east-1.amazonaws.com/123/io-foo-orders",
						"https://sqs.us-east-1.amazonaws.com/123/io-foo-orders-dlq",
					},
				},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != "aws_sqs_queue" {
			t.Errorf("Type=%q", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["url"] == "" {
			t.Error("NativeIDs[url] empty")
		}
		if ir.Identity.NativeIDs["name"] == "" {
			t.Error("NativeIDs[name] empty")
		}
	}
	// Output is sorted by URL → addresses are deterministic.
	if got[0].Identity.NameHint != "io-foo-orders" {
		t.Errorf("first NameHint=%q, want io-foo-orders (sorted)", got[0].Identity.NameHint)
	}
}

func TestSQSDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	d := &sqsDiscoverer{new: func(_ string) sqsClient {
		return &fakeSQSClient{
			pages: []sqs.ListQueuesOutput{
				{QueueUrls: []string{"https://example/io-foo-a"}, NextToken: ptr("tok1")},
				{QueueUrls: []string{"https://example/io-foo-b"}, NextToken: ptr("tok2")},
				{QueueUrls: []string{"https://example/io-foo-c"}}, // terminal
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
}

func TestSQSDiscover_PassesPrefixServerSide(t *testing.T) {
	t.Parallel()
	fake := &fakeSQSClient{}
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one ListQueues call")
	}
	in := fake.calls[0]
	if in.QueueNamePrefix == nil || *in.QueueNamePrefix != "io-foo" {
		t.Errorf("QueueNamePrefix=%v, want io-foo", in.QueueNamePrefix)
	}
}

func TestSQSDiscover_EmptyProjectPassesNoPrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeSQSClient{}
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return fake }}
	if _, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("expected at least one ListQueues call")
	}
	if fake.calls[0].QueueNamePrefix != nil {
		t.Errorf("QueueNamePrefix=%v, want nil for empty project", fake.calls[0].QueueNamePrefix)
	}
}

func TestSQSDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &sqsDiscoverer{new: func(_ string) sqsClient {
		return &fakeSQSClient{err: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSQSDiscoverByID_AcceptsURL(t *testing.T) {
	t.Parallel()
	fake := &fakeSQSClient{getURLByName: map[string]string{
		"io-foo-orders": "https://sqs.us-east-1.amazonaws.com/123/io-foo-orders",
	}}
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return fake }}
	got, err := d.DiscoverByID(context.Background(),
		"https://sqs.us-east-1.amazonaws.com/123/io-foo-orders", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_sqs_queue" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-orders" {
		t.Errorf("NameHint=%q, want io-foo-orders", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["url"] == "" {
		t.Error("NativeIDs[url] empty")
	}
	if len(fake.getURLCalls) != 1 || *fake.getURLCalls[0].QueueName != "io-foo-orders" {
		t.Errorf("expected one GetQueueUrl call with name=io-foo-orders; got %v", fake.getURLCalls)
	}
}

func TestSQSDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	fake := &fakeSQSClient{getURLByName: map[string]string{
		"io-foo-orders": "https://sqs.us-east-1.amazonaws.com/123/io-foo-orders",
	}}
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return fake }}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:sqs:us-east-1:123:io-foo-orders", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NameHint != "io-foo-orders" {
		t.Errorf("NameHint=%q, want io-foo-orders", got.Identity.NameHint)
	}
}

func TestSQSDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	fake := &fakeSQSClient{} // empty getURLByName triggers QueueDoesNotExist
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return fake }}
	_, err := d.DiscoverByID(context.Background(), "io-foo-missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestSQSDiscover_MultiRegionTriggersOneSDKCallPerRegion (#291) is the
// pattern-pin for every per-service Discover's `for _, region := range
// args.Regions` loop. The aggregator-level test only proves the slice
// is threaded; this test proves it is *iterated*. A regression that
// drops the inner loop in any per-service file (e.g. ignores Regions[1]
// and only scans Regions[0]) survives every other pin.
//
// Strategy: hand the closure a per-region fake so the test can assert
// (a) both regions trigger a ListQueues call, and (b) the manifest
// contains entries from both regions. The same shape generalizes to
// every other regional service — this serves as the canonical example.
func TestSQSDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeSQSClient{
		"us-east-1": {
			pages: []sqs.ListQueuesOutput{{QueueUrls: []string{"https://sqs.us-east-1.amazonaws.com/123/io-foo-east"}}},
		},
		"eu-west-1": {
			pages: []sqs.ListQueuesOutput{{QueueUrls: []string{"https://sqs.eu-west-1.amazonaws.com/123/io-foo-west"}}},
		},
	}
	var seenRegions []string
	d := &sqsDiscoverer{new: func(region string) sqsClient {
		seenRegions = append(seenRegions, region)
		f, ok := fakes[region]
		if !ok {
			t.Fatalf("closure called with unexpected region %q", region)
		}
		return f
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}

	// (a) closure invoked once per region.
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seenRegions)
	}
	// (b) each region's fake saw a ListQueues call.
	if len(fakes["us-east-1"].calls) == 0 {
		t.Error("us-east-1 fake never received ListQueues; per-region loop dropped the first region")
	}
	if len(fakes["eu-west-1"].calls) == 0 {
		t.Error("eu-west-1 fake never received ListQueues; per-region loop dropped the second region")
	}
	// (c) manifest carries entries from both regions.
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
	gotNames := map[string]bool{}
	for _, ir := range got {
		gotNames[ir.Identity.NameHint] = true
	}
	if !gotNames["io-foo-east"] || !gotNames["io-foo-west"] {
		t.Errorf("manifest names = %v, want both io-foo-east and io-foo-west", gotNames)
	}
}

// TestSQSDiscover_TagSelectorAppliedAsFilter (#291) is the pattern-pin
// for the per-service Discover's `if !MatchesAll(...) { continue }`
// in-loop filter. The aggregator-level test only proves selectors are
// threaded; this test proves they are *applied*. A regression that
// drops the MatchesAll call in any per-service file (or inverts the
// condition) survives every other pin.
func TestSQSDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	urlProd := "https://sqs.us-east-1.amazonaws.com/123/io-foo-prod"
	urlStaging := "https://sqs.us-east-1.amazonaws.com/123/io-foo-staging"
	fake := &fakeSQSClient{
		pages: []sqs.ListQueuesOutput{{QueueUrls: []string{urlProd, urlStaging}}},
		tagsByURL: map[string]map[string]string{
			urlProd:    {"env": "prod"},
			urlStaging: {"env": "staging"},
		},
	}
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod queue should pass)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-prod" {
		t.Errorf("NameHint=%q, want io-foo-prod", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestSQSDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &sqsDiscoverer{new: func(_ string) sqsClient { return &fakeSQSClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::some-bucket", // wrong service
		"arn:aws:lambda:us-east-1:123:function:hello", // wrong service
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
