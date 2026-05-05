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
	out, err := applyCrossRefs(in, resources)
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
	out, err := applyCrossRefs(in, resources)
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
	out, err := applyCrossRefs(in, resources)
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
	out, err := applyCrossRefs(in, []imported.ImportedResource{}) // empty index
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
	out, err := applyCrossRefs(in, resources)
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
	out, err := applyCrossRefs(in, resources)
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
	out, err := applyCrossRefs(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("empty resource list must produce identical output\n--- want ---\n%s\n--- got ---\n%s", in, out)
	}
}
