package genconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestExtractAttributes_JSONEncodePolicyEvaluates pins the #652 fix: a
// JSON-string attribute that `terraform plan -generate-config-out`
// renders as a jsonencode({...}) call must be captured as a valid JSON
// string, not as the literal source text "jsonencode({...})". Capturing
// the text made the composer re-quote it into `policy =
// "jsonencode({...})"`, which terraform plan rejects with `"policy"
// contains an invalid JSON policy: not a JSON object`.
func TestExtractAttributes_JSONEncodePolicyEvaluates(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_iam_policy" "x" {
  name = "alpha"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject"]
      Resource = "arn:aws:s3:::example-bucket/*"
    }]
  })
}
`)
	out, err := extractAttributes(in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_iam_policy.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := out[0].Attributes["policy"].(string)
	if !ok {
		t.Fatalf("policy should be a string; got %T = %v", out[0].Attributes["policy"], out[0].Attributes["policy"])
	}
	if strings.Contains(policy, "jsonencode") {
		t.Fatalf("policy still carries the jsonencode() source text; the expression was not evaluated:\n%s", policy)
	}
	// The captured value must be a valid JSON object.
	var doc map[string]any
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		t.Fatalf("captured policy is not valid JSON: %v\n%s", err, policy)
	}
	if doc["Version"] != "2012-10-17" {
		t.Errorf("policy JSON Version = %v, want 2012-10-17", doc["Version"])
	}
}

// TestExtractAttributes_JSONEncodeAssumeRolePolicy is the sibling of
// TestExtractAttributes_JSONEncodePolicyEvaluates for a different type
// and attribute — aws_iam_role.assume_role_policy. terraform plan
// -generate-config-out renders every IAM JSON-string attribute as a
// jsonencode({...}) call, so the #652 fix must be generic across all of
// them, not special-cased to aws_iam_policy.policy.
func TestExtractAttributes_JSONEncodeAssumeRolePolicy(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_iam_role" "x" {
  name = "alpha"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}
`)
	out, err := extractAttributes(in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_iam_role.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := out[0].Attributes["assume_role_policy"].(string)
	if !ok {
		t.Fatalf("assume_role_policy should be a string; got %T", out[0].Attributes["assume_role_policy"])
	}
	if strings.Contains(policy, "jsonencode") {
		t.Fatalf("assume_role_policy still carries jsonencode() source text:\n%s", policy)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		t.Fatalf("captured assume_role_policy is not valid JSON: %v\n%s", err, policy)
	}
}

func TestExtractAttributes_ScalarTypes(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name                       = "alpha"
  delay_seconds              = 30
  fifo_queue                 = true
  message_retention_seconds  = 86400
}
`)
	out, err := extractAttributes(in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	got := out[0].Attributes
	// Assert every fixture attribute round-trips. A mutation in
	// decodeAttribute that returned nil for any subset (e.g. an off-by-one
	// in a future loop) would only get caught when every input is verified.
	if got["name"] != "alpha" {
		t.Errorf("name = %v, want \"alpha\"", got["name"])
	}
	if got["fifo_queue"] != true {
		t.Errorf("fifo_queue = %v, want true", got["fifo_queue"])
	}
	// json.Unmarshal turns numbers into float64, which is fine for this layer.
	if got["delay_seconds"] != float64(30) {
		t.Errorf("delay_seconds = %v (%T), want 30", got["delay_seconds"], got["delay_seconds"])
	}
	if got["message_retention_seconds"] != float64(86400) {
		t.Errorf("message_retention_seconds = %v (%T), want 86400", got["message_retention_seconds"], got["message_retention_seconds"])
	}
	if len(got) != 4 {
		t.Errorf("Attributes len=%d, want 4 (all fixture attrs decoded)\n--- got ---\n%v", len(got), got)
	}
}

// TestExtractAttributes_PreservesRefAsSource pins the contract for
// non-literal expressions: a reference like aws_kms_key.foo.arn cannot be
// JSON-decoded, so it must be stored as the source text. A mutation that
// drops these would erase the cross-ref work that crossref.go just did.
func TestExtractAttributes_PreservesRefAsSource(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_secretsmanager_secret" "x" {
  name        = "alpha"
  kms_key_id  = aws_kms_key.foo.arn
}
`)
	out, err := extractAttributes(in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_secretsmanager_secret.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out[0].Attributes["kms_key_id"].(string)
	if !ok {
		t.Fatalf("kms_key_id should be stored as string source; got %T = %v", out[0].Attributes["kms_key_id"], out[0].Attributes["kms_key_id"])
	}
	if got != "aws_kms_key.foo.arn" {
		t.Errorf("kms_key_id = %q, want \"aws_kms_key.foo.arn\"", got)
	}
}

// TestExtractAttributes_UnmatchedAddressPreservesExisting pins the
// safety contract: a manifest entry whose address doesn't appear in the
// generated HCL keeps its existing Attributes. A mutation that always
// blanks Attributes would silently drop hand-edits between runs.
func TestExtractAttributes_UnmatchedAddressPreservesExisting(t *testing.T) {
	t.Parallel()
	preserve := imported.ImportedResource{
		Identity:   imported.ResourceIdentity{Address: "aws_sqs_queue.notinhcl"},
		Attributes: map[string]any{"name": "preserved"},
	}
	out, err := extractAttributes([]byte(`# empty file`), []imported.ImportedResource{preserve})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Attributes["name"] != "preserved" {
		t.Errorf("Attributes for unmatched address must be preserved; got %v", out[0].Attributes)
	}
}

// TestExtractAttributes_DecodesListLiteral pins that list literals decode
// to a Go []any (via cty -> JSON). A mutation that took the source-text
// fallback path for everything would store the raw "[\"a\", \"b\"]" string
// here instead of a typed slice.
func TestExtractAttributes_DecodesListLiteral(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  tags_keys = ["a", "b", "c"]
}
`)
	out, err := extractAttributes(in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out[0].Attributes["tags_keys"].([]any)
	if !ok {
		t.Fatalf("tags_keys should decode to []any; got %T = %v", out[0].Attributes["tags_keys"], out[0].Attributes["tags_keys"])
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("tags_keys = %v, want [a b c]", got)
	}
}

// TestExtractAttributes_ReturnedSliceIsCopy pins that mutations to the
// returned Resources don't bleed into the caller's slice. The pipeline
// re-emits the manifest right after, so accidentally mutating the input
// would break determinism across re-runs of writeManifest.
func TestExtractAttributes_ReturnedSliceIsCopy(t *testing.T) {
	t.Parallel()
	input := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x"}},
	}
	out, err := extractAttributes([]byte(`resource "aws_sqs_queue" "x" { name = "alpha" }`), input)
	if err != nil {
		t.Fatal(err)
	}
	if input[0].Attributes != nil {
		t.Errorf("input slice was mutated; out and input must not alias")
	}
	if out[0].Attributes == nil {
		t.Errorf("output slice missing Attributes")
	}
}
