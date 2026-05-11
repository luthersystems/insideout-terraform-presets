package genconfig

import (
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func ir(addr, importID string, native map[string]string) imported.ImportedResource {
	return imported.ImportedResource{Identity: imported.ResourceIdentity{
		Address:   addr,
		ImportID:  importID,
		NativeIDs: native,
	}}
}

func TestApplyCrossRefs_ReplacesArnLiteral(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_cloudwatch_log_subscription_filter" "x" {
  destination_arn = "arn:aws:lambda:us-east-1:123:function:fanout"
  log_group_name  = "/aws/lambda/foo"
}
`)
	resources := []imported.ImportedResource{
		ir("aws_lambda_function.fanout", "arn:aws:lambda:us-east-1:123:function:fanout",
			map[string]string{"arn": "arn:aws:lambda:us-east-1:123:function:fanout"}),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "destination_arn = aws_lambda_function.fanout.arn") {
		t.Errorf("ARN literal must become a traversal\n--- got ---\n%s", got)
	}
	if strings.Contains(got, `"arn:aws:lambda:us-east-1:123:function:fanout"`) {
		t.Errorf("literal ARN string must be removed\n--- got ---\n%s", got)
	}
}

// TestApplyCrossRefs_ReplacesQueueURLAtTopLevel pins that an SQS queue URL
// at the top level of a resource block (e.g. `redrive_policy = "<url>"`)
// becomes a .url traversal. Stage 2b's cross-ref deliberately handles only
// top-level string-literal attrs — nested map/object literals (e.g.
// `dimensions = { QueueName = "..." }`) are deferred to Stage 2c's dep-chase
// work, where the tooling already needs to walk arbitrary expressions.
func TestApplyCrossRefs_ReplacesQueueURLAtTopLevel(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_lambda_event_source_mapping" "x" {
  event_source_arn = "https://sqs.us-east-1.amazonaws.com/123/orders"
  function_name    = "fanout"
}
`)
	resources := []imported.ImportedResource{
		ir("aws_sqs_queue.orders", "https://sqs.us-east-1.amazonaws.com/123/orders",
			map[string]string{"url": "https://sqs.us-east-1.amazonaws.com/123/orders"}),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "event_source_arn = aws_sqs_queue.orders.url") {
		t.Errorf("queue URL must become a .url traversal\n--- got ---\n%s", out)
	}
}

// TestApplyCrossRefs_NestedLiteralUntouched documents the deferred-to-2c
// limitation explicitly so a future contributor can find it: literals nested
// inside object/map values are NOT cross-referenced by Stage 2b. A mutation
// that silently reaches into nested expressions would change behavior and
// should fail this test.
func TestApplyCrossRefs_NestedLiteralUntouched(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_cloudwatch_metric_alarm" "x" {
  dimensions = {
    QueueName = "https://sqs.us-east-1.amazonaws.com/123/orders"
  }
}
`)
	resources := []imported.ImportedResource{
		ir("aws_sqs_queue.orders", "https://sqs.us-east-1.amazonaws.com/123/orders",
			map[string]string{"url": "https://sqs.us-east-1.amazonaws.com/123/orders"}),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"https://sqs.us-east-1.amazonaws.com/123/orders"`) {
		t.Errorf("nested literal must remain literal in Stage 2b (Stage 2c handles nested deps)\n--- got ---\n%s", out)
	}
}

// TestApplyCrossRefs_LeavesUnknownLiteralsAlone pins the conservative
// contract: literals that don't match an in-batch resource are not touched,
// even if they look like ARNs. A mutation that broadens the regex would
// turn external ARNs into broken traversals.
func TestApplyCrossRefs_LeavesUnknownLiteralsAlone(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_iam_role_policy_attachment" "x" {
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
  role       = "some-role"
}
`)
	out, err := applyCrossRefs(in, []imported.ImportedResource{}, "") // empty index
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"arn:aws:iam::aws:policy/AdministratorAccess"`) {
		t.Errorf("unknown ARN literal must be preserved\n--- got ---\n%s", out)
	}
}

// TestApplyCrossRefs_DoesNotSelfReference pins that a resource whose own
// ARN appears in its own body (e.g. `aws_kms_key.foo.arn` inside a
// `aws_kms_alias.foo.target_key_id` reverse-emit edge case) is not turned
// into a self-loop. Without this guard, terraform validate fails with
// `Self-referential dependency`.
func TestApplyCrossRefs_DoesNotSelfReference(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "orders" {
  name = "orders"
  arn  = "arn:aws:sqs:us-east-1:123:orders"
}
`)
	resources := []imported.ImportedResource{
		ir("aws_sqs_queue.orders", "arn:aws:sqs:us-east-1:123:orders",
			map[string]string{"arn": "arn:aws:sqs:us-east-1:123:orders"}),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "arn = aws_sqs_queue.orders.arn") {
		t.Errorf("must not produce self-referential traversal\n--- got ---\n%s", out)
	}
}

// TestApplyCrossRefs_HonorsImportIDOverArn pins the lookup priority: when
// only ImportID is populated (no NativeIDs.arn), an ARN-shaped ImportID
// still maps to the .arn attribute on the target. A mutation that flipped
// this to .id would emit `aws_kms_key.x.id` (which is the key UUID, not the
// ARN) and downstream references would silently break.
func TestApplyCrossRefs_HonorsImportIDOverArn(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_iam_role_policy_attachment" "x" {
  policy_arn = "arn:aws:iam::123:policy/foo"
  role       = "some-role"
}
`)
	resources := []imported.ImportedResource{
		ir("aws_iam_policy.foo", "arn:aws:iam::123:policy/foo", nil), // ImportID only
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "policy_arn = aws_iam_policy.foo.arn") {
		t.Errorf("ARN-shaped ImportID must map to .arn, not .id\n--- got ---\n%s", out)
	}
}

