package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type fakeSQSClient struct {
	pages []sqs.ListQueuesOutput
	calls []sqs.ListQueuesInput
	err   error // when non-nil, every ListQueues call returns this
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

func ptr(s string) *string { return &s }

func TestSQSDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &sqsDiscoverer{new: func() sqsClient {
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
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
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
	d := &sqsDiscoverer{new: func() sqsClient {
		return &fakeSQSClient{
			pages: []sqs.ListQueuesOutput{
				{QueueUrls: []string{"https://example/io-foo-a"}, NextToken: ptr("tok1")},
				{QueueUrls: []string{"https://example/io-foo-b"}, NextToken: ptr("tok2")},
				{QueueUrls: []string{"https://example/io-foo-c"}}, // terminal
			},
		}
	}}
	got, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
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
	d := &sqsDiscoverer{new: func() sqsClient { return fake }}
	if _, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123"); err != nil {
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
	d := &sqsDiscoverer{new: func() sqsClient { return fake }}
	if _, err := d.Discover(context.Background(), "", "us-east-1", "123"); err != nil {
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
	d := &sqsDiscoverer{new: func() sqsClient {
		return &fakeSQSClient{err: errors.New("AccessDenied")}
	}}
	_, err := d.Discover(context.Background(), "io-foo", "us-east-1", "123")
	if err == nil {
		t.Fatal("expected error")
	}
}
