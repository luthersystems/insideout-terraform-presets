package importgen

import (
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

func TestGenerateImportBlocks(t *testing.T) {
	resources := []discovery.DiscoveredResource{
		{
			TerraformType: "aws_sqs_queue",
			ImportID:      "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue",
			Name:          "my-queue",
		},
		{
			TerraformType: "aws_dynamodb_table",
			ImportID:      "my-table",
			Name:          "my-table",
		},
	}

	got, err := GenerateImportBlocks(resources)
	if err != nil {
		t.Fatalf("GenerateImportBlocks() error = %v", err)
	}

	output := string(got)

	// Check that import blocks are present
	if !strings.Contains(output, "import {") {
		t.Error("output should contain 'import {' blocks")
	}
	if !strings.Contains(output, "aws_sqs_queue.my_queue") {
		t.Errorf("output should contain aws_sqs_queue.my_queue, got:\n%s", output)
	}
	if !strings.Contains(output, "aws_dynamodb_table.my_table") {
		t.Errorf("output should contain aws_dynamodb_table.my_table, got:\n%s", output)
	}
	if !strings.Contains(output, `"https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"`) {
		t.Error("output should contain the SQS queue URL as import ID")
	}
}

func TestGenerateImportBlocksDeduplication(t *testing.T) {
	resources := []discovery.DiscoveredResource{
		{TerraformType: "aws_sqs_queue", ImportID: "url1", Name: "my-queue"},
		{TerraformType: "aws_sqs_queue", ImportID: "url2", Name: "my-queue"},
	}

	got, err := GenerateImportBlocks(resources)
	if err != nil {
		t.Fatalf("GenerateImportBlocks() error = %v", err)
	}

	output := string(got)
	if !strings.Contains(output, "aws_sqs_queue.my_queue") {
		t.Error("output should contain first queue")
	}
	if !strings.Contains(output, "aws_sqs_queue.my_queue_1") {
		t.Errorf("output should contain deduplicated second queue, got:\n%s", output)
	}
}

func TestGenerateImportBlocksEmpty(t *testing.T) {
	got, err := GenerateImportBlocks(nil)
	if err != nil {
		t.Fatalf("GenerateImportBlocks() error = %v", err)
	}
	// Should produce valid but empty HCL
	if len(got) > 1 { // may have a trailing newline
		output := strings.TrimSpace(string(got))
		if output != "" {
			t.Errorf("expected empty output for nil resources, got: %q", output)
		}
	}
}

func TestSanitizedNames(t *testing.T) {
	resources := []discovery.DiscoveredResource{
		{Name: "io-buqiks112yag-queue"},
		{Name: "io-buqiks112yag-queue-dlq"},
		{Name: "/io-buqiks112yag-logs/app"},
	}

	names := SanitizedNames(resources)
	expected := []string{
		"io_buqiks112yag_queue",
		"io_buqiks112yag_queue_dlq",
		"_io_buqiks112yag_logs_app",
	}

	for i, want := range expected {
		if names[i] != want {
			t.Errorf("SanitizedNames()[%d] = %q, want %q", i, names[i], want)
		}
	}
}
