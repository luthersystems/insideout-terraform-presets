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

// errSeedListTables is the package-level sentinel returned by the fake
// DynamoDB client in tests that want to assert ListTables error
// propagation. Tests should use errors.Is(err, errSeedListTables)
// rather than asserting only on `err != nil` — the latter masks
// regressions where the discover layer silently swallows the SDK
// error and returns a different one.
var errSeedListTables = errors.New("AccessDenied")

type fakeDynamoClient struct {
	pages    []dynamodb.ListTablesOutput
	tagsByID map[string][]dynamotypes.Tag
	tagsErr  map[string]error

	mu        sync.Mutex
	listCalls []dynamodb.ListTablesInput
	tagCalls  []string
	listErr   error

	// DescribeTable wiring for DiscoverByID tests.
	describeByName     map[string]*dynamotypes.TableDescription
	describeErr        error
	describeCalls      []string
	describeReturnsErr bool
}

func (f *fakeDynamoClient) ListTables(_ context.Context, in *dynamodb.ListTablesInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
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

func (f *fakeDynamoClient) DescribeTable(_ context.Context, in *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	name := aws.ToString(in.TableName)
	f.mu.Lock()
	f.describeCalls = append(f.describeCalls, name)
	f.mu.Unlock()
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if td, ok := f.describeByName[name]; ok {
		return &dynamodb.DescribeTableOutput{Table: td}, nil
	}
	return nil, &dynamotypes.ResourceNotFoundException{}
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
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
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
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient {
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
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
}

func TestDynamoDBDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient {
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
			d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return fake }}
			got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{tc.region}, AccountID: tc.accountID})
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
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient {
		return &fakeDynamoClient{listErr: errSeedListTables}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errSeedListTables) {
		t.Errorf("err=%v, want errors.Is(err, errSeedListTables) — discover swallowed the SDK error", err)
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

// DescribeTable is unused by the concurrency tests but required to
// satisfy the dynamoClient interface; the DiscoverByID code path is
// covered by fakeDynamoClient.
func (c *blockingDynamoClient) DescribeTable(_ context.Context, _ *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	return nil, errors.New("blockingDynamoClient.DescribeTable: not used in concurrency tests")
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
		new:            func(_ string) dynamoClient { return bc },
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
		new:            func(_ string) dynamoClient { return bc },
		maxConcurrency: total,
	}

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

func TestDynamoDBDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:dynamodb:us-east-1:123:table/io-foo-orders"
	fake := &fakeDynamoClient{
		describeByName: map[string]*dynamotypes.TableDescription{
			"io-foo-orders": {TableArn: aws.String(arn), TableName: aws.String("io-foo-orders")},
		},
	}
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), arn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_dynamodb_table" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-orders" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] != arn {
		t.Errorf("NativeIDs[arn]=%q, want %q", got.Identity.NativeIDs["arn"], arn)
	}
}

func TestDynamoDBDiscoverByID_AcceptsBareName(t *testing.T) {
	t.Parallel()
	fake := &fakeDynamoClient{
		describeByName: map[string]*dynamotypes.TableDescription{
			"orders": {TableName: aws.String("orders")},
		},
	}
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "orders", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NameHint != "orders" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	// arn is constructed locally when DescribeTable does not echo it back.
	if got.Identity.NativeIDs["arn"] != "arn:aws:dynamodb:us-east-1:123:table/orders" {
		t.Errorf("NativeIDs[arn]=%q (expected synthesized)", got.Identity.NativeIDs["arn"])
	}
}

func TestDynamoDBDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	fake := &fakeDynamoClient{} // empty describeByName triggers ResourceNotFoundException
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return fake }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestDynamoDBDiscover_EmitsServiceStartFinish_PerRegion (#295) is the
// dynamodb mirror of TestSQSDiscover_EmitsServiceStartFinish_PerRegion.
// DynamoDB is the most operationally significant per-service emitter test
// because the discoverer fans out per-table tag fetches under an
// errgroup — concurrent ItemFound emits race with one another, and the
// progress.JSONEmitter mutex is the only thing keeping the on-the-wire
// output line-safe. Asserting the per-region brackets here keeps that
// path covered by a unit test (the live race-detector run on
// progress.JSONEmitter covers the line-safety side).
func TestDynamoDBDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeDynamoClient{
		"us-east-1": {
			pages: []dynamodb.ListTablesOutput{{TableNames: []string{"io-foo-east"}}},
			tagsByID: map[string][]dynamotypes.Tag{
				"arn:aws:dynamodb:us-east-1:123:table/io-foo-east": {tagPair("Project", "io-foo")},
			},
		},
		"eu-west-1": {
			pages: []dynamodb.ListTablesOutput{{TableNames: []string{"io-foo-west"}}},
			tagsByID: map[string][]dynamotypes.Tag{
				"arn:aws:dynamodb:eu-west-1:123:table/io-foo-west": {tagPair("Project", "io-foo")},
			},
		},
	}
	d := &dynamoDiscoverer{new: func(region string) dynamoClient { return fakes[region] }, maxConcurrency: 4}
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
			if e.Service != "dynamodb" {
				t.Errorf("service_start.service=%q, want dynamodb", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "dynamodb" {
				t.Errorf("service_finish.service=%q, want dynamodb", e.Service)
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

// TestDynamoDBDiscover_EmitsItemFound_PerTable (#295) pins one
// item_found per emitted ImportedResource. Tables that fail the
// Project=<project> back-compat tag check (TestDynamoDBDiscover_PrefixThenTagFilter
// pattern) must not emit item_found.
func TestDynamoDBDiscover_EmitsItemFound_PerTable(t *testing.T) {
	t.Parallel()
	fake := &fakeDynamoClient{
		pages: []dynamodb.ListTablesOutput{
			{TableNames: []string{"io-foo-a", "io-foo-b", "io-foo-untagged"}},
		},
		tagsByID: map[string][]dynamotypes.Tag{
			"arn:aws:dynamodb:us-east-1:123:table/io-foo-a":        {tagPair("Project", "io-foo")},
			"arn:aws:dynamodb:us-east-1:123:table/io-foo-b":        {tagPair("Project", "io-foo")},
			"arn:aws:dynamodb:us-east-1:123:table/io-foo-untagged": {tagPair("Owner", "team")},
		},
	}
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return fake }, maxConcurrency: 4}
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
	wantNames := map[string]bool{"io-foo-a": true, "io-foo-b": true}
	for _, it := range items {
		if it.Service != "dynamodb" {
			t.Errorf("item.service=%q, want dynamodb", it.Service)
		}
		if it.TFType != "aws_dynamodb_table" {
			t.Errorf("item.tf_type=%q, want aws_dynamodb_table", it.TFType)
		}
		if !wantNames[it.ImportID] {
			t.Errorf("item.import_id=%q not in expected set", it.ImportID)
		}
	}
}

func TestDynamoDBDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &dynamoDiscoverer{new: func(_ string) dynamoClient { return &fakeDynamoClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:dynamodb:us-east-1:123:stream/io-foo/abc", // not a table arn
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
