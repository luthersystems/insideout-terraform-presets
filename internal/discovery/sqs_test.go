package discovery

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type mockSQS struct {
	listQueuesPages    []sqs.ListQueuesOutput
	listQueueTagsResp  map[string]*sqs.ListQueueTagsOutput
	getQueueAttrsResp  map[string]*sqs.GetQueueAttributesOutput
	listQueuesErr      error
	listQueueTagsErr   error
	getQueueAttrsErr   error
	listQueuesPageIdx  int
}

func (m *mockSQS) ListQueues(_ context.Context, _ *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	if m.listQueuesErr != nil {
		return nil, m.listQueuesErr
	}
	if m.listQueuesPageIdx >= len(m.listQueuesPages) {
		return &sqs.ListQueuesOutput{}, nil
	}
	page := m.listQueuesPages[m.listQueuesPageIdx]
	m.listQueuesPageIdx++
	return &page, nil
}

func (m *mockSQS) ListQueueTags(_ context.Context, input *sqs.ListQueueTagsInput, _ ...func(*sqs.Options)) (*sqs.ListQueueTagsOutput, error) {
	if m.listQueueTagsErr != nil {
		return nil, m.listQueueTagsErr
	}
	if resp, ok := m.listQueueTagsResp[aws.ToString(input.QueueUrl)]; ok {
		return resp, nil
	}
	return &sqs.ListQueueTagsOutput{}, nil
}

func (m *mockSQS) GetQueueAttributes(_ context.Context, input *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if m.getQueueAttrsErr != nil {
		return nil, m.getQueueAttrsErr
	}
	if resp, ok := m.getQueueAttrsResp[aws.ToString(input.QueueUrl)]; ok {
		return resp, nil
	}
	return &sqs.GetQueueAttributesOutput{Attributes: map[string]string{}}, nil
}

func TestSQSDiscoverer_Discover(t *testing.T) {
	queueURL := "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue"
	dlqURL := "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue-dlq"

	mock := &mockSQS{
		listQueuesPages: []sqs.ListQueuesOutput{
			{QueueUrls: []string{queueURL, dlqURL}},
		},
		listQueueTagsResp: map[string]*sqs.ListQueueTagsOutput{
			queueURL: {Tags: map[string]string{"Project": "my-project"}},
			dlqURL:   {Tags: map[string]string{"Project": "my-project"}},
		},
		getQueueAttrsResp: map[string]*sqs.GetQueueAttributesOutput{
			queueURL: {Attributes: map[string]string{
				string(sqstypes.QueueAttributeNameQueueArn): "arn:aws:sqs:us-east-1:123456789012:my-project-queue",
			}},
			dlqURL: {Attributes: map[string]string{
				string(sqstypes.QueueAttributeNameQueueArn): "arn:aws:sqs:us-east-1:123456789012:my-project-queue-dlq",
			}},
		},
	}

	d := &SQSDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}

	if resources[0].Name != "my-project-queue" {
		t.Errorf("resources[0].Name = %q, want %q", resources[0].Name, "my-project-queue")
	}
	if resources[0].ImportID != queueURL {
		t.Errorf("resources[0].ImportID = %q, want queue URL", resources[0].ImportID)
	}
	if resources[0].ARN != "arn:aws:sqs:us-east-1:123456789012:my-project-queue" {
		t.Errorf("resources[0].ARN = %q", resources[0].ARN)
	}
	if resources[0].TerraformType != "aws_sqs_queue" {
		t.Errorf("resources[0].TerraformType = %q", resources[0].TerraformType)
	}
	if resources[1].Name != "my-project-queue-dlq" {
		t.Errorf("resources[1].Name = %q", resources[1].Name)
	}
}

func TestSQSDiscoverer_TagFilter(t *testing.T) {
	queueURL := "https://sqs.us-east-1.amazonaws.com/123456789012/q1"

	mock := &mockSQS{
		listQueuesPages: []sqs.ListQueuesOutput{
			{QueueUrls: []string{queueURL}},
		},
		listQueueTagsResp: map[string]*sqs.ListQueueTagsOutput{
			queueURL: {Tags: map[string]string{"env": "staging"}},
		},
		getQueueAttrsResp: map[string]*sqs.GetQueueAttributesOutput{
			queueURL: {Attributes: map[string]string{}},
		},
	}

	d := &SQSDiscoverer{client: mock}

	// Filter for env=production should exclude the staging queue
	resources, err := d.Discover(context.Background(), Filter{
		Tags: map[string]string{"env": "production"},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources with mismatched tags, got %d", len(resources))
	}
}

func TestSQSDiscoverer_APIError(t *testing.T) {
	mock := &mockSQS{
		listQueuesErr: fmt.Errorf("access denied"),
	}

	d := &SQSDiscoverer{client: mock}
	_, err := d.Discover(context.Background(), Filter{})
	if err == nil {
		t.Fatal("expected error from API failure")
	}
}

func TestQueueNameFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://sqs.us-east-1.amazonaws.com/123456789012/my-queue", "my-queue"},
		{"https://sqs.us-east-1.amazonaws.com/123456789012/my-queue-dlq", "my-queue-dlq"},
		{"simple-name", "simple-name"},
	}
	for _, tt := range tests {
		got := queueNameFromURL(tt.url)
		if got != tt.want {
			t.Errorf("queueNameFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
