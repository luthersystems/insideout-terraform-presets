package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDynamoClient struct {
	pages    []dynamodb.ListTablesOutput
	tagsByID map[string][]dynamotypes.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []dynamodb.ListTablesInput
	tagCalls  []string
	listErr   error
}

func (f *fakeDynamoClient) ListTables(_ context.Context, in *dynamodb.ListTablesInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	f.listCalls = append(f.listCalls, *in)
	if f.listErr != nil {
		return nil, f.listErr
	}
	idx := len(f.listCalls) - 1
	if idx >= len(f.pages) {
		return &dynamodb.ListTablesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeDynamoClient) ListTagsOfResource(_ context.Context, in *dynamodb.ListTagsOfResourceInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &dynamodb.ListTagsOfResourceOutput{Tags: f.tagsByID[arn]}, nil
}

func tagPair(k, v string) dynamotypes.Tag {
	return dynamotypes.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func TestDynamoDBDiscover_PrefixThenTagFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeDynamoClient{
		pages: []dynamodb.ListTablesOutput{
			{TableNames: []string{"io-foo-orders", "io-foo-events", "other-table", "io-foo-untagged"}},
		},
		tagsByID: map[string][]dynamotypes.Tag{
			"arn:aws:dynamodb:us-east-1:123:table/io-foo-orders":   {tagPair("Project", "io-foo")},
			"arn:aws:dynamodb:us-east-1:123:table/io-foo-events":   {tagPair("Project", "io-foo")},
			"arn:aws:dynamodb:us-east-1:123:table/io-foo-untagged": {tagPair("Owner", "team")},
		},
	}
	d := &dynamoDiscoverer{new: func() dynamoClient { return fake }}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	// Three names match the prefix; one (untagged) lacks Project tag.
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix + tag filter)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Error("NativeIDs[arn] empty")
		}
	}
	// Prefix is supposed to gate the ARN construction so we don't fan
	// out ListTagsOfResource on every table in the account. Without this
	// pin, a mutation that drops the prefix check (`if true {...}`) still
	// produces len==2 because non-prefix-matching tables get tag-filtered
	// out — the optimization is silent.
	if len(fake.tagCalls) != 3 {
		t.Errorf("expected ListTagsOfResource only on the 3 prefix-matching tables; got %d call(s) on %v", len(fake.tagCalls), fake.tagCalls)
	}
}

