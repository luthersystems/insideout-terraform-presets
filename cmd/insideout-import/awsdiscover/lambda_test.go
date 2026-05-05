package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type fakeLambdaClient struct {
	pages    []lambda.ListFunctionsOutput
	tagsByID map[string]map[string]string
	tagsErr  map[string]error // errors keyed by ARN

	mu        sync.Mutex
	listCalls int
	tagCalls  []string

	// GetFunction wiring for DiscoverByID tests.
	getByName    map[string]*lambda.GetFunctionOutput
	getErr       error
	getFnCalls   []string
	getFnCallsMu sync.Mutex
}

func (f *fakeLambdaClient) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	idx := f.listCalls
	f.listCalls++
	if idx >= len(f.pages) {
		return &lambda.ListFunctionsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeLambdaClient) ListTags(_ context.Context, in *lambda.ListTagsInput, _ ...func(*lambda.Options)) (*lambda.ListTagsOutput, error) {
	arn := aws.ToString(in.Resource)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &lambda.ListTagsOutput{Tags: f.tagsByID[arn]}, nil
}

func (f *fakeLambdaClient) GetFunction(_ context.Context, in *lambda.GetFunctionInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error) {
	name := aws.ToString(in.FunctionName)
	f.getFnCallsMu.Lock()
	f.getFnCalls = append(f.getFnCalls, name)
	f.getFnCallsMu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	if out, ok := f.getByName[name]; ok {
		return out, nil
	}
	return nil, &lambdatypes.ResourceNotFoundException{}
}

func fn(name, arn string) lambdatypes.FunctionConfiguration {
	return lambdatypes.FunctionConfiguration{
		FunctionName: aws.String(name),
		FunctionArn:  aws.String(arn),
	}
}

func TestLambdaDiscover_FiltersByProjectTag(t *testing.T) {
	t.Parallel()
	d := &lambdaDiscoverer{new: func() lambdaClient {
		return &fakeLambdaClient{
			pages: []lambda.ListFunctionsOutput{
				{Functions: []lambdatypes.FunctionConfiguration{
					fn("io-foo-a", "arn-a"),
					fn("other-b", "arn-b"),
					fn("io-foo-c", "arn-c"),
				}},
			},
			tagsByID: map[string]map[string]string{
				"arn-a": {"Project": "io-foo"},
				"arn-b": {"Project": "other"},
				"arn-c": {"Project": "io-foo"},
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (filtered)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NameHint == "other-b" {
			t.Error("function with non-matching Project tag leaked through filter")
		}
	}
}

func TestLambdaDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	d := &lambdaDiscoverer{new: func() lambdaClient {
		return &fakeLambdaClient{
			pages: []lambda.ListFunctionsOutput{
				{Functions: []lambdatypes.FunctionConfiguration{fn("a", "arn-a"), fn("b", "arn-b")}},
			},
		}
	}}
	got, err := d.Discover(context.Background(), "", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (all when project empty)", len(got))
	}
}

func TestLambdaDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	fake := &fakeLambdaClient{
		pages: []lambda.ListFunctionsOutput{
			{Functions: []lambdatypes.FunctionConfiguration{
				fn("io-foo-a", "arn-a"),
				fn("io-foo-b", "arn-b"),
			}},
		},
		tagsByID: map[string]map[string]string{
			"arn-b": {"Project": "io-foo"},
		},
		tagsErr: map[string]error{"arn-a": errors.New("Throttling")},
	}
	d := &lambdaDiscoverer{new: func() lambdaClient { return fake }}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	// arn-a's ListTags failed → fail-closed (skip), arn-b matched → include.
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (arn-b only)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-b" {
		t.Errorf("NameHint=%q, want io-foo-b", got[0].Identity.NameHint)
	}
	// Pin that ListTags was *attempted* on arn-a before fail-closed kicked
	// in — without this assertion a mutation that skipped arn-a entirely
	// (never even calling ListTags) would still produce len==1 and the
	// test would silently accept.
	if !contains(fake.tagCalls, "arn-a") {
		t.Errorf("ListTags must be attempted on arn-a before fail-closed; tagCalls=%v", fake.tagCalls)
	}
	if !contains(fake.tagCalls, "arn-b") {
		t.Errorf("ListTags must be attempted on arn-b; tagCalls=%v", fake.tagCalls)
	}
}

