package genconfig

import (
	"regexp"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

func awsSchemas(rt string, attrs map[string]*tfjson.SchemaAttribute) *tfjson.ProviderSchemas {
	return &tfjson.ProviderSchemas{
		Schemas: map[string]*tfjson.ProviderSchema{
			awsProviderKey: {
				ResourceSchemas: map[string]*tfjson.Schema{
					rt: {Block: &tfjson.SchemaBlock{Attributes: attrs}},
				},
			},
		},
	}
}

func TestCleanGenerated_DropsComputedOnly(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name = "alpha"
  arn  = "arn:aws:sqs:us-east-1:123:alpha"
}
`)
	schema := awsSchemas("aws_sqs_queue", map[string]*tfjson.SchemaAttribute{
		"name": {AttributeType: cty.String, Required: true},
		"arn":  {AttributeType: cty.String, Computed: true},
	})
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// Whitespace-tolerant negative: any `arn = ...` line variant fails.
	if regexp.MustCompile(`(?m)^\s*arn\s*=`).MatchString(got) {
		t.Errorf("Computed-only attr `arn` must be dropped (any whitespace)\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, `name = "alpha"`) {
		t.Errorf("Required attr `name` must be retained\n--- got ---\n%s", got)
	}
}

// TestCleanGenerated_MultiResourceOrder pins that two `resource` blocks
// in input order are both visited and emitted in input order. A mutation
// that broke iteration (e.g. processing only the first block, or
// reordering) would survive single-resource tests but fail this one.
func TestCleanGenerated_MultiResourceOrder(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "alpha" {
  name = "alpha"
  arn  = "arn:aws:sqs:us-east-1:123:alpha"
}

resource "aws_sqs_queue" "bravo" {
  name = "bravo"
  arn  = "arn:aws:sqs:us-east-1:123:bravo"
}
`)
	schema := awsSchemas("aws_sqs_queue", map[string]*tfjson.SchemaAttribute{
		"name": {AttributeType: cty.String, Required: true},
		"arn":  {AttributeType: cty.String, Computed: true},
	})
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// Both arn lines dropped; both names retained.
	if regexp.MustCompile(`(?m)^\s*arn\s*=`).MatchString(got) {
		t.Errorf("Computed-only `arn` must be dropped from BOTH blocks\n--- got ---\n%s", got)
	}
	for _, want := range []string{`name = "alpha"`, `name = "bravo"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q after multi-resource cleanup\n--- got ---\n%s", want, got)
		}
	}
	// Input order is preserved.
	i, j := strings.Index(got, "alpha"), strings.Index(got, "bravo")
	if i < 0 || j < 0 || i > j {
		t.Errorf("multi-resource emit reordered: positions alpha=%d bravo=%d", i, j)
	}
}

// TestCleanGenerated_MergesIntoExistingLifecycle pins that when an input
// block already has a `lifecycle` sub-block, cleanup UNIONS the declared-
// Sensitive attributes into its ignore_changes — preserving the operator's
// existing entries rather than clobbering them. The fixture uses a bare-
// identifier entry (`other`) because that is the form this package emits and
// the form existingIgnoreChangeNames recognizes; the assertion proves both the
// existing entry and the Sensitive attr survive, in order.
func TestCleanGenerated_MergesIntoExistingLifecycle(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_secretsmanager_secret_version" "x" {
  secret_id     = "arn:aws:secretsmanager:us-east-1:123:secret:foo"
  secret_string = "actual-secret"

  lifecycle {
    ignore_changes = [other]
  }
}
`)
	schema := awsSchemas("aws_secretsmanager_secret_version", map[string]*tfjson.SchemaAttribute{
		"secret_id":     {AttributeType: cty.String, Required: true},
		"secret_string": {AttributeType: cty.String, Optional: true, Sensitive: true},
	})
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// Only one lifecycle block — must NOT have appended a second one.
	if strings.Count(got, "lifecycle {") != 1 {
		t.Errorf("expected exactly 1 lifecycle block, got %d:\n%s", strings.Count(got, "lifecycle {"), got)
	}
	// The existing entry is preserved AND the Sensitive attr is unioned in,
	// in order: [other, secret_string]. (A clobbering impl would drop `other`.)
	if !regexp.MustCompile(`ignore_changes\s*=\s*\[other,\s+secret_string\]`).MatchString(got) {
		t.Errorf("ignore_changes must union existing `other` with Sensitive `secret_string` -> [other, secret_string]\n--- got ---\n%s", got)
	}
}

