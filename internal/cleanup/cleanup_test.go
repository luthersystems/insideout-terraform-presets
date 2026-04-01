package cleanup

import (
	"strings"
	"testing"
)

func TestCleanupGeneratedHCL(t *testing.T) {
	input := `resource "aws_sqs_queue" "my_queue" {
  name                       = "my-queue"
  delay_seconds              = 0
  max_message_size           = 262144
  message_retention_seconds  = 345600
  visibility_timeout_seconds = 30
  arn                        = "arn:aws:sqs:us-east-1:123456789012:my-queue"
  url                        = "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
  id                         = "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
  tags_all                   = {}
  tags                       = { "Project" = "demo" }
}
`
	got, err := CleanupGeneratedHCL([]byte(input))
	if err != nil {
		t.Fatalf("CleanupGeneratedHCL() error = %v", err)
	}

	output := string(got)

	// Should keep
	if !strings.Contains(output, `name`) {
		t.Error("should keep 'name' attribute")
	}
	if !strings.Contains(output, `delay_seconds`) {
		t.Error("should keep 'delay_seconds' attribute")
	}
	if !strings.Contains(output, `tags`) {
		t.Error("should keep 'tags' attribute")
	}

	// Should remove
	if strings.Contains(output, `arn =`) || strings.Contains(output, `arn  `) {
		t.Error("should remove 'arn' attribute")
	}
	if strings.Contains(output, `url =`) || strings.Contains(output, `url  `) {
		t.Error("should remove 'url' attribute")
	}
	if strings.Contains(output, `tags_all`) {
		t.Error("should remove 'tags_all' attribute")
	}
	if strings.Contains(output, `id =`) || strings.Contains(output, `id  `) {
		t.Error("should remove 'id' attribute")
	}
}

func TestCleanupDynamoDB(t *testing.T) {
	input := `resource "aws_dynamodb_table" "my_table" {
  name           = "my-table"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "id"
  arn            = "arn:aws:dynamodb:us-east-1:123456789012:table/my-table"
  stream_arn     = ""
  stream_label   = ""
  id             = "my-table"
  tags_all       = {}

  attribute {
    name = "id"
    type = "S"
  }
}
`
	got, err := CleanupGeneratedHCL([]byte(input))
	if err != nil {
		t.Fatalf("CleanupGeneratedHCL() error = %v", err)
	}

	output := string(got)
	if !strings.Contains(output, `name`) {
		t.Error("should keep 'name'")
	}
	if !strings.Contains(output, `hash_key`) {
		t.Error("should keep 'hash_key'")
	}
	if strings.Contains(output, "stream_arn") {
		t.Error("should remove 'stream_arn'")
	}
	if strings.Contains(output, "stream_label") {
		t.Error("should remove 'stream_label'")
	}
}

func TestCleanupLambda(t *testing.T) {
	input := `resource "aws_lambda_function" "my_func" {
  function_name = "my-func"
  handler       = "index.handler"
  runtime       = "nodejs18.x"
  role          = "arn:aws:iam::123456789012:role/lambda-role"
  arn           = "arn:aws:lambda:us-east-1:123456789012:function:my-func"
  invoke_arn    = "arn:aws:apigateway:us-east-1:lambda:path/functions/..."
  last_modified = "2024-01-01T00:00:00.000+0000"
  version       = "$LATEST"
  code_sha256   = "abc123"
  id            = "my-func"
  tags_all      = {}
}
`
	got, err := CleanupGeneratedHCL([]byte(input))
	if err != nil {
		t.Fatalf("CleanupGeneratedHCL() error = %v", err)
	}

	output := string(got)
	if !strings.Contains(output, "function_name") {
		t.Error("should keep 'function_name'")
	}
	if !strings.Contains(output, "handler") {
		t.Error("should keep 'handler'")
	}
	if !strings.Contains(output, "role") {
		t.Error("should keep 'role'")
	}
	for _, removed := range []string{"invoke_arn", "last_modified", "version", "code_sha256"} {
		if strings.Contains(output, removed) {
			t.Errorf("should remove '%s'", removed)
		}
	}
}

func TestCleanupPreservesNonResourceBlocks(t *testing.T) {
	input := `terraform {
  required_version = ">= 1.5"
}

resource "aws_sqs_queue" "q" {
  name = "q"
  arn  = "arn:..."
  id   = "q"
}
`
	got, err := CleanupGeneratedHCL([]byte(input))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	output := string(got)
	if !strings.Contains(output, "terraform") {
		t.Error("should preserve terraform block")
	}
}

func TestCleanupUnknownResourceType(t *testing.T) {
	input := `resource "aws_unknown_thing" "x" {
  name = "x"
  arn  = "arn:..."
  id   = "x-123"
  tags_all = {}
}
`
	got, err := CleanupGeneratedHCL([]byte(input))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	output := string(got)
	// Should still remove universal attrs (id, tags_all)
	if strings.Contains(output, "tags_all") {
		t.Error("should remove tags_all even for unknown types")
	}
	// But keep arn since it's not in the computed list for this type
	if !strings.Contains(output, "arn") {
		t.Error("should keep arn for unknown resource types")
	}
}
