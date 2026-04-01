package cleanup

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

func TestExtractSchemaInfo(t *testing.T) {
	schemas := &tfjson.ProviderSchemas{
		Schemas: map[string]*tfjson.ProviderSchema{
			"registry.terraform.io/hashicorp/aws": {
				ResourceSchemas: map[string]*tfjson.Schema{
					"aws_sqs_queue": {
						Block: &tfjson.SchemaBlock{
							Attributes: map[string]*tfjson.SchemaAttribute{
								"name":     {Optional: true, Computed: true},
								"arn":      {Computed: true},  // computed-only
								"url":      {Computed: true},  // computed-only
								"tags":     {Optional: true},
								"tags_all": {Optional: true, Computed: true},
								"id":       {Optional: true, Computed: true},
							},
						},
					},
					"aws_secretsmanager_secret": {
						Block: &tfjson.SchemaBlock{
							Attributes: map[string]*tfjson.SchemaAttribute{
								"name":                           {Optional: true, Computed: true},
								"arn":                            {Computed: true}, // computed-only
								"force_overwrite_replica_secret": {Optional: true, WriteOnly: true},
								"recovery_window_in_days":        {Optional: true},
							},
						},
					},
				},
			},
		},
	}

	info := ExtractSchemaInfo(schemas)

	// SQS: arn and url should be computed-only
	sqsComputed := info.ComputedAttrsFor("aws_sqs_queue")
	if sqsComputed == nil {
		t.Fatal("expected computed attrs for aws_sqs_queue")
	}
	if !sqsComputed["arn"] {
		t.Error("arn should be computed-only for SQS")
	}
	if !sqsComputed["url"] {
		t.Error("url should be computed-only for SQS")
	}
	// name is computed+optional — should NOT be computed-only
	if sqsComputed["name"] {
		t.Error("name should NOT be computed-only (it's computed+optional)")
	}
	// tags_all is computed+optional — should NOT be computed-only
	if sqsComputed["tags_all"] {
		t.Error("tags_all should NOT be computed-only (it's computed+optional)")
	}

	// Secrets Manager: arn should be computed-only
	smComputed := info.ComputedAttrsFor("aws_secretsmanager_secret")
	if !smComputed["arn"] {
		t.Error("arn should be computed-only for SM")
	}

	// Secrets Manager: force_overwrite_replica_secret should be write-only
	smWriteOnly := info.WriteOnlyAttrsFor("aws_secretsmanager_secret")
	if smWriteOnly == nil {
		t.Fatal("expected write-only attrs for SM")
	}
	if !smWriteOnly["force_overwrite_replica_secret"] {
		t.Error("force_overwrite_replica_secret should be write-only")
	}

	// Unknown type should return nil
	if info.ComputedAttrsFor("aws_unknown") != nil {
		t.Error("unknown type should return nil computed attrs")
	}
}

func TestExtractSchemaInfo_Nil(t *testing.T) {
	info := ExtractSchemaInfo(nil)
	if info == nil {
		t.Fatal("should return non-nil SchemaInfo even for nil input")
	}
	if info.ComputedAttrsFor("aws_sqs_queue") != nil {
		t.Error("nil schema should have no computed attrs")
	}
}

func TestCleanupWithSchema(t *testing.T) {
	// Build a schema that marks 'arn' and 'stream_arn' as computed-only
	schema := &SchemaInfo{
		ComputedOnly: map[string]map[string]bool{
			"aws_dynamodb_table": {
				"arn":          true,
				"stream_arn":   true,
				"stream_label": true,
				"id":           true,
				"tags_all":     true,
			},
		},
	}

	input := `resource "aws_dynamodb_table" "t" {
  name           = "my-table"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "pk"
  arn            = "arn:aws:dynamodb:us-east-1:123:table/my-table"
  stream_arn     = ""
  stream_label   = ""
  id             = "my-table"
  tags_all       = {}

  attribute {
    name = "pk"
    type = "S"
  }
}
`
	got, err := CleanupGeneratedHCL([]byte(input), schema)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	body := parseHCLResource(t, got)

	// Schema says these are computed-only — should be removed
	for _, attr := range []string{"arn", "stream_arn", "stream_label", "id", "tags_all"} {
		if body.GetAttribute(attr) != nil {
			t.Errorf("schema-driven cleanup should remove %q", attr)
		}
	}

	// These should be kept
	for _, attr := range []string{"name", "billing_mode", "hash_key"} {
		if body.GetAttribute(attr) == nil {
			t.Errorf("schema-driven cleanup should keep %q", attr)
		}
	}
}

func TestCleanupWithSchema_WriteOnly(t *testing.T) {
	schema := &SchemaInfo{
		ComputedOnly: map[string]map[string]bool{
			"aws_secretsmanager_secret": {"arn": true},
		},
		WriteOnly: map[string]map[string]bool{
			"aws_secretsmanager_secret": {"force_overwrite_replica_secret": true},
		},
	}

	input := `resource "aws_secretsmanager_secret" "s" {
  name                           = "my-secret"
  arn                            = "arn:aws:secretsmanager:..."
  force_overwrite_replica_secret = false
  recovery_window_in_days        = null
}
`
	got, err := CleanupGeneratedHCL([]byte(input), schema)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	body := parseHCLResource(t, got)

	// arn should be removed (computed-only via schema)
	if body.GetAttribute("arn") != nil {
		t.Error("arn should be removed by schema")
	}

	// force_overwrite_replica_secret is write-only — should be in lifecycle ignore_changes
	lifecycleFound := false
	for _, block := range body.Blocks() {
		if block.Type() == "lifecycle" {
			lifecycleFound = true
		}
	}
	if !lifecycleFound {
		t.Error("write-only attrs should trigger lifecycle { ignore_changes } block")
	}

	// recovery_window_in_days should be set to default 30 (nullDefaults)
	attr := body.GetAttribute("recovery_window_in_days")
	if attr == nil {
		t.Error("recovery_window_in_days should be set to default")
	}
}

func TestWriteOnlyKeys(t *testing.T) {
	attrs := map[string]bool{
		"zebra": true,
		"alpha": true,
		"mid":   true,
	}
	got := WriteOnlyKeys(attrs)
	want := []string{"alpha", "mid", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Reuse the parseHCLResource helper from cleanup_test.go
// (it's in the same package so it's available)
var _ = cty.StringVal // ensure cty is used in test deps
