package cleanup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// findTerraform returns the path to the terraform binary, or skips the test.
func findTerraform(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform binary not found, skipping validate test")
	}
	return path
}

// setupValidateDir creates a temp dir with providers.tf and the given HCL,
// runs terraform init + validate, and returns any error.
func setupAndValidate(t *testing.T, tfBinary string, hcl string) error {
	t.Helper()
	dir := t.TempDir()

	providers := `terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}
`
	if err := os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(providers), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated.tf"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	// terraform init
	init := exec.CommandContext(context.Background(), tfBinary, "init", "-backend=false")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Logf("terraform init output:\n%s", out)
		return err
	}

	// terraform validate
	validate := exec.CommandContext(context.Background(), tfBinary, "validate")
	validate.Dir = dir
	if out, err := validate.CombinedOutput(); err != nil {
		t.Logf("terraform validate output:\n%s", out)
		return err
	}
	return nil
}

// TestValidate_CleanedSQS runs terraform validate against cleaned SQS HCL.
func TestValidate_CleanedSQS(t *testing.T) {
	tfBin := findTerraform(t)

	raw := `resource "aws_sqs_queue" "my_queue" {
  name                              = "my-queue"
  delay_seconds                     = 0
  max_message_size                  = 262144
  message_retention_seconds         = 345600
  visibility_timeout_seconds        = 30
  content_based_deduplication       = false
  fifo_queue                        = false
  kms_data_key_reuse_period_seconds = 300
  receive_wait_time_seconds         = 10
  sqs_managed_sse_enabled           = true
  arn                               = "arn:aws:sqs:us-east-1:123456789012:my-queue"
  url                               = "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
  id                                = "my-queue"
  tags_all                          = {}
  tags                              = { "Project" = "demo" }
}
`
	cleaned, err := CleanupGeneratedHCL([]byte(raw))
	if err != nil {
		t.Fatalf("cleanup error: %v", err)
	}

	if err := setupAndValidate(t, tfBin, string(cleaned)); err != nil {
		t.Fatalf("terraform validate failed on cleaned SQS: %v", err)
	}
}

// TestValidate_CleanedLambdaPlaceholder runs terraform validate against
// cleaned Lambda HCL where none of filename/image_uri/s3_bucket were set
// (common case for generate-config-out), so the fixup inserts placeholder.zip.
func TestValidate_CleanedLambdaPlaceholder(t *testing.T) {
	tfBin := findTerraform(t)

	raw := `resource "aws_lambda_function" "my_func" {
  function_name                  = "my-func"
  handler                        = "index.handler"
  runtime                        = "nodejs20.x"
  role                           = "arn:aws:iam::123456789012:role/my-role"
  arn                            = "arn:aws:lambda:us-east-1:123456789012:function:my-func"
  invoke_arn                     = "arn:aws:apigateway:us-east-1:..."
  last_modified                  = "2025-01-01T00:00:00.000+0000"
  version                        = "$LATEST"
  code_sha256                    = "abc123"
  source_code_size               = 1024
  qualified_arn                  = "arn:aws:lambda:us-east-1:123456789012:function:my-func:$LATEST"
  filename                       = null
  image_uri                      = null
  s3_bucket                      = null
  s3_key                         = null
  id                             = "my-func"
  tags_all                       = {}
  memory_size                    = 128
  timeout                        = 3
  reserved_concurrent_executions = -1
  package_type                   = "Zip"
}
`
	cleaned, err := CleanupGeneratedHCL([]byte(raw))
	if err != nil {
		t.Fatalf("cleanup error: %v", err)
	}

	if err := setupAndValidate(t, tfBin, string(cleaned)); err != nil {
		t.Fatalf("terraform validate failed on cleaned Lambda: %v", err)
	}
}