// TestCleanGenerated_KeepsComputedOptional pins that an Optional+Computed
// attribute (e.g. `kms_master_key_id` on aws_sqs_queue, where the user can
// override but Terraform can also compute the default) is RETAINED.
// A drop-on-Computed-alone mutation would silently strip these.
func TestCleanGenerated_KeepsComputedOptional(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name              = "alpha"
  kms_master_key_id = "alias/aws/sqs"
}
`)
	schema := awsSchemas("aws_sqs_queue", map[string]*tfjson.SchemaAttribute{
		"name":              {AttributeType: cty.String, Required: true},
		"kms_master_key_id": {AttributeType: cty.String, Optional: true, Computed: true},
	})
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kms_master_key_id") {
		t.Errorf("Optional+Computed attr must be retained; cleaner over-pruned\n--- got ---\n%s", out)
	}
}

func TestCleanGenerated_AddsLifecycleForSensitive(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_secretsmanager_secret_version" "x" {
  secret_id     = "arn:aws:secretsmanager:us-east-1:123:secret:foo"
  secret_string = "actual-secret-value"
}
`)
	schema := awsSchemas("aws_secretsmanager_secret_version", map[string]*tfjson.SchemaAttribute{
		"secret_id":     {AttributeType: cty.String, Required: true},
		"secret_string": {AttributeType: cty.String, Optional: true, Sensitive: true},
	})
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "lifecycle") {
		t.Errorf("Sensitive attr must trigger a lifecycle block\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "ignore_changes") {
		t.Errorf("Sensitive attr must add ignore_changes\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "secret_string") {
		t.Errorf("ignore_changes list must mention secret_string\n--- got ---\n%s", got)
	}
}

// TestCleanGenerated_NoSensitiveNoLifecycle pins the negative: when no
// Sensitive attrs are present, no lifecycle block is added (a drop-test
// for an over-eager cleaner that always emits one).
func TestCleanGenerated_NoSensitiveNoLifecycle(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" }
`)
	schema := awsSchemas("aws_sqs_queue", map[string]*tfjson.SchemaAttribute{
		"name": {AttributeType: cty.String, Required: true},
	})
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "lifecycle") {
		t.Errorf("no-Sensitive cleanup must NOT add a lifecycle block\n--- got ---\n%s", out)
	}
}

// TestCleanGenerated_UnknownTypePassedThrough pins that a resource whose
// schema is missing from the response (provider drift, partial init, etc.)
// is left alone rather than corrupted. The validator step downstream will
// surface real syntax issues.
func TestCleanGenerated_UnknownTypePassedThrough(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_brand_new" "x" { foo = "bar" }
`)
	schema := awsSchemas("aws_other", map[string]*tfjson.SchemaAttribute{}) // no aws_brand_new
	out, err := cleanGenerated(in, schema, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `foo = "bar"`) {
		t.Errorf("unknown resource type must be preserved\n--- got ---\n%s", out)
	}
}

func TestCleanGenerated_EmptySchemaErrors(t *testing.T) {
	t.Parallel()
	for name, schema := range map[string]*tfjson.ProviderSchemas{
		"nil":         nil,
		"empty-map":   {Schemas: map[string]*tfjson.ProviderSchema{}},
		"missing-aws": {Schemas: map[string]*tfjson.ProviderSchema{"other": {}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := cleanGenerated([]byte(`resource "aws_sqs_queue" "x" {}`), schema, ""); err == nil {
				t.Error("expected error for missing AWS schema")
			}
		})
	}
}

// TestCleanGenerated_GCPSchemaKey pins that ProviderGCP routes cleanup
// through the hashicorp/google schema bucket, not hashicorp/aws. A
// mutation that hardcoded awsProviderKey would silently leave google_*
// resources unprocessed (Computed-only attrs leaking through, no
// Sensitive-attr lifecycle escalation).
func TestCleanGenerated_GCPSchemaKey(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "google_pubsub_topic" "x" {
  name = "io-events"
  etag = "abc"
}
`)
	schemas := &tfjson.ProviderSchemas{
		Schemas: map[string]*tfjson.ProviderSchema{
			gcpProviderKey: {
				ResourceSchemas: map[string]*tfjson.Schema{
					"google_pubsub_topic": {Block: &tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{
						"name": {AttributeType: cty.String, Required: true},
						"etag": {AttributeType: cty.String, Computed: true},
					}}},
				},
			},
		},
	}
	out, err := cleanGenerated(in, schemas, ProviderGCP)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if regexp.MustCompile(`(?m)^\s*etag\s*=`).MatchString(got) {
		t.Errorf("Computed-only `etag` must be dropped on GCP path\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, `name = "io-events"`) {
		t.Errorf("Required attr must survive\n--- got ---\n%s", got)
	}
}

// TestCleanGenerated_GCPProviderMissingErrors pins that a Provider == "gcp"
// run with only AWS schemas in the response surfaces an explicit error
// (rather than silently passing every block through unchanged).
func TestCleanGenerated_GCPProviderMissingErrors(t *testing.T) {
	t.Parallel()
	awsOnly := awsSchemas("aws_sqs_queue", map[string]*tfjson.SchemaAttribute{
		"name": {AttributeType: cty.String, Required: true},
	})
	if _, err := cleanGenerated([]byte(`resource "google_pubsub_topic" "x" {}`), awsOnly, ProviderGCP); err == nil {
		t.Error("expected error for GCP provider when only AWS schema is present")
	}
}
