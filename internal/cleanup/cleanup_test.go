package cleanup

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// parseHCLResource parses HCL and returns the body of the first resource block.
func parseHCLResource(t *testing.T, src []byte) *hclwrite.Body {
	t.Helper()
	f, diags := hclwrite.ParseConfig(src, "test.tf", hcl.Pos{})
	if diags.HasErrors() {
		t.Fatalf("failed to parse output HCL: %s", diags.Error())
	}
	for _, block := range f.Body().Blocks() {
		if block.Type() == "resource" {
			return block.Body()
		}
	}
	t.Fatal("no resource block found in output")
	return nil
}

func TestCleanupGeneratedHCL_SQS(t *testing.T) {
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
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("CleanupGeneratedHCL() error = %v", err)
	}

	body := parseHCLResource(t, got)

	// Should keep
	for _, keep := range []string{"name", "delay_seconds", "max_message_size", "tags", "visibility_timeout_seconds"} {
		if body.GetAttribute(keep) == nil {
			t.Errorf("should keep %q attribute", keep)
		}
	}

	// Should remove
	for _, remove := range []string{"arn", "url", "id", "tags_all"} {
		if body.GetAttribute(remove) != nil {
			t.Errorf("should remove %q attribute", remove)
		}
	}
}

func TestCleanupGeneratedHCL_DynamoDB(t *testing.T) {
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
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("CleanupGeneratedHCL() error = %v", err)
	}

	body := parseHCLResource(t, got)

	for _, keep := range []string{"name", "billing_mode", "hash_key"} {
		if body.GetAttribute(keep) == nil {
			t.Errorf("should keep %q", keep)
		}
	}
	for _, remove := range []string{"arn", "stream_arn", "stream_label", "id", "tags_all"} {
		if body.GetAttribute(remove) != nil {
			t.Errorf("should remove %q", remove)
		}
	}
	// Verify nested attribute block is preserved
	if len(body.Blocks()) == 0 {
		t.Error("should preserve nested 'attribute' block")
	}
}

func TestCleanupGeneratedHCL_Lambda(t *testing.T) {
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
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("CleanupGeneratedHCL() error = %v", err)
	}

	body := parseHCLResource(t, got)

	for _, keep := range []string{"function_name", "handler", "runtime", "role"} {
		if body.GetAttribute(keep) == nil {
			t.Errorf("should keep %q", keep)
		}
	}
	for _, remove := range []string{"arn", "invoke_arn", "last_modified", "version", "code_sha256", "id", "tags_all"} {
		if body.GetAttribute(remove) != nil {
			t.Errorf("should remove %q", remove)
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
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	f, diags := hclwrite.ParseConfig(got, "test.tf", hcl.Pos{})
	if diags.HasErrors() {
		t.Fatalf("parse error: %s", diags.Error())
	}

	hasTerraform := false
	hasResource := false
	for _, block := range f.Body().Blocks() {
		switch block.Type() {
		case "terraform":
			hasTerraform = true
		case "resource":
			hasResource = true
		}
	}
	if !hasTerraform {
		t.Error("should preserve terraform block")
	}
	if !hasResource {
		t.Error("should preserve resource block")
	}
}

func TestCleanupUnknownResourceType(t *testing.T) {
	input := `resource "aws_unknown_thing" "x" {
  name     = "x"
  arn      = "arn:..."
  id       = "x-123"
  tags_all = {}
}
`
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	body := parseHCLResource(t, got)

	// Universal attrs should be removed
	if body.GetAttribute("tags_all") != nil {
		t.Error("should remove tags_all even for unknown types")
	}
	if body.GetAttribute("id") != nil {
		t.Error("should remove id even for unknown types")
	}
	// arn is NOT in universal list, so should be kept for unknown types
	if body.GetAttribute("arn") == nil {
		t.Error("should keep arn for unknown resource types")
	}
	if body.GetAttribute("name") == nil {
		t.Error("should keep name for unknown resource types")
	}
}

// --- Lambda fixup tests (all 4 branches) ---

func TestFixupLambda_NoneSet_InsertsPlaceholder(t *testing.T) {
	input := `resource "aws_lambda_function" "f" {
  function_name = "my-func"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  role          = "arn:aws:iam::123:role/r"
  filename      = null
  image_uri     = null
  s3_bucket     = null
  s3_key        = null
}
`
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	body := parseHCLResource(t, got)

	// Should add placeholder filename
	if body.GetAttribute("filename") == nil {
		t.Fatal("should set filename to placeholder.zip when none specified")
	}
	// Should remove the other code source attrs
	for _, removed := range []string{"image_uri", "s3_bucket", "s3_key"} {
		if body.GetAttribute(removed) != nil {
			t.Errorf("should remove %q when using placeholder filename", removed)
		}
	}
}

func TestFixupLambda_S3BucketSet_KeepsS3(t *testing.T) {
	input := `resource "aws_lambda_function" "f" {
  function_name      = "my-func"
  handler            = "index.handler"
  runtime            = "nodejs20.x"
  role               = "arn:aws:iam::123:role/r"
  s3_bucket          = "my-deploy-bucket"
  s3_key             = "lambda/v1.zip"
  s3_object_version  = "abc123"
  filename           = null
  image_uri          = null
}
`
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	body := parseHCLResource(t, got)

	// Should keep S3 attrs
	if body.GetAttribute("s3_bucket") == nil {
		t.Error("should keep s3_bucket")
	}
	if body.GetAttribute("s3_key") == nil {
		t.Error("should keep s3_key")
	}
	// Should remove filename and image_uri
	if body.GetAttribute("filename") != nil {
		t.Error("should remove filename when s3_bucket is set")
	}
	if body.GetAttribute("image_uri") != nil {
		t.Error("should remove image_uri when s3_bucket is set")
	}
}

func TestFixupLambda_ImageURISet_KeepsImage(t *testing.T) {
	input := `resource "aws_lambda_function" "f" {
  function_name = "my-func"
  handler       = null
  runtime       = null
  role          = "arn:aws:iam::123:role/r"
  image_uri     = "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-image:latest"
  filename      = null
  s3_bucket     = null
  s3_key        = null
}
`
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	body := parseHCLResource(t, got)

	if body.GetAttribute("image_uri") == nil {
		t.Error("should keep image_uri")
	}
	for _, removed := range []string{"filename", "s3_bucket", "s3_key"} {
		if body.GetAttribute(removed) != nil {
			t.Errorf("should remove %q when image_uri is set", removed)
		}
	}
}

func TestFixupLambda_FilenameSet_KeepsFilename(t *testing.T) {
	input := `resource "aws_lambda_function" "f" {
  function_name = "my-func"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  role          = "arn:aws:iam::123:role/r"
  filename      = "deploy.zip"
  image_uri     = null
  s3_bucket     = null
}
`
	got, err := CleanupGeneratedHCL([]byte(input), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	body := parseHCLResource(t, got)

	if body.GetAttribute("filename") == nil {
		t.Error("should keep filename")
	}
	for _, removed := range []string{"image_uri", "s3_bucket"} {
		if body.GetAttribute(removed) != nil {
			t.Errorf("should remove %q when filename is set", removed)
		}
	}
}