// TestValidate_CleanedDynamoDB runs terraform validate against cleaned DynamoDB HCL.
func TestValidate_CleanedDynamoDB(t *testing.T) {
	tfBin := findTerraform(t)

	raw := `resource "aws_dynamodb_table" "my_table" {
  name                        = "my-table"
  billing_mode                = "PAY_PER_REQUEST"
  hash_key                    = "pk"
  deletion_protection_enabled = false
  stream_enabled              = false
  table_class                 = "STANDARD"
  arn                         = "arn:aws:dynamodb:us-east-1:123456789012:table/my-table"
  stream_arn                  = ""
  stream_label                = ""
  id                          = "my-table"
  tags_all                    = {}
  read_capacity               = 0
  write_capacity              = 0

  attribute {
    name = "pk"
    type = "S"
  }
}
`
	cleaned, err := CleanupGeneratedHCL([]byte(raw))
	if err != nil {
		t.Fatalf("cleanup error: %v", err)
	}

	if err := setupAndValidate(t, tfBin, string(cleaned)); err != nil {
		t.Fatalf("terraform validate failed on cleaned DynamoDB: %v", err)
	}
}

// TestValidate_MultiResource validates a multi-resource output with
// cross-references, similar to what the full pipeline produces.
func TestValidate_MultiResource(t *testing.T) {
	tfBin := findTerraform(t)

	// Realistic multi-resource output — Lambda referencing an IAM role
	hcl := `resource "aws_iam_role" "lambda_exec" {
  name               = "my-lambda-exec"
  assume_role_policy = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"lambda.amazonaws.com\"},\"Action\":\"sts:AssumeRole\"}]}"
}

resource "aws_lambda_function" "handler" {
  function_name                  = "my-handler"
  handler                        = "index.handler"
  runtime                        = "nodejs20.x"
  role                           = aws_iam_role.lambda_exec.arn
  filename                       = "placeholder.zip"
  memory_size                    = 128
  timeout                        = 3
  reserved_concurrent_executions = -1
  package_type                   = "Zip"
}

resource "aws_sqs_queue" "main" {
  name                              = "my-queue"
  delay_seconds                     = 0
  max_message_size                  = 262144
  message_retention_seconds         = 345600
  visibility_timeout_seconds        = 30
  sqs_managed_sse_enabled           = true
}

resource "aws_sqs_queue" "dlq" {
  name                              = "my-queue-dlq"
  delay_seconds                     = 0
  max_message_size                  = 262144
  message_retention_seconds         = 1209600
  visibility_timeout_seconds        = 30
  sqs_managed_sse_enabled           = true
}
`
	if err := setupAndValidate(t, tfBin, hcl); err != nil {
		t.Fatalf("terraform validate failed on multi-resource output: %v", err)
	}
}

// TestValidate_FilteredImports verifies that FilterImportBlocks produces
// valid import blocks that pass terraform validate alongside their resources.
func TestValidate_FilteredImports(t *testing.T) {
	tfBin := findTerraform(t)

	generated := `resource "aws_sqs_queue" "my_queue" {
  name = "my-queue"
}
`
	// Import blocks — one matches (sqs_queue), one doesn't (iam_role)
	imports := `import {
  to = aws_sqs_queue.my_queue
  id = "https://sqs.us-east-1.amazonaws.com/123/my-queue"
}

import {
  to = aws_iam_role.missing_role
  id = "missing-role"
}
`
	filtered, err := FilterImportBlocks([]byte(imports), []byte(generated))
	if err != nil {
		t.Fatalf("FilterImportBlocks error: %v", err)
	}

	// Write both to a dir and validate
	dir := t.TempDir()
	providers := `terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 6.0" }
  }
}
provider "aws" { region = "us-east-1" }
`
	os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(providers), 0644)
	os.WriteFile(filepath.Join(dir, "generated.tf"), []byte(generated), 0644)
	os.WriteFile(filepath.Join(dir, "imports.tf"), filtered, 0644)

	init := exec.CommandContext(context.Background(), tfBin, "init", "-backend=false")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("terraform init failed:\n%s", out)
	}

	validate := exec.CommandContext(context.Background(), tfBin, "validate")
	validate.Dir = dir
	if out, err := validate.CombinedOutput(); err != nil {
		t.Fatalf("terraform validate failed with filtered imports:\n%s", out)
	}
}
