package reverseimport

import (
	"encoding/json"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestBackfillImportedAttrsFromPlanFillsMissingTypedBlocks(t *testing.T) {
	resources := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.state",
			ImportID: "luther-state",
			Region:   "us-east-1",
		},
		Tier:       imported.TierImportedFlat,
		Source:     imported.SourceImporter,
		Attributes: map[string]any{"bucket": "luther-state", "tags": map[string]any{"Project": "core"}},
		Attrs:      json.RawMessage(`{"bucket":{"literal":"luther-state"},"tags":{"Project":{"literal":"core"}}}`),
	}}
	plan := &tfjson.Plan{
		PlannedValues: &tfjson.StateValues{RootModule: &tfjson.StateModule{Resources: []*tfjson.StateResource{{
			Address: "aws_s3_bucket.state",
			Mode:    tfjson.ManagedResourceMode,
			Type:    "aws_s3_bucket",
			Name:    "state",
			AttributeValues: map[string]any{
				"arn":    "arn:aws:s3:::luther-state",
				"bucket": "luther-state",
				"tags": map[string]any{
					"InsideOutImported": "true",
					"Project":           "core",
				},
				"versioning": []any{map[string]any{
					"enabled":    true,
					"mfa_delete": false,
				}},
				"server_side_encryption_configuration": []any{map[string]any{
					"rule": []any{map[string]any{
						"bucket_key_enabled": false,
						"apply_server_side_encryption_by_default": []any{map[string]any{
							"kms_master_key_id": "arn:aws:kms:us-east-1:123:key/abc",
							"sse_algorithm":     "aws:kms",
						}},
					}},
				}},
			},
		}}}},
	}

	got, changed, err := BackfillImportedAttrsFromPlan(resources, plan)
	if err != nil {
		t.Fatalf("BackfillImportedAttrsFromPlan: %v", err)
	}
	if !changed {
		t.Fatal("BackfillImportedAttrsFromPlan changed=false, want true")
	}
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1", len(got))
	}
	if _, ok := got[0].Attributes["arn"]; ok {
		t.Fatalf("computed-only arn must not be backfilled into legacy Attributes: %#v", got[0].Attributes)
	}
	tags, ok := got[0].Attributes["tags"].(map[string]any)
	if !ok {
		t.Fatalf("legacy tags = %#v, want map[string]any", got[0].Attributes["tags"])
	}
	if tags["InsideOutImported"] != nil {
		t.Fatalf("existing tags must not be overwritten by plan/provenance tags: %#v", tags)
	}
	versioning, ok := got[0].Attributes["versioning"].([]any)
	if !ok || len(versioning) != 1 {
		t.Fatalf("legacy versioning = %#v, want one block", got[0].Attributes["versioning"])
	}

	decoded, err := generated.UnmarshalAttrs("aws_s3_bucket", got[0].Attrs)
	if err != nil {
		t.Fatalf("typed attrs did not decode: %v\n%s", err, got[0].Attrs)
	}
	bucket := decoded.(*generated.AWSS3Bucket)
	if len(bucket.Versioning) != 1 || bucket.Versioning[0].Enabled == nil || bucket.Versioning[0].Enabled.Literal == nil || !*bucket.Versioning[0].Enabled.Literal {
		t.Fatalf("typed versioning not backfilled: %#v", bucket.Versioning)
	}
	if len(bucket.ServerSideEncryptionConfiguration) != 1 {
		t.Fatalf("typed server_side_encryption_configuration not backfilled: %#v", bucket.ServerSideEncryptionConfiguration)
	}
	if _, ok := bucket.Tags["InsideOutImported"]; ok {
		t.Fatalf("typed tags must preserve the generated config value, got %#v", bucket.Tags)
	}
}

func TestBackfillImportedAttrsFromPlanSkipsSensitivePlanValues(t *testing.T) {
	resources := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.orders",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
			Region:   "us-east-1",
		},
		Attrs: json.RawMessage(`{"name":{"literal":"orders"}}`),
	}}
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.orders",
		Mode:    tfjson.ManagedResourceMode,
		Type:    "aws_sqs_queue",
		Name:    "orders",
		Change: &tfjson.Change{
			After:          map[string]any{"name": "orders", "kms_master_key_id": "secret-key"},
			AfterSensitive: map[string]any{"kms_master_key_id": true},
		},
	}}}

	got, changed, err := BackfillImportedAttrsFromPlan(resources, plan)
	if err != nil {
		t.Fatalf("BackfillImportedAttrsFromPlan: %v", err)
	}
	if changed {
		t.Fatalf("sensitive-only plan value should not change resource: %#v", got[0].Attributes)
	}
	if string(got[0].Attrs) != `{"name":{"literal":"orders"}}` {
		t.Fatalf("Attrs changed despite only sensitive missing fields: %s", got[0].Attrs)
	}
}
