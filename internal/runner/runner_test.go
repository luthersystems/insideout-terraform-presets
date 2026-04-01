package runner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

// mockDiscoverer returns canned discovery results.
type mockDiscoverer struct {
	resources []discovery.DiscoveredResource
	err       error
}

func (m *mockDiscoverer) Discover(_ context.Context) ([]discovery.DiscoveredResource, error) {
	return m.resources, m.err
}

// mockTF simulates terraform operations by writing fixture HCL files.
// It tracks call count to return different HCL on successive PlanGenerateConfig
// calls (simulating the dependency chase loop where terraform generates more
// resources on each iteration).
type mockTF struct {
	workDir        string
	initErr        error
	validateErr    error
	generatedPages []string // HCL to write on successive PlanGenerateConfig calls
	planCallCount  int
}

func (m *mockTF) SetWorkDir(dir string) { m.workDir = dir }

func (m *mockTF) Init(_ context.Context) error {
	return m.initErr
}

func (m *mockTF) PlanGenerateConfig(_ context.Context, outFile string) error {
	if m.planCallCount >= len(m.generatedPages) {
		return nil
	}
	hcl := m.generatedPages[m.planCallCount]
	m.planCallCount++
	return os.WriteFile(filepath.Join(m.workDir, outFile), []byte(hcl), 0644)
}

func (m *mockTF) Validate(_ context.Context) error {
	return m.validateErr
}

func (m *mockTF) ProvidersSchema(_ context.Context) (*tfjson.ProviderSchemas, error) {
	return nil, nil // tests use fallback cleanup path
}

func (m *mockTF) PlanJSON(_ context.Context) (*tfjson.Plan, error) {
	// Return empty plan (no drift) — drift-fix pass sees no changes
	return &tfjson.Plan{}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRunner_DryRun(t *testing.T) {
	r := New(Config{
		Project: "test-project",
		Region:  "us-east-1",
		DryRun:  true,
	}, testLogger())

	r.discoverer = &mockDiscoverer{
		resources: []discovery.DiscoveredResource{
			{TerraformType: "aws_sqs_queue", ImportID: "url1", Name: "queue1"},
			{TerraformType: "aws_dynamodb_table", ImportID: "table1", Name: "table1"},
		},
	}

	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.DiscoveredCount != 2 {
		t.Errorf("DiscoveredCount = %d, want 2", result.DiscoveredCount)
	}
	if result.ImportedCount != 0 {
		t.Errorf("ImportedCount = %d, want 0 (dry run)", result.ImportedCount)
	}
}

func TestRunner_NoResources(t *testing.T) {
	r := New(Config{
		Project: "empty-project",
		Region:  "us-east-1",
	}, testLogger())

	r.discoverer = &mockDiscoverer{resources: nil}

	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.DiscoveredCount != 0 {
		t.Errorf("DiscoveredCount = %d, want 0", result.DiscoveredCount)
	}
}

func TestRunner_DiscoveryError(t *testing.T) {
	r := New(Config{
		Project: "test",
		Region:  "us-east-1",
	}, testLogger())

	r.discoverer = &mockDiscoverer{
		err: context.DeadlineExceeded,
	}

	_, err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from discovery failure")
	}
}

