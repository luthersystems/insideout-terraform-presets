package depchase

import (
	"reflect"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/dependencies"
)

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestNonARNAttrRulesAgreeWithDependencyRegistry is a drift guard: every
// class-B curated attr rule's tfType MUST agree with the canonical
// cross-reference registry pkg/imported/dependencies (Lookup / FieldRefs,
// presets#482/#667). The class-B chase seeds a discoverer with rule.tfType; if
// this map drifted from the registry the chase would look up the wrong resource
// type. Triggering example: kms_master_key_id → aws_kms_key must match in both.
func TestNonARNAttrRulesAgreeWithDependencyRegistry(t *testing.T) {
	t.Parallel()
	for attr, rule := range nonARNAttrRules {
		want, ok := dependencies.Lookup(attr)
		if !ok {
			t.Errorf("nonARNAttrRules[%q] has no entry in pkg/imported/dependencies (Lookup); every class-B attr must be a known cross-ref field", attr)
			continue
		}
		if rule.tfType != want {
			t.Errorf("nonARNAttrRules[%q].tfType=%q disagrees with dependencies.Lookup=%q", attr, rule.tfType, want)
		}
	}
}

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
	// Interpolations, traversals, and ARNs embedded inside larger template
	// text must NOT be treated as reference literals — the conservative
	// finder leaves them alone for genconfig's crossref pass. (The class-A
	// nested-list case — `layers = ["arn:…"]` — IS now surfaced by design;
	// see TestFindUnresolved_SurfacesNestedListAndObjectARNs.)
	raw := []byte(`
resource "aws_lambda_function" "a" {
  role           = aws_iam_role.handler.arn
  source_account = "123"
  description    = "see arn:aws:iam::123:role/embedded inside text"
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (traversal / non-arn / embedded-in-text)", got)
	}
}

// TestFindUnresolved_SurfacesNestedListAndObjectARNs pins presets#834 class A:
// an ARN literal nested inside a list element or an object attribute value —
// which the historical top-level-only pass silently skipped — is now surfaced
// by the nested-body walk. The lambda-layer ARN sits in a list; the KMS ARN
// sits in an object value inside a nested block.
func TestFindUnresolved_SurfacesNestedListAndObjectARNs(t *testing.T) {
	t.Parallel()
	raw := []byte(`
resource "aws_lambda_function" "a" {
  layers = ["arn:aws:lambda:us-east-1:123:layer:x:1"]
  environment {
    variables = {
      SIGNING_KEY = "arn:aws:kms:us-east-1:123:key/uuid-1"
    }
  }
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"arn:aws:kms:us-east-1:123:key/uuid-1",
		"arn:aws:lambda:us-east-1:123:layer:x:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (nested list + object-in-block ARNs)", got, want)
	}
}

// TestFindUnresolved_NestedARNResolvedWhenTargetInBatch pins that a nested ARN
// pointing at an in-batch resource is NOT surfaced — the resolved-set check
// applies to nested hits exactly as it does to top-level ones.
func TestFindUnresolved_NestedARNResolvedWhenTargetInBatch(t *testing.T) {
	t.Parallel()
	raw := []byte(`
resource "aws_lambda_function" "a" {
  layers = ["arn:aws:lambda:us-east-1:123:layer:shared:1"]
}
`)
	in := []imported.ImportedResource{
		resource("aws_lambda_layer_version.shared", "arn:aws:lambda:us-east-1:123:layer:shared:1",
			map[string]string{"arn": "arn:aws:lambda:us-east-1:123:layer:shared:1"}),
	}
	got, err := FindUnresolved(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (nested ARN target is in the batch)", got)
	}
}

