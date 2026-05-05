package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type fakeLambdaClient struct {
	pages    []lambda.ListFunctionsOutput
	tagsByID map[string]map[string]string
	tagsErr  map[string]error // errors keyed by ARN

	listCalls int
	tagCalls  []string
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
	f.tagCalls = append(f.tagCalls, arn)
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &lambda.ListTagsOutput{Tags: f.tagsByID[arn]}, nil
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