// TestRunner_DependencyChase exercises the full pipeline with realistic
// terraform output. The fixture HCL is based on actual terraform
// -generate-config-out output from project io-buqiks112yag.
//
// Flow:
//   1. Discovery finds a Lambda function and an SQS queue
//   2. Terraform generates HCL with hardcoded IAM role ARN in the Lambda
//   3. Cleanup strips computed attrs, cross-ref resolves the SQS queue ARN
//   4. Dependency chaser finds the IAM role ARN as unresolved
//   5. New import block generated for aws_iam_role
//   6. Terraform generates HCL for the IAM role (iteration 2)
//   7. No more unresolved deps → done
//   8. Final output has cross-references and no computed attrs
func TestRunner_DependencyChase(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "output")

	// Iteration 1: terraform generates HCL for Lambda + SQS.
	// This is modeled on real terraform output — includes computed attrs
	// (arn, id, tags_all) that cleanup will strip, and a hardcoded IAM role
	// ARN that dependency chasing will discover.
	iteration1HCL := `# __generated__ by Terraform
resource "aws_lambda_function" "my_project_handler" {
  architectures                  = ["x86_64"]
  arn                            = "arn:aws:lambda:us-east-1:123456789012:function:my-project-handler"
  code_signing_config_arn        = null
  description                    = null
  filename                       = null
  function_name                  = "my-project-handler"
  handler                        = "index.handler"
  id                             = "my-project-handler"
  image_uri                      = null
  invoke_arn                     = "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:123456789012:function:my-project-handler/invocations"
  kms_key_arn                    = null
  last_modified                  = "2025-01-15T10:30:00.000+0000"
  layers                         = []
  memory_size                    = 256
  package_type                   = "Zip"
  qualified_arn                  = "arn:aws:lambda:us-east-1:123456789012:function:my-project-handler:$LATEST"
  reserved_concurrent_executions = -1
  role                           = "arn:aws:iam::123456789012:role/my-project-lambda-exec"
  runtime                        = "nodejs20.x"
  s3_bucket                      = null
  s3_key                         = null
  s3_object_version              = null
  skip_destroy                   = false
  source_code_size               = 1024
  tags = {
    Project = "my-project"
    Component = "lambda"
  }
  tags_all = {
    Project = "my-project"
    Component = "lambda"
  }
  timeout     = 30
  version     = "$LATEST"
  code_sha256 = "abc123def456"
  tracing_config {
    mode = "PassThrough"
  }
  logging_config {
    log_format = "Text"
    log_group  = "/aws/lambda/my-project-handler"
  }
}

resource "aws_sqs_queue" "my_project_queue" {
  arn                               = "arn:aws:sqs:us-east-1:123456789012:my-project-queue"
  content_based_deduplication       = false
  delay_seconds                     = 0
  fifo_queue                        = false
  id                                = "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue"
  kms_data_key_reuse_period_seconds = 300
  max_message_size                  = 262144
  message_retention_seconds         = 345600
  name                              = "my-project-queue"
  receive_wait_time_seconds         = 10
  redrive_policy                    = "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:123456789012:my-project-queue-dlq\",\"maxReceiveCount\":3}"
  sqs_managed_sse_enabled           = true
  tags = {
    Project = "my-project"
  }
  tags_all = {
    Project = "my-project"
  }
  url                        = "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue"
  visibility_timeout_seconds = 30
}
`

	// Iteration 2: terraform generates HCL for the chased IAM role dependency.
	// This is what terraform would produce when importing aws_iam_role.
	iteration2HCL := `# __generated__ by Terraform
resource "aws_lambda_function" "my_project_handler" {
  architectures                  = ["x86_64"]
  arn                            = "arn:aws:lambda:us-east-1:123456789012:function:my-project-handler"
  code_signing_config_arn        = null
  description                    = null
  filename                       = null
  function_name                  = "my-project-handler"
  handler                        = "index.handler"
  id                             = "my-project-handler"
  image_uri                      = null
  invoke_arn                     = "arn:aws:apigateway:us-east-1:lambda:path/..."
  kms_key_arn                    = null
  last_modified                  = "2025-01-15T10:30:00.000+0000"
  layers                         = []
  memory_size                    = 256
  package_type                   = "Zip"
  qualified_arn                  = "arn:aws:lambda:us-east-1:123456789012:function:my-project-handler:$LATEST"
  reserved_concurrent_executions = -1
  role                           = "arn:aws:iam::123456789012:role/my-project-lambda-exec"
  runtime                        = "nodejs20.x"
  s3_bucket                      = null
  s3_key                         = null
  s3_object_version              = null
  skip_destroy                   = false
  source_code_size               = 1024
  tags = {
    Project = "my-project"
    Component = "lambda"
  }
  tags_all = {
    Project = "my-project"
    Component = "lambda"
  }
  timeout     = 30
  version     = "$LATEST"
  code_sha256 = "abc123def456"
  tracing_config {
    mode = "PassThrough"
  }
  logging_config {
    log_format = "Text"
    log_group  = "/aws/lambda/my-project-handler"
  }
}

resource "aws_sqs_queue" "my_project_queue" {
  arn                               = "arn:aws:sqs:us-east-1:123456789012:my-project-queue"
  content_based_deduplication       = false
  delay_seconds                     = 0
  fifo_queue                        = false
  id                                = "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue"
  kms_data_key_reuse_period_seconds = 300
  max_message_size                  = 262144
  message_retention_seconds         = 345600
  name                              = "my-project-queue"
  receive_wait_time_seconds         = 10
  redrive_policy                    = "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:123456789012:my-project-queue-dlq\",\"maxReceiveCount\":3}"
  sqs_managed_sse_enabled           = true
  tags = {
    Project = "my-project"
  }
  tags_all = {
    Project = "my-project"
  }
  url                        = "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue"
  visibility_timeout_seconds = 30
}

resource "aws_iam_role" "my_project_lambda_exec" {
  arn                  = "arn:aws:iam::123456789012:role/my-project-lambda-exec"
  assume_role_policy   = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"lambda.amazonaws.com\"},\"Action\":\"sts:AssumeRole\"}]}"
  create_date          = "2025-01-10T08:00:00Z"
  description          = null
  force_detach_policies = false
  id                   = "my-project-lambda-exec"
  managed_policy_arns  = ["arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"]
  max_session_duration = 3600
  name                 = "my-project-lambda-exec"
  path                 = "/"
  tags = {
    Project = "my-project"
  }
  tags_all = {
    Project = "my-project"
  }
  unique_id = "AROAEXAMPLE123456789"
}
`

	mock := &mockTF{
		generatedPages: []string{iteration1HCL, iteration2HCL},
	}

	r := New(Config{
		Project:   "my-project",
		Region:    "us-east-1",
		OutputDir: outputDir,
	}, testLogger())

	r.discoverer = &mockDiscoverer{
		resources: []discovery.DiscoveredResource{
			{
				TerraformType: "aws_lambda_function",
				ImportID:      "my-project-handler",
				Name:          "my-project-handler",
				ARN:           "arn:aws:lambda:us-east-1:123456789012:function:my-project-handler",
			},
			{
				TerraformType: "aws_sqs_queue",
				ImportID:      "https://sqs.us-east-1.amazonaws.com/123456789012/my-project-queue",
				Name:          "my-project-queue",
				ARN:           "arn:aws:sqs:us-east-1:123456789012:my-project-queue",
			},
		},
	}
	r.tfRunner = mock

	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// --- Verify discovery counts ---
	if result.DiscoveredCount != 2 {
		t.Errorf("DiscoveredCount = %d, want 2", result.DiscoveredCount)
	}
	// 2 initial + 1 chased IAM role = 3
	if result.ImportedCount != 3 {
		t.Errorf("ImportedCount = %d, want 3 (2 discovered + 1 chased dep)", result.ImportedCount)
	}

	// --- Verify terraform was called twice (initial + chase iteration) ---
	if mock.planCallCount != 2 {
		t.Errorf("terraform plan called %d times, want 2", mock.planCallCount)
	}

	// --- Verify output files exist ---
	generatedPath := filepath.Join(outputDir, "generated.tf")
	generatedBytes, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("failed to read generated.tf: %v", err)
	}
	generated := string(generatedBytes)

	// --- Verify computed attributes were stripped ---
	for _, computed := range []string{"invoke_arn", "last_modified", "qualified_arn", "source_code_size", "code_sha256"} {
		if strings.Contains(generated, computed) {
			t.Errorf("generated.tf should not contain computed attr %q", computed)
		}
	}
	if strings.Contains(generated, "tags_all") {
		t.Error("generated.tf should not contain tags_all")
	}

	// --- Verify Lambda fixup (placeholder.zip) ---
	if !strings.Contains(generated, "placeholder.zip") {
		t.Error("Lambda should have filename = placeholder.zip (none of filename/s3_bucket/image_uri were set)")
	}

	// --- Verify cross-reference: Lambda role → IAM role ---
	if !strings.Contains(generated, "aws_iam_role.my_project_lambda_exec.arn") {
		t.Error("Lambda role should be cross-referenced to aws_iam_role.my_project_lambda_exec.arn")
	}

	// --- Verify IAM role computed attrs stripped ---
	if strings.Contains(generated, "create_date") {
		t.Error("IAM role create_date should be stripped")
	}
	if strings.Contains(generated, "unique_id") {
		t.Error("IAM role unique_id should be stripped")
	}

	// --- Verify IAM role retained attrs ---
	if !strings.Contains(generated, "assume_role_policy") {
		t.Error("IAM role should retain assume_role_policy")
	}
	if !strings.Contains(generated, "my-project-lambda-exec") {
		t.Error("IAM role should retain name")
	}

	// --- Verify imports.tf contains the chased IAM role ---
	importsBytes, err := os.ReadFile(filepath.Join(outputDir, "imports.tf"))
	if err != nil {
		t.Fatalf("failed to read imports.tf: %v", err)
	}
	imports := string(importsBytes)
	if !strings.Contains(imports, "aws_iam_role") {
		t.Error("imports.tf should contain the chased aws_iam_role import block")
	}
	if !strings.Contains(imports, "my-project-lambda-exec") {
		t.Error("imports.tf should contain the IAM role import ID")
	}

	// --- Verify validation ran ---
	if !result.ValidationOK {
		t.Error("ValidationOK should be true (mock returns no error)")
	}
}
