package genconfig

import (
	"regexp"
	"strings"
	"testing"
)

// TestFixupLambda_NullSourceAttrsTreatedAsMissing pins the real-world
// shape live AWS produces: terraform plan -generate-config-out emits
// `filename = null`, `image_uri = null`, `s3_bucket = null` for an
// imported Lambda (the attrs exist in the schema but carry no value at
// import time). The fixup must treat null-valued attributes as missing
// and inject a placeholder anyway. A naive `body.GetAttribute(name) != nil`
// check passes here even though no usable source is present — so this
// test is the one that pins the difference.
func TestFixupLambda_NullSourceAttrsTreatedAsMissing(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = null
  image_uri     = null
  s3_bucket     = null
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*filename\s*=\s*"lambda_placeholder\.zip"`).MatchString(got) {
		t.Errorf("null-valued source attrs must be treated as missing; placeholder must be injected\n--- got ---\n%s", got)
	}
}

// TestFixupLambda_NoSourceInjectsPlaceholderAndIgnore pins the contract:
// when generate-config-out produced a Lambda block missing all three
// AtLeastOneOf source attrs, the fixup injects `filename =
// "lambda_placeholder.zip"` and a `lifecycle { ignore_changes = [...] }`
// block covering every source-shaped attribute. Without both halves of
// this fix, terraform validate fails for every imported Lambda — the
// real-world live-smoke regression that motivated this code.
func TestFixupLambda_NoSourceInjectsPlaceholderAndIgnore(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*filename\s*=\s*"lambda_placeholder\.zip"`).MatchString(got) {
		t.Errorf("placeholder filename not injected\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "lifecycle") || !strings.Contains(got, "ignore_changes") {
		t.Errorf("lifecycle.ignore_changes block not added\n--- got ---\n%s", got)
	}
	for _, want := range lambdaIgnoreChanges {
		if !strings.Contains(got, want) {
			t.Errorf("ignore_changes missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFixupLambda_ExistingFilenameNotOverwritten pins a friendly-fire
// guard: if the operator (or a future generate-config-out) does emit
// `filename`, the fixup must not clobber it — only the ignore_changes
// pin gets added. Otherwise an apply against the stack would re-upload
// whatever the placeholder points at, defeating the purpose.
func TestFixupLambda_ExistingFilenameNotOverwritten(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = "real_code.zip"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*filename\s*=\s*"real_code\.zip"`).MatchString(got) {
		t.Errorf("operator-supplied filename was clobbered\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "lambda_placeholder.zip") {
		t.Errorf("placeholder injected over existing filename\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "ignore_changes") {
		t.Errorf("ignore_changes pin missing\n--- got ---\n%s", got)
	}
}

// TestFixupLambda_ImageURIAlsoSatisfiesSource pins symmetry with
// container-Lambda: the AtLeastOneOf gate is satisfied by any of
// {filename, image_uri, s3_bucket}, so a Lambda already declaring
// image_uri must NOT have a placeholder filename injected.
func TestFixupLambda_ImageURIAlsoSatisfiesSource(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "fn" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  package_type  = "Image"
  image_uri     = "123.dkr.ecr.us-east-1.amazonaws.com/foo:latest"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "lambda_placeholder.zip") {
		t.Errorf("image_uri Lambda must not get a filename placeholder\n--- got ---\n%s", out)
	}
}

// TestFixupLambda_NonLambdaResourceUntouched pins isolation: the fixup
// table is keyed by resource type, so an unrelated resource block must
// pass through unchanged. A mutation that broadened the fixup to "every
// resource type" would corrupt these blocks.
func TestFixupLambda_NonLambdaResourceUntouched(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" }
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "lifecycle") {
		t.Errorf("non-Lambda resource must not get a lifecycle block from Lambda fixup\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "lambda_placeholder.zip") {
		t.Errorf("non-Lambda resource must not get a Lambda placeholder\n--- got ---\n%s", got)
	}
}

// TestFixupKMS_RotationPeriodZeroDropped pins the LocalStack 4.x
// fidelity workaround for #272: DescribeKey returns
// rotation_period_in_days=0 for keys without rotation enabled, but the
// AWS provider's validator rejects 0 (range 90-2560). Real AWS leaves
// the field absent, so the fixup normalizes LocalStack output to the
// AWS-shaped output that schema cleanup is built around.
func TestFixupKMS_RotationPeriodZeroDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_kms_key" "main" {
  description             = "x"
  rotation_period_in_days = 0
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "rotation_period_in_days") {
		t.Errorf("rotation_period_in_days = 0 must be dropped\n--- got ---\n%s", got)
	}
}

// TestFixupKMS_RotationPeriodNonZeroPreserved pins conservative scope:
// real AWS returning a meaningful 365-day rotation must NOT have its
// value silently dropped. Only the literal 0 from LocalStack triggers
// the fixup.
//
// Table-driven so the carve-outs documented on isAttrLiteralZero
// ("does NOT match `0.0`, `00`, or any computed expression") are
// pinned by tests, not just docstrings. A mutation broadening the
// trigger to `strings.HasPrefix(s, "0")` or `== "00"` would now fail
// these cases.
func TestFixupKMS_RotationPeriodNonZeroPreserved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
	}{
		{name: "real AWS value", value: "365"},
		{name: "minimum valid", value: "90"},
		{name: "maximum valid", value: "2560"},
		{name: "leading-zero literal (carve-out: not the LocalStack shape)", value: "00"},
		{name: "float-zero literal (carve-out: not the LocalStack shape)", value: "0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_kms_key" "main" {
  description             = "x"
  rotation_period_in_days = ` + tc.value + `
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			pat := `rotation_period_in_days\s*=\s*` + regexp.QuoteMeta(tc.value)
			if !regexp.MustCompile(pat).MatchString(got) {
				t.Errorf("value %q must be preserved (only literal `0` is dropped)\n--- got ---\n%s", tc.value, got)
			}
		})
	}
}