func TestDynamoDBDiscover_PaginatesUntilNoLastEvaluatedKey(t *testing.T) {
	t.Parallel()
	d := &dynamoDiscoverer{new: func() dynamoClient {
		return &fakeDynamoClient{
			pages: []dynamodb.ListTablesOutput{
				{TableNames: []string{"io-foo-a"}, LastEvaluatedTableName: aws.String("io-foo-a")},
				{TableNames: []string{"io-foo-b"}}, // terminal
			},
			tagsByID: map[string][]dynamotypes.Tag{
				"arn:aws:dynamodb:us-east-1:123:table/io-foo-a": {tagPair("Project", "io-foo")},
				"arn:aws:dynamodb:us-east-1:123:table/io-foo-b": {tagPair("Project", "io-foo")},
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
}

func TestDynamoDBDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	d := &dynamoDiscoverer{new: func() dynamoClient {
		return &fakeDynamoClient{
			pages: []dynamodb.ListTablesOutput{
				{TableNames: []string{"io-foo-good", "io-foo-throttled"}},
			},
			tagsByID: map[string][]dynamotypes.Tag{
				"arn:aws:dynamodb:us-east-1:123:table/io-foo-good": {tagPair("Project", "io-foo")},
			},
			tagsErr: map[string]error{
				"arn:aws:dynamodb:us-east-1:123:table/io-foo-throttled": errors.New("Throttling"),
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
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

func TestDynamoDBDiscover_PrefixOnlyFallback(t *testing.T) {
	t.Parallel()
	// dynamodb.go:59 falls back to prefix-only when EITHER accountID OR
	// region is empty (we cannot construct the ARN ListTagsOfResource
	// needs). Both legs are exercised below — without both, a mutation
	// that swaps `||` for `&&` survives.
	cases := []struct {
		name      string
		region    string
		accountID string
	}{
		{name: "empty account id", region: "us-east-1", accountID: ""},
		{name: "empty region", region: "", accountID: "123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeDynamoClient{
				pages: []dynamodb.ListTablesOutput{
					{TableNames: []string{"io-foo-x", "other-y"}},
				},
			}
			d := &dynamoDiscoverer{new: func() dynamoClient { return fake }}
			got, err := d.Discover(context.Background(), "io-foo", tc.region, tc.accountID)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("len=%d, want 1 (prefix-only fallback)", len(got))
			}
			// Pin: no ListTagsOfResource calls happened — the fallback
			// was reached, not the full filter path.
			if len(fake.tagCalls) != 0 {
				t.Errorf("fallback should skip ListTagsOfResource; got %d call(s)", len(fake.tagCalls))
			}
		})
	}
}

func TestDynamoDBDiscover_PropagatesListTablesError(t *testing.T) {
	t.Parallel()
	d := &dynamoDiscoverer{new: func() dynamoClient {
		return &fakeDynamoClient{listErr: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}

// blockingDynamoClient is a fake whose ListTagsOfResource signals when
// each call enters and then blocks until release is closed (or ctx is
// cancelled). Used by the concurrency + cancellation tests.
type blockingDynamoClient struct {
	pages []dynamodb.ListTablesOutput
	tags  map[string][]dynamotypes.Tag

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int
	starts      chan string

	listIdx int
}

func (c *blockingDynamoClient) ListTables(_ context.Context, _ *dynamodb.ListTablesInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	if c.listIdx >= len(c.pages) {
		return &dynamodb.ListTablesOutput{}, nil
	}
	out := c.pages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingDynamoClient) ListTagsOfResource(ctx context.Context, in *dynamodb.ListTagsOfResourceInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
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
		return &dynamodb.ListTagsOfResourceOutput{Tags: c.tags[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestDynamoDBDiscover_BoundedConcurrency mirrors the Lambda test for
// the DynamoDB code path — distinct discoverer, distinct errgroup, so
// we pin both. Without separate coverage a regression that drops
// SetLimit on dynamodb.go but keeps it on lambda.go would slip through.
func TestDynamoDBDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4

	names := make([]string, total)
	tags := make(map[string][]dynamotypes.Tag, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%d", i)
		names[i] = name
		arn := fmt.Sprintf("arn:aws:dynamodb:us-east-1:123:table/%s", name)
		tags[arn] = []dynamotypes.Tag{tagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	bc := &blockingDynamoClient{
		pages:   []dynamodb.ListTablesOutput{{TableNames: names}},
		tags:    tags,
		release: release,
	}
	d := &dynamoDiscoverer{
		new:            func() dynamoClient { return bc },
		maxConcurrency: limit,
	}

	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
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

// TestDynamoDBDiscover_ContextCancellationUnblocksSiblings mirrors the
// lambda test — pins the same gctx-propagation contract on the DynamoDB
// errgroup. See the lambda variant's docstring for why ctx-cancellation
// is the chosen trigger over per-item error injection.
func TestDynamoDBDiscover_ContextCancellationUnblocksSiblings(t *testing.T) {
	t.Parallel()
	const total = 5
	names := make([]string, total)
	tags := make(map[string][]dynamotypes.Tag, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("io-foo-%d", i)
		names[i] = name
		arn := fmt.Sprintf("arn:aws:dynamodb:us-east-1:123:table/%s", name)
		tags[arn] = []dynamotypes.Tag{tagPair("Project", "io-foo")}
	}
	release := make(chan struct{})
	starts := make(chan string, total)
	bc := &blockingDynamoClient{
		pages:   []dynamodb.ListTablesOutput{{TableNames: names}},
		tags:    tags,
		release: release,
		starts:  starts,
	}
	d := &dynamoDiscoverer{
		new:            func() dynamoClient { return bc },
		maxConcurrency: total,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(ctx, "io-foo", "us-east-1", "123")
		done <- err
	}()

	for i := 0; i < total; i++ {
		select {
		case <-starts:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d goroutines entered ListTagsOfResource before timeout", i)
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
