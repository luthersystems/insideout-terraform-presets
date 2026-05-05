package depchase

import (
	"reflect"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func resource(addr, importID string, native map[string]string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Address:   addr,
			ImportID:  importID,
			NativeIDs: native,
		},
	}
}

func TestFindUnresolved_ReturnsARNLiteralsNotInBatch(t *testing.T) {
	t.Parallel()
	raw := []byte(`
resource "aws_lambda_function" "io_foo_handler" {
  function_name = "io-foo-handler"
  role          = "arn:aws:iam::123:role/io-foo-handler-role"
  kms_key_arn   = "arn:aws:kms:us-east-1:123:key/uuid-1"
}
resource "aws_dynamodb_table" "io_foo_orders" {
  name = "io-foo-orders"
}
`)
	in := []imported.ImportedResource{
		resource("aws_lambda_function.io_foo_handler", "io-foo-handler",
			map[string]string{"arn": "arn:aws:lambda:us-east-1:123:function:io-foo-handler"}),
		resource("aws_dynamodb_table.io_foo_orders", "io-foo-orders",
			map[string]string{"arn": "arn:aws:dynamodb:us-east-1:123:table/io-foo-orders"}),
	}
	got, err := FindUnresolved(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"arn:aws:iam::123:role/io-foo-handler-role",
		"arn:aws:kms:us-east-1:123:key/uuid-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFindUnresolved_SkipsResolvedARNs(t *testing.T) {
	t.Parallel()
	// Lambda B references Lambda A's ARN — A is in the batch, so the
	// reference is resolved and not surfaced.
	raw := []byte(`
resource "aws_lambda_function" "a" {
  function_name = "a"
}
resource "aws_lambda_function" "b" {
  function_name = "b"
  destination   = "arn:aws:lambda:us-east-1:123:function:a"
}
`)
	in := []imported.ImportedResource{
		resource("aws_lambda_function.a", "a",
			map[string]string{"arn": "arn:aws:lambda:us-east-1:123:function:a"}),
		resource("aws_lambda_function.b", "b",
			map[string]string{"arn": "arn:aws:lambda:us-east-1:123:function:b"}),
	}
	got, err := FindUnresolved(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (Lambda A's ARN is in the resolved set)", got)
	}
}

func TestFindUnresolved_DeduplicatesAndSorts(t *testing.T) {
	t.Parallel()
	// Two resources both reference the same external IAM role.
	raw := []byte(`
resource "aws_lambda_function" "a" {
  role = "arn:aws:iam::123:role/shared-role"
}
resource "aws_lambda_function" "b" {
  role = "arn:aws:iam::123:role/shared-role"
}
resource "aws_lambda_function" "c" {
  role = "arn:aws:iam::123:role/another-role"
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"arn:aws:iam::123:role/another-role",
		"arn:aws:iam::123:role/shared-role",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (sorted, deduped)", got, want)
	}
}

func TestFindUnresolved_IgnoresNonLiteralExpressions(t *testing.T) {
	t.Parallel()
	// Interpolations, list values, and references to other resources
	// should not be treated as unresolved literals — the conservative
	// finder leaves them alone for genconfig's crossref pass.
	raw := []byte(`
resource "aws_lambda_function" "a" {
  role           = aws_iam_role.handler.arn
  source_account = "123"
  layers         = ["arn:aws:lambda:us-east-1:123:layer:x:1"]
  description    = "see arn:aws:iam::123:role/embedded inside text"
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (no top-level pure-literal ARN attrs)", got)
	}
}

func TestFindUnresolved_IgnoresNonResourceBlocks(t *testing.T) {
	t.Parallel()
	// Provider/terraform/variable blocks must not be walked — only
	// `resource` blocks. (A `data` block could legitimately reference
	// an external ARN, but Phase 2 imports do not emit data blocks.)
	raw := []byte(`
provider "aws" {
  region = "arn:aws:iam::123:role/should-not-match"
}
data "aws_caller_identity" "current" {}
variable "external_role" {
  default = "arn:aws:iam::123:role/from-variable-default"
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (only resource blocks are scanned)", got)
	}
}

func TestFindUnresolved_NoARNsReturnsEmpty(t *testing.T) {
	t.Parallel()
	raw := []byte(`
resource "aws_dynamodb_table" "t" {
  name           = "io-foo-orders"
  hash_key       = "id"
  read_capacity  = 5
  write_capacity = 5
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestFindUnresolved_MalformedHCLErrors(t *testing.T) {
	t.Parallel()
	_, err := FindUnresolved([]byte("resource {{{ broken"), nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestFindUnresolved_MatchesAgainstURLAndImportID(t *testing.T) {
	t.Parallel()
	// SQS queue URL and Lambda function name (ImportID) should also
	// count as resolved — buildResolvedSet inverts all three (arn,
	// url, ImportID).
	raw := []byte(`
resource "aws_lambda_function" "h" {
  source = "https://sqs.us-east-1.amazonaws.com/123/io-foo-q"
  fn     = "io-foo-handler"
  unresolved = "arn:aws:iam::123:role/external"
}
`)
	in := []imported.ImportedResource{
		resource("aws_sqs_queue.q", "https://sqs.us-east-1.amazonaws.com/123/io-foo-q",
			map[string]string{"url": "https://sqs.us-east-1.amazonaws.com/123/io-foo-q"}),
		resource("aws_lambda_function.h", "io-foo-handler", nil),
	}
	got, err := FindUnresolved(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	// Only the IAM role ARN should surface — the URL and bare name
	// aren't ARN-shaped, so they wouldn't have been collected anyway,
	// but this test pins the cross-check against the resolved set.
	want := []string{"arn:aws:iam::123:role/external"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