// TestFixupDynamoDB_PITRRecoveryPeriodZeroDropped is the DynamoDB twin
// of the KMS rotation fixup — same LocalStack 4.x quirk, different
// resource type. Validator range is 1-35; LocalStack returns 0 when
// PITR is disabled.
func TestFixupDynamoDB_PITRRecoveryPeriodZeroDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled                 = false
    recovery_period_in_days = 0
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "recovery_period_in_days") {
		t.Errorf("recovery_period_in_days = 0 must be dropped from point_in_time_recovery block\n--- got ---\n%s", got)
	}
	// The enclosing block must remain so other PITR fields (enabled)
	// stay intact.
	if !strings.Contains(got, "point_in_time_recovery {") {
		t.Errorf("point_in_time_recovery block must not be removed wholesale\n--- got ---\n%s", got)
	}
}

// TestFixupDynamoDB_PITRRecoveryPeriodNonZeroPreserved is the symmetric
// non-zero case — a real PITR window must reach the emitted HCL
// untouched. Table-driven to also pin the literal-zero carve-outs
// documented on isAttrLiteralZero.
func TestFixupDynamoDB_PITRRecoveryPeriodNonZeroPreserved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
	}{
		{name: "real AWS value", value: "14"},
		{name: "minimum valid", value: "1"},
		{name: "maximum valid", value: "35"},
		{name: "leading-zero literal (carve-out)", value: "00"},
		{name: "float-zero literal (carve-out)", value: "0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled                 = true
    recovery_period_in_days = ` + tc.value + `
  }
}
`)
			out, err := applyResourceTypeFixups(in)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			pat := `recovery_period_in_days\s*=\s*` + regexp.QuoteMeta(tc.value)
			if !regexp.MustCompile(pat).MatchString(got) {
				t.Errorf("value %q must be preserved (only literal `0` is dropped)\n--- got ---\n%s", tc.value, got)
			}
		})
	}
}

// TestFixupDynamoDB_NoPITRBlockNoOp pins the canonical real-AWS shape:
// when point_in_time_recovery isn't even present, the fixup must be a
// pure no-op. A mutation that "helpfully" injected a PITR block or
// touched other sub-blocks would fail this.
func TestFixupDynamoDB_NoPITRBlockNoOp(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name     = "x"
  hash_key = "id"

  attribute {
    name = "id"
    type = "S"
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("absent point_in_time_recovery must yield identical output\n--- in ---\n%s\n--- out ---\n%s", in, out)
	}
}

// TestFixupDynamoDB_PITRBlockPresentAttrAbsentNoOp pins that a PITR
// block carrying only `enabled = false` (no recovery_period_in_days)
// is also a no-op. A mutation that always added or removed
// recovery_period_in_days regardless of presence would fail.
func TestFixupDynamoDB_PITRBlockPresentAttrAbsentNoOp(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled = false
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !regexp.MustCompile(`(?m)^\s*enabled\s*=\s*false`).MatchString(got) {
		t.Errorf("enabled=false must be preserved\n--- got ---\n%s", got)
	}
	if regexp.MustCompile(`recovery_period_in_days`).MatchString(got) {
		t.Errorf("absent recovery_period_in_days must NOT appear after fixup\n--- got ---\n%s", got)
	}
}

// TestFixupDynamoDB_MultiplePITRBlocksAllZerosDropped pins iteration:
// if a (hypothetical) DynamoDB resource has multiple
// point_in_time_recovery sub-blocks (Terraform doesn't support this in
// reality, but `terraform plan -generate-config-out` has emitted
// duplicate blocks before for other types), the fixup must process
// all of them — not break after the first match. A mutation
// substituting `break` for `continue` after the inner remove would
// survive single-block tests but fail this.
func TestFixupDynamoDB_MultiplePITRBlocksAllZerosDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_dynamodb_table" "main" {
  name = "x"
  point_in_time_recovery {
    enabled                 = false
    recovery_period_in_days = 0
  }
  point_in_time_recovery {
    enabled                 = false
    recovery_period_in_days = 0
  }
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`recovery_period_in_days`).MatchString(got) {
		t.Errorf("all zero-valued recovery_period_in_days must be dropped, even across multiple PITR blocks\n--- got ---\n%s", got)
	}
}

// TestFixupLambda_MultipleLambdasBothFixed pins iteration: two Lambda
// blocks in input order both get the placeholder + ignore_changes
// treatment. A mutation that exited after the first block would survive
// single-resource tests but fail this one.
func TestFixupLambda_MultipleLambdasBothFixed(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_function" "alpha" {
  function_name = "alpha"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
}

resource "aws_lambda_function" "bravo" {
  function_name = "bravo"
  role          = "arn:aws:iam::123456789012:role/r"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
}
`)
	out, err := applyResourceTypeFixups(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "lambda_placeholder.zip") != 2 {
		t.Errorf("expected 2 placeholder injections, got %d\n--- got ---\n%s", strings.Count(got, "lambda_placeholder.zip"), got)
	}
	if strings.Count(got, "ignore_changes") != 2 {
		t.Errorf("expected 2 ignore_changes injections, got %d", strings.Count(got, "ignore_changes"))
	}
}