// TestLambdaDiscover_SkipsFunctionWithNoProjectTag pins the empty-tags-but-
// successful-call branch — distinct from the error branch covered by
// FailClosedOnTagsError. Without this, a mutation that reads
// tagsOut.Tags["Project"] from a tag map with no Project key still
// "succeeds" via the missing-key zero-value path (== "" != project) so
// it's correctly excluded — we want to make sure that exclusion stays
// in place if the conditional is altered.
func TestLambdaDiscover_SkipsFunctionWithNoProjectTag(t *testing.T) {
	t.Parallel()
	d := &lambdaDiscoverer{new: func() lambdaClient {
		return &fakeLambdaClient{
			pages: []lambda.ListFunctionsOutput{
				{Functions: []lambdatypes.FunctionConfiguration{
					fn("io-foo-untagged", "arn-untagged"),
					fn("io-foo-tagged", "arn-tagged"),
				}},
			},
			tagsByID: map[string]map[string]string{
				"arn-untagged": {"Owner": "team", "Env": "prod"}, // no Project key
				"arn-tagged":   {"Project": "io-foo"},
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (untagged excluded)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-tagged" {
		t.Errorf("wrong function admitted: NameHint=%q", got[0].Identity.NameHint)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func TestLambdaDiscover_AbortsOnListFunctionsError(t *testing.T) {
	t.Parallel()
	// fake that returns an error from ListFunctions: the paginator
	// surfaces it directly via NextPage. Use an empty fake struct and
	// inject error via a wrapping client.
	wrap := &lambdaErrClient{err: errors.New("AccessDenied")}
	d := &lambdaDiscoverer{new: func() lambdaClient { return wrap }}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected ListFunctions error to abort")
	}
}

// lambdaErrClient is a tiny client whose ListFunctions always errors.
// ListTags is here only to satisfy the interface — it's never called
// because ListFunctions aborts the run before any tag fan-out begins.
type lambdaErrClient struct{ err error }

func (c *lambdaErrClient) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	return nil, c.err
}

func (c *lambdaErrClient) ListTags(_ context.Context, _ *lambda.ListTagsInput, _ ...func(*lambda.Options)) (*lambda.ListTagsOutput, error) {
	return nil, c.err
}

func (c *lambdaErrClient) GetFunction(_ context.Context, _ *lambda.GetFunctionInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error) {
	return nil, c.err
}

// blockingLambdaClient is a fake whose ListTags signals when each call
// enters and then blocks until release is closed (or ctx is cancelled).
// Used by the concurrency + cancellation tests to observe peak in-flight
// goroutine count and to model a real cloud call that would otherwise
// keep a goroutine pinned waiting for a response.
type blockingLambdaClient struct {
	pages   []lambda.ListFunctionsOutput
	release chan struct{}
	tags    map[string]map[string]string

	mu          sync.Mutex
	inflight    int
	maxInflight int
	starts      chan string // optional: published on each ListTags entry
}

func (c *blockingLambdaClient) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	if len(c.pages) == 0 {
		return &lambda.ListFunctionsOutput{}, nil
	}
	out := c.pages[0]
	c.pages = c.pages[1:]
	return &out, nil
}

func (c *blockingLambdaClient) ListTags(ctx context.Context, in *lambda.ListTagsInput, _ ...func(*lambda.Options)) (*lambda.ListTagsOutput, error) {
	arn := aws.ToString(in.Resource)
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
		return &lambda.ListTagsOutput{Tags: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetFunction is unused by the concurrency tests but required to satisfy
// the lambdaClient interface; the DiscoverByID path is covered by
// fakeLambdaClient.
func (c *blockingLambdaClient) GetFunction(_ context.Context, _ *lambda.GetFunctionInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error) {
	return nil, errors.New("blockingLambdaClient.GetFunction: not used in concurrency tests")
}

// TestLambdaDiscover_BoundedConcurrency pins g.SetLimit(maxConcurrency).
// 50 functions are dispatched but no more than maxConcurrency=3 may run
// concurrently. Without this pin a mutation that drops g.SetLimit (or
// passes a value derived from len(items)) silently regresses the QoS
// contract that protects shared accounts from a noisy-neighbor scan.
func TestLambdaDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 50
	const limit = 3

	pages := []lambda.ListFunctionsOutput{{Functions: make([]lambdatypes.FunctionConfiguration, total)}}
	tags := make(map[string]map[string]string, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn-%d", i)
		pages[0].Functions[i] = fn(name, arn)
		tags[arn] = map[string]string{"Project": "io-foo"}
	}
	release := make(chan struct{})
	bc := &blockingLambdaClient{pages: pages, release: release, tags: tags}

	d := &lambdaDiscoverer{
		new:            func() lambdaClient { return bc },
		maxConcurrency: limit,
	}
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
		done <- err
	}()

	// Wait until at least `limit` calls are in flight, then sample peak.
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
	// Hold for a moment to catch any over-shoot.
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

// TestLambdaDiscover_ContextCancellationUnblocksSiblings pins that a
// cancelled parent context propagates through gctx and unblocks any
// goroutines stuck on a long-running ListTags. Without errgroup wiring,
// the fail-closed handler would leave them hanging until the SDK call
// itself observed the cancellation.
//
// Implementation note (deviation from #270 brief): the brief asked for
// "return an error immediately for one item" to trigger sibling cancel.
// Doing that would require flipping the documented fail-closed semantic
// (existing tests TestLambdaDiscover_FailClosedOnTagsError and
// TestDynamoDBDiscover_FailClosedOnTagsError both pin per-item errors as
// non-fatal). Parent-context cancellation is the equivalent path that
// the real operator hits (Ctrl+C, parent timeout) and exercises the
// same "siblings unblock via gctx, Discover returns the error" wiring.
func TestLambdaDiscover_ContextCancellationUnblocksSiblings(t *testing.T) {
	t.Parallel()
	const total = 5
	pages := []lambda.ListFunctionsOutput{{Functions: make([]lambdatypes.FunctionConfiguration, total)}}
	tags := make(map[string]map[string]string, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%d", i)
		arn := fmt.Sprintf("arn-%d", i)
		pages[0].Functions[i] = fn(name, arn)
		tags[arn] = map[string]string{"Project": "io-foo"}
	}
	release := make(chan struct{}) // never closed: every call blocks until ctx cancels
	starts := make(chan string, total)
	bc := &blockingLambdaClient{pages: pages, release: release, tags: tags, starts: starts}

	d := &lambdaDiscoverer{
		new:            func() lambdaClient { return bc },
		maxConcurrency: total, // allow all to dispatch at once
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(ctx, "io-foo", "us-east-1", "123")
		done <- err
	}()

	// Wait until every blocked goroutine has entered ListTags so we know
	// the cancel signal must propagate via gctx, not via "no goroutine
	// started yet".
	for i := 0; i < total; i++ {
		select {
		case <-starts:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d goroutines entered ListTags before timeout", i)
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
		t.Fatal("Discover did not return after parent ctx cancelled — siblings stuck")
	}
}

func TestLambdaDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:lambda:us-east-1:123:function:io-foo-handler"
	d := &lambdaDiscoverer{new: func() lambdaClient {
		return &fakeLambdaClient{getByName: map[string]*lambda.GetFunctionOutput{
			"io-foo-handler": {Configuration: &lambdatypes.FunctionConfiguration{
				FunctionName: aws.String("io-foo-handler"),
				FunctionArn:  aws.String(arn),
			}},
		}}
	}}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_lambda_function" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-handler" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] != arn {
		t.Errorf("NativeIDs[arn]=%q, want %q", got.Identity.NativeIDs["arn"], arn)
	}
}

func TestLambdaDiscoverByID_StripsVersionFromARN(t *testing.T) {
	t.Parallel()
	d := &lambdaDiscoverer{new: func() lambdaClient {
		return &fakeLambdaClient{getByName: map[string]*lambda.GetFunctionOutput{
			"io-foo-handler": {Configuration: &lambdatypes.FunctionConfiguration{
				FunctionName: aws.String("io-foo-handler"),
				FunctionArn:  aws.String("arn:aws:lambda:us-east-1:123:function:io-foo-handler"),
			}},
		}}
	}}
	// Versioned/aliased ARN — Lambda's import expects the bare name.
	_, err := d.DiscoverByID(context.Background(),
		"arn:aws:lambda:us-east-1:123:function:io-foo-handler:PROD",
		"us-east-1", "123")
	if err != nil {
		t.Fatalf("expected version stripped to bare name; got %v", err)
	}
}

func TestLambdaDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &lambdaDiscoverer{new: func() lambdaClient { return &fakeLambdaClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestLambdaDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &lambdaDiscoverer{new: func() lambdaClient { return &fakeLambdaClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket",
		"arn:aws:lambda:us-east-1:123:layer:my-layer:1",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
