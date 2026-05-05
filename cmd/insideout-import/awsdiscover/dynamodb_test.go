package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDynamoClient struct {
	pages    []dynamodb.ListTablesOutput
	tagsByID map[string][]dynamotypes.Tag
	tagsErr  map[string]error

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
	f.tagCalls = append(f.tagCalls, arn)
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