func TestApplyCrossRefs_NoResourcesNoChange(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" }
`)
	out, err := applyCrossRefs(in, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("empty resource list must produce identical output\n--- want ---\n%s\n--- got ---\n%s", in, out)
	}
}

// TestApplyCrossRefs_DBInstanceReplicateSourceUsesARN pins the
// per-attribute ARN override (#360). When the consumer's attribute is
// aws_db_instance.replicate_source_db, the index's default .id target
// is overridden to .arn — the AWS provider rejects an identifier-form
// reference with "replicate_source_db must be an ARN when
// db_subnet_group_name is set."
func TestApplyCrossRefs_DBInstanceReplicateSourceUsesARN(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_db_instance" "replica" {
  identifier           = "io-foo-rds0-replica-1"
  replicate_source_db  = "io-foo-rds0"
  db_subnet_group_name = "io-foo-rds-subnets"
}
`)
	resources := []imported.ImportedResource{
		ir("aws_db_instance.io_foo_rds0", "io-foo-rds0", nil),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "replicate_source_db  = aws_db_instance.io_foo_rds0.arn") {
		t.Errorf("replicate_source_db must resolve to .arn (per crossRefAttrOverrides), not .id\n--- got ---\n%s", got)
	}
	// Sanity: only the consumer attribute is rewritten. The replica's
	// own `identifier` literal must survive intact, and the
	// replicate_source_db literal must be gone (replaced by the
	// traversal).
	if !strings.Contains(got, `identifier           = "io-foo-rds0-replica-1"`) {
		t.Errorf("identifier literal must remain intact (only replicate_source_db is rewritten)\n--- got ---\n%s", got)
	}
	if strings.Contains(got, `replicate_source_db  = "io-foo-rds0"`) {
		t.Errorf("replicate_source_db literal must have been replaced by a traversal\n--- got ---\n%s", got)
	}
}

// TestApplyCrossRefs_DBInstanceOtherAttrsUseDefault pins the
// narrowness of the override: only replicate_source_db is escalated to
// .arn. Other aws_db_instance attributes that match an in-batch
// identity should resolve to the default .id form.
func TestApplyCrossRefs_DBInstanceOtherAttrsUseDefault(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_db_instance" "primary" {
  identifier           = "io-foo-rds0"
  db_subnet_group_name = "io-foo-rds-subnets"
}
`)
	// A hypothetical case where db_subnet_group_name happens to match
	// some other in-batch identifier. Synthetic — the real composer
	// emits db_subnet_group_name = aws_db_subnet_group.X.id via
	// crossref against a literal string, so this test exercises the
	// "no override" path.
	resources := []imported.ImportedResource{
		ir("aws_db_subnet_group.subnets", "io-foo-rds-subnets", nil),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "db_subnet_group_name = aws_db_subnet_group.subnets.id") {
		t.Errorf("db_subnet_group_name must resolve to .id (default), not .arn\n--- got ---\n%s", got)
	}
}

// TestApplyCrossRefs_DBInstanceSelfReferenceUntouched pins that the
// override does not regress the self-reference guard — a replica's
// replicate_source_db pointing to its OWN identifier (which shouldn't
// happen in practice, but a misconfigured stack could produce it) is
// left as a literal.
func TestApplyCrossRefs_DBInstanceSelfReferenceUntouched(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_db_instance" "rds" {
  identifier          = "io-foo-rds0"
  replicate_source_db = "io-foo-rds0"
}
`)
	resources := []imported.ImportedResource{
		ir("aws_db_instance.rds", "io-foo-rds0", nil),
	}
	out, err := applyCrossRefs(in, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// Self-reference guard fires before the override, so the literal
	// stays as a string.
	if !strings.Contains(got, `replicate_source_db = "io-foo-rds0"`) {
		t.Errorf("self-reference must remain a literal\n--- got ---\n%s", got)
	}
}

// TestApplyCrossRefs_GCPIsNoOp pins the documented GCP scope: the
// AWS-shaped ARN/URL crossref rewriter would silently mishandle GCP
// self-link literals if it ran against google_* resources. ProviderGCP
// short-circuits before the buildCrossRefIndex pass — the input bytes
// must come through byte-identical.
func TestApplyCrossRefs_GCPIsNoOp(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "google_pubsub_topic" "x" {
  name = "io-events"
}

resource "google_pubsub_subscription" "y" {
  name  = "io-events-sub"
  topic = "projects/real-proj/topics/io-events"
}
`)
	resources := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_pubsub_topic",
			Address:  "google_pubsub_topic.x",
			ImportID: "projects/real-proj/topics/io-events",
		}},
	}
	out, err := applyCrossRefs(in, resources, ProviderGCP)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("ProviderGCP path must be a byte-identical no-op\n--- want ---\n%s\n--- got ---\n%s", in, out)
	}
}
