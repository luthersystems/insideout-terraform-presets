package importgen

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

// parseImportBlocks parses generated HCL and returns import blocks as
// map[to_address]import_id for assertion.
func parseImportBlocks(t *testing.T, src []byte) map[string]string {
	t.Helper()
	f, diags := hclwrite.ParseConfig(src, "imports.tf", hcl.Pos{})
	if diags.HasErrors() {
		t.Fatalf("failed to parse import blocks: %s", diags.Error())
	}
	result := make(map[string]string)
	for _, block := range f.Body().Blocks() {
		if block.Type() != "import" {
			continue
		}
		toAttr := block.Body().GetAttribute("to")
		idAttr := block.Body().GetAttribute("id")
		if toAttr == nil || idAttr == nil {
			t.Error("import block missing 'to' or 'id' attribute")
			continue
		}
		toVal := string(toAttr.Expr().BuildTokens(nil).Bytes())
		idVal := string(idAttr.Expr().BuildTokens(nil).Bytes())
		// Trim whitespace and quotes from id value
		idVal = trimHCLString(idVal)
		toVal = trimHCLIdent(toVal)
		result[toVal] = idVal
	}
	return result
}

func trimHCLString(s string) string {
	// Remove surrounding quotes and whitespace
	s = trimWhitespace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func trimHCLIdent(s string) string {
	return trimWhitespace(s)
}

func trimWhitespace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

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

	blocks := parseImportBlocks(t, got)

	// Verify exact pairings
	sqsID, ok := blocks["aws_sqs_queue.my_queue"]
	if !ok {
		t.Fatal("missing import block for aws_sqs_queue.my_queue")
	}
	if sqsID != "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue" {
		t.Errorf("SQS import ID = %q, want queue URL", sqsID)
	}

	ddbID, ok := blocks["aws_dynamodb_table.my_table"]
	if !ok {
		t.Fatal("missing import block for aws_dynamodb_table.my_table")
	}
	if ddbID != "my-table" {
		t.Errorf("DynamoDB import ID = %q, want %q", ddbID, "my-table")
	}

	if len(blocks) != 2 {
		t.Errorf("expected 2 import blocks, got %d", len(blocks))
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

	blocks := parseImportBlocks(t, got)

	// Verify first gets original name, second gets deduplicated name
	if id, ok := blocks["aws_sqs_queue.my_queue"]; !ok {
		t.Error("missing import block for aws_sqs_queue.my_queue")
	} else if id != "url1" {
		t.Errorf("first queue should have id %q, got %q", "url1", id)
	}

	if id, ok := blocks["aws_sqs_queue.my_queue_1"]; !ok {
		t.Error("missing import block for aws_sqs_queue.my_queue_1")
	} else if id != "url2" {
		t.Errorf("second queue should have id %q, got %q", "url2", id)
	}
}

func TestGenerateImportBlocksEmpty(t *testing.T) {
	got, err := GenerateImportBlocks(nil)
	if err != nil {
		t.Fatalf("GenerateImportBlocks() error = %v", err)
	}
	blocks := parseImportBlocks(t, got)
	if len(blocks) != 0 {
		t.Errorf("expected 0 import blocks for nil input, got %d", len(blocks))
	}
}

func TestSanitizedNames(t *testing.T) {
	resources := []discovery.DiscoveredResource{
		{Name: "io-buqiks112yag-queue"},
		{Name: "io-buqiks112yag-queue-dlq"},
		{Name: "/io-buqiks112yag-logs/app"},
	}

	names := SanitizedNames(resources)

	if len(names) != len(resources) {
		t.Fatalf("SanitizedNames() returned %d names, want %d", len(names), len(resources))
	}

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

func TestResourceAddress(t *testing.T) {
	got := ResourceAddress("aws_sqs_queue", "my_queue")
	if got != "aws_sqs_queue.my_queue" {
		t.Errorf("ResourceAddress() = %q, want %q", got, "aws_sqs_queue.my_queue")
	}
}
