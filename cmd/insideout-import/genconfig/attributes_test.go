package genconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// requireNoLegacyAttributes fails when genconfig wrote the deprecated
// Attributes map for a type that has a typed model (#921).
func requireNoLegacyAttributes(t *testing.T, ir imported.ImportedResource) {
	t.Helper()
	if ir.Attributes != nil {
		t.Errorf("Attributes must stay unset for a type with a typed model; got %v", ir.Attributes)
	}
}

// TestExtractAttributes_JSONEncodePolicyEvaluates pins the #652 fix on
// the legacy Attributes path (a type with no typed model): a JSON-string
// attribute that `terraform plan -generate-config-out` renders as a
// jsonencode({...}) call must be captured as a valid JSON string, not as
// the literal source text "jsonencode({...})". Capturing the text made the
// composer re-quote it into `policy = "jsonencode({...})"`, which terraform
// plan rejects with `"policy" contains an invalid JSON policy: not a JSON
// object`.
func TestExtractAttributes_JSONEncodePolicyEvaluates(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_unmodeled_policy" "x" {
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
		{Identity: imported.ResourceIdentity{Address: "aws_unmodeled_policy.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := out[0].Attributes
	policy, ok := legacy["policy"].(string)
	if !ok {
		t.Fatalf("policy should be a string; got %T = %v", legacy["policy"], legacy["policy"])
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

// TestExtractAttributes_JSONEncodeTypedAttrs pins the typed side of #652
// for the IAM JSON-string attributes, which terraform plan
// -generate-config-out renders as jsonencode({...}) calls. Typed Attrs keep
// the call as an expression, which the composer emits unquoted, so
// terraform evaluates it; the legacy Attributes map stays unset (#921).
// The resource Type is empty here, as in some callers, so the type comes
// from the block label.
func TestExtractAttributes_JSONEncodeTypedAttrs(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_iam_policy" "p" {
  name = "alpha"
  policy = jsonencode({
    Version = "2012-10-17"
  })
}
resource "aws_iam_role" "r" {
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
		{Identity: imported.ResourceIdentity{Address: "aws_iam_policy.p"}},
		{Identity: imported.ResourceIdentity{Address: "aws_iam_role.r"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"policy", "assume_role_policy"} {
		requireNoLegacyAttributes(t, out[i])
		var typed map[string]map[string]any
		if err := json.Unmarshal(out[i].Attrs, &typed); err != nil {
			t.Fatalf("%s: Attrs should decode as typed JSON: %v\n%s", key, err, out[i].Attrs)
		}
		expr, ok := typed[key]["expr"].(string)
		if !ok || !strings.HasPrefix(expr, "jsonencode(") {
			t.Errorf("%s: typed Attrs should keep the jsonencode() call as an expression; got %v", key, typed[key])
		}
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
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	requireNoLegacyAttributes(t, out[0])

	var typed map[string]map[string]any
	if err := json.Unmarshal(out[0].Attrs, &typed); err != nil {
		t.Fatalf("Attrs should decode as typed JSON: %v\n%s", err, out[0].Attrs)
	}
	if typed["name"]["literal"] != "alpha" {
		t.Errorf("typed Attrs name literal = %v, want alpha", typed["name"]["literal"])
	}
	if typed["fifo_queue"]["literal"] != true {
		t.Errorf("typed Attrs fifo_queue literal = %v, want true", typed["fifo_queue"]["literal"])
	}
	if typed["delay_seconds"]["literal"] != float64(30) {
		t.Errorf("typed Attrs delay_seconds literal = %v, want 30", typed["delay_seconds"]["literal"])
	}
	if typed["message_retention_seconds"]["literal"] != float64(86400) {
		t.Errorf("typed Attrs message_retention_seconds literal = %v, want 86400", typed["message_retention_seconds"]["literal"])
	}
	if len(typed) != 4 {
		t.Errorf("typed Attrs len=%d, want 4 (all fixture attrs decoded)\n%s", len(typed), out[0].Attrs)
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
		{Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", Address: "aws_secretsmanager_secret.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	requireNoLegacyAttributes(t, out[0])

	var typed map[string]map[string]any
	if err := json.Unmarshal(out[0].Attrs, &typed); err != nil {
		t.Fatalf("Attrs should decode as typed JSON: %v\n%s", err, out[0].Attrs)
	}
	if typed["kms_key_id"]["expr"] != "aws_kms_key.foo.arn" {
		t.Errorf("typed Attrs kms_key_id expr = %v, want aws_kms_key.foo.arn", typed["kms_key_id"]["expr"])
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

// TestExtractAttributes_UnmodeledTypeKeepsLegacyAttributes pins the one
// case genconfig still writes the deprecated Attributes map: a type with no
// typed model, where it is the only place the desired state can go (#921).
// It also pins the legacy decoding rules there: list literals decode to a
// Go []any (via cty -> JSON), and a reference is kept as its source text. A
// mutation that took the source-text fallback for everything would store
// the raw "[\"a\", \"b\"]" string instead of a slice.
func TestExtractAttributes_UnmodeledTypeKeepsLegacyAttributes(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_unmodeled_thing" "x" {
  tags_keys  = ["a", "b", "c"]
  kms_key_id = aws_kms_key.foo.arn
}
`)
	out, err := extractAttributes(in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_unmodeled_thing", Address: "aws_unmodeled_thing.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := out[0].Attributes
	got, ok := legacy["tags_keys"].([]any)
	if !ok {
		t.Fatalf("tags_keys should decode to []any; got %T = %v", legacy["tags_keys"], legacy["tags_keys"])
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("tags_keys = %v, want [a b c]", got)
	}
	if legacy["kms_key_id"] != "aws_kms_key.foo.arn" {
		t.Errorf("kms_key_id = %v, want the reference source text", legacy["kms_key_id"])
	}
	if len(out[0].Attrs) != 0 {
		t.Errorf("a type with no model cannot have typed Attrs; got %s", out[0].Attrs)
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
	if input[0].Attrs != nil {
		t.Errorf("input slice was mutated; out and input must not alias")
	}
	if out[0].Attrs == nil {
		t.Errorf("output slice missing typed Attrs")
	}
}

// TestReportUntypedAttrs pins that an attribute or block in generated.tf the
// typed model has no field for is reported, so typed Attrs never drop a
// value silently (#921).
func TestReportUntypedAttrs(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name          = "alpha"
  not_in_model  = "dropped"
  timeouts {
    create  = "5m"
    unknown = "dropped"
  }
  mystery_block {
    a = 1
  }
}
resource "aws_unmodeled_thing" "y" {
  anything = 1
}
`)
	var buf strings.Builder
	reportUntypedAttrs(&buf, in, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.x"}},
		{Identity: imported.ResourceIdentity{Type: "aws_unmodeled_thing", Address: "aws_unmodeled_thing.y"}},
	})
	got := buf.String()
	for _, want := range []string{"aws_sqs_queue.x: not_in_model", "aws_sqs_queue.x: timeouts.unknown", "aws_sqs_queue.x: mystery_block"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"name", "timeouts.create", "aws_unmodeled_thing"} {
		if strings.Contains(got, ": "+unwanted+"\n") || strings.Contains(got, unwanted+".y") {
			t.Errorf("report should not name %q:\n%s", unwanted, got)
		}
	}
}