// TestFindUnresolved_SurfacesCuratedNonARNIdentifier pins presets#834 class B:
// a bare KMS KeyId UUID in a curated attribute (kms_key_id / kms_master_key_id)
// is surfaced, while the SAME UUID shape in a non-curated attribute is not
// (guards against generic bare-name matching / false positives).
func TestFindUnresolved_SurfacesCuratedNonARNIdentifier(t *testing.T) {
	t.Parallel()
	raw := []byte(`
resource "aws_sqs_queue" "q" {
  kms_master_key_id = "1234abcd-12ab-34cd-56ef-1234567890ab"
}
resource "aws_ebs_volume" "v" {
  kms_key_id = "abcd0000-11ab-22cd-33ef-abcdef012345"
}
resource "aws_instance" "i" {
  some_other_id = "ffffffff-ffff-ffff-ffff-ffffffffffff"
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1234abcd-12ab-34cd-56ef-1234567890ab",
		"abcd0000-11ab-22cd-33ef-abcdef012345",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (only curated kms_* attrs; some_other_id UUID must NOT match)", got, want)
	}
}

// TestFindUnresolved_SurfacesClassBInObjectExpression pins F6: a curated
// non-ARN identifier (KMS KeyId UUID) nested inside an OBJECT expression value
// — not just a top-level/nested-block attribute — is surfaced. Before F6,
// checkNonARN ran only on body attributes, so an object-shaped SSE config was
// silent.
func TestFindUnresolved_SurfacesClassBInObjectExpression(t *testing.T) {
	t.Parallel()
	uuid := "1234abcd-12ab-34cd-56ef-1234567890ab"
	raw := []byte(`
resource "aws_s3_bucket" "b" {
  rule = {
    x = {
      kms_master_key_id = "` + uuid + `"
    }
  }
}
`)
	scan, err := scanGenerated(raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hit, ok := scan.nonARN[uuid]
	if !ok {
		t.Fatalf("object-nested kms_master_key_id UUID not surfaced; nonARN=%+v", scan.nonARN)
	}
	// The class-B hit renders from attr (never a path — G5 dropped nonARNHit.path);
	// pin attr + tfType and the unified unresolved membership.
	if hit.attr != "kms_master_key_id" || hit.tfType != "aws_kms_key" {
		t.Errorf("class-B hit=%+v, want attr=kms_master_key_id tfType=aws_kms_key", hit)
	}
	if !contains(scan.unresolved, uuid) {
		t.Errorf("unresolved=%v, want it to include the object-nested UUID", scan.unresolved)
	}
}

// TestObjectKey_DynamicKeyYieldsStar pins F7: a parenthesized dynamic object key
// `(expr) = …` must collapse to the '*' path segment (matching the canonical
// pkg/composer.objectConsKeyAsString ForceNonLiteral guard), not a fabricated
// static name.
func TestObjectKey_DynamicKeyYieldsStar(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:iam::123:role/dynamic-keyed"
	// A SINGLE-segment parenthesized key `(k) = …`: without the ForceNonLiteral
	// guard the local objectKey would fall through to the single-segment
	// ScopeTraversalExpr branch and fabricate the static name "k"; the guard
	// forces the '*' fallback (matching pkg/composer.objectConsKeyAsString).
	raw := []byte(`
resource "aws_lambda_function" "fn" {
  tags = {
    (k) = "` + arn + `"
  }
}
`)
	scan, err := scanGenerated(raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hit, ok := scan.nested[arn]
	if !ok {
		t.Fatalf("dynamic-keyed nested ARN not surfaced; nested=%+v", scan.nested)
	}
	if hit.path != "tags.*" {
		t.Errorf("dynamic-key path=%q, want tags.* (ForceNonLiteral guard); a fabricated static name like tags.k means the guard is missing", hit.path)
	}
}

// TestFindUnresolved_UppercaseKMSKeyUUID pins F9: the KMS KeyId UUID matcher is
// case-insensitive — an uppercase-hex UUID (as the AWS console sometimes renders
// it) is surfaced, and a non-hex look-alike is not.
func TestFindUnresolved_UppercaseKMSKeyUUID(t *testing.T) {
	t.Parallel()
	upper := "ABCD0000-11AB-22CD-33EF-ABCDEF012345"
	raw := []byte(`
resource "aws_sqs_queue" "q" {
  kms_master_key_id = "` + upper + `"
}
resource "aws_sqs_queue" "bad" {
  kms_master_key_id = "gggg0000-11ab-22cd-33ef-abcdef012345"
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{upper}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (uppercase UUID matches, non-hex does not)", got, want)
	}
}

// TestFindUnresolved_LabeledNestedBlockPaths pins F10: two same-type LABELED
// nested blocks yield distinct attribute paths (labels are folded into the path
// segment as type[label]) so their warnings are individually traceable.
func TestFindUnresolved_LabeledNestedBlockPaths(t *testing.T) {
	t.Parallel()
	arnA := "arn:aws:kms:us-east-1:123:key/aaaa1111-bbbb-2222-cccc-333333333333"
	arnB := "arn:aws:kms:us-east-1:123:key/bbbb2222-cccc-3333-dddd-444444444444"
	raw := []byte(`
resource "aws_s3_bucket" "b" {
  rule "a" {
    signing_key = "` + arnA + `"
  }
  rule "b" {
    signing_key = "` + arnB + `"
  }
}
`)
	scan, err := scanGenerated(raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hit := scan.nested[arnA]; hit.path != "rule[a].signing_key" {
		t.Errorf("labeled block A path=%q, want rule[a].signing_key", hit.path)
	}
	if hit := scan.nested[arnB]; hit.path != "rule[b].signing_key" {
		t.Errorf("labeled block B path=%q, want rule[b].signing_key", hit.path)
	}
}

// TestFindUnresolved_IgnoresNestedNonLiterals pins the conservative contract at
// depth: a nested interpolation, a nested traversal, and an ARN embedded inside
// a larger nested string must ALL be ignored — only concrete, pure string
// literals are surfaced.
func TestFindUnresolved_IgnoresNestedNonLiterals(t *testing.T) {
	t.Parallel()
	raw := []byte(`
resource "aws_lambda_function" "a" {
  layers = ["${aws_lambda_layer_version.y.arn}"]
  environment {
    variables = {
      TRAVERSAL = aws_iam_role.handler.arn
      EMBEDDED  = "prefix arn:aws:iam::123:role/embedded suffix"
    }
  }
}
`)
	got, err := FindUnresolved(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (nested interpolation / traversal / embedded-in-text all ignored)", got)
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

// TestScanGenerated_Depth0StrictVsHeredocClassification pins G1 at the finder
// level: a depth-0 ARN attribute is classified top-level ONLY when its raw
// source is a strict, crossref-rewritable quoted literal (`role = "arn:…"`). The
// SAME ARN written as a HEREDOC — which pureStringLiteral decodes (with a
// trailing newline) but genconfig's crossref can never rewrite — is classified
// as a NESTED (terminal-by-design) hit, NOT top-level, and its recorded key is
// TrimSpace'd (no trailing newline, G2). The deleted hclwrite stringLiteralValue
// enforced this implicitly via its OQuote/QuotedLit/CQuote 3-token contract.
func TestScanGenerated_Depth0StrictVsHeredocClassification(t *testing.T) {
	t.Parallel()
	strictARN := "arn:aws:iam::123:role/strict"
	heredocARN := "arn:aws:iam::123:role/heredoc"
	// The heredoc terminator must sit at column 0.
	raw := []byte(`
resource "aws_lambda_function" "fn" {
  role      = "` + strictARN + `"
  exec_role = <<EOT
` + heredocARN + `
EOT
}
`)
	scan, err := scanGenerated(raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Strict quoted depth-0 ARN → top-level, never nested.
	if _, ok := scan.topLevel[strictARN]; !ok {
		t.Errorf("strict quoted depth-0 ARN not classified top-level; topLevel=%+v", scan.topLevel)
	}
	if _, ok := scan.nested[strictARN]; ok {
		t.Errorf("strict quoted depth-0 ARN must NOT be nested; nested=%+v", scan.nested)
	}
	// Heredoc depth-0 ARN → nested (terminal-by-design), never top-level.
	if _, ok := scan.topLevel[heredocARN]; ok {
		t.Errorf("heredoc depth-0 ARN must NOT be top-level (crossref can't rewrite it); topLevel=%+v", scan.topLevel)
	}
	hit, ok := scan.nested[heredocARN]
	if !ok {
		t.Fatalf("heredoc depth-0 ARN not surfaced as nested; nested=%+v", scan.nested)
	}
	if hit.path != "exec_role" {
		t.Errorf("heredoc nested path=%q, want the attr name exec_role", hit.path)
	}
	// A raw heredoc value carries a trailing newline; the recorded key must be
	// trimmed, so it appears clean in unresolved (no `\n` leak, G2).
	if contains(scan.unresolved, heredocARN+"\n") {
		t.Errorf("unresolved leaked a trailing-newline key: %v", scan.unresolved)
	}
	for _, want := range []string{strictARN, heredocARN} {
		if !contains(scan.unresolved, want) {
			t.Errorf("unresolved=%v, want it to include the clean literal %q", scan.unresolved, want)
		}
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
