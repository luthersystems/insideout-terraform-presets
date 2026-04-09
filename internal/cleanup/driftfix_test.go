package cleanup

import (
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func TestFixDriftFromPlan_NoDrift(t *testing.T) {
	hcl := `resource "google_storage_bucket" "b" {
  name     = "my-bucket"
  location = "US-CENTRAL1"
}
`
	// Plan with no changes — should return HCL unchanged
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "google_storage_bucket.b",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{"no-op"},
				},
			},
		},
	}

	got, err := FixDriftFromPlan([]byte(hcl), plan)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if string(got) != hcl {
		t.Error("HCL should be unchanged when there's no drift")
	}
}

func TestFixDriftFromPlan_WithDrift(t *testing.T) {
	hcl := `resource "google_storage_bucket" "b" {
  name     = "my-bucket"
  location = "US-CENTRAL1"
}
`
	// Plan shows terraform_labels drifting
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "google_storage_bucket.b",
				Type:    "google_storage_bucket",
				Name:    "b",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{"update"},
					Before: map[string]interface{}{
						"name":             "my-bucket",
						"terraform_labels": map[string]interface{}{},
					},
					After: map[string]interface{}{
						"name": "my-bucket",
						"terraform_labels": map[string]interface{}{
							"goog-terraform-provisioned": "true",
						},
					},
				},
			},
		},
	}

	got, err := FixDriftFromPlan([]byte(hcl), plan)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	output := string(got)

	// Should have lifecycle { ignore_changes = [terraform_labels] }
	if !strings.Contains(output, "lifecycle") {
		t.Fatal("should add lifecycle block for drifting resource")
	}
	if !strings.Contains(output, "terraform_labels") {
		t.Error("ignore_changes should contain terraform_labels")
	}
	// Should NOT ignore 'name' (it didn't drift)
	body := parseHCLResource(t, got)
	for _, block := range body.Blocks() {
		if block.Type() == "lifecycle" {
			ic := block.Body().GetAttribute("ignore_changes")
			if ic != nil {
				icStr := string(ic.Expr().BuildTokens(nil).Bytes())
				if strings.Contains(icStr, "name") {
					t.Error("ignore_changes should NOT contain 'name' (it didn't drift)")
				}
			}
		}
	}
}

func TestFixDriftFromPlan_MultipleDriftingAttrs(t *testing.T) {
	hcl := `resource "aws_secretsmanager_secret" "s" {
  name                    = "my-secret"
  recovery_window_in_days = 30
}
`
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "aws_secretsmanager_secret.s",
				Type:    "aws_secretsmanager_secret",
				Name:    "s",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{"update"},
					Before: map[string]interface{}{
						"name":                           "my-secret",
						"recovery_window_in_days":        nil,
						"force_overwrite_replica_secret": nil,
					},
					After: map[string]interface{}{
						"name":                           "my-secret",
						"recovery_window_in_days":        float64(30),
						"force_overwrite_replica_secret": false,
					},
				},
			},
		},
	}

	got, err := FixDriftFromPlan([]byte(hcl), plan)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	output := string(got)
	if !strings.Contains(output, "force_overwrite_replica_secret") {
		t.Error("should ignore force_overwrite_replica_secret (drifted)")
	}
	if !strings.Contains(output, "recovery_window_in_days") {
		t.Error("should ignore recovery_window_in_days (drifted)")
	}
}

func TestFixDriftFromPlan_LambdaDrift(t *testing.T) {
	hcl := `resource "aws_lambda_function" "f" {
  function_name = "my-func"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  role          = "arn:aws:iam::123:role/r"
  filename      = "placeholder.zip"
}
`
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "aws_lambda_function.f",
				Type:    "aws_lambda_function",
				Name:    "f",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{"update"},
					Before: map[string]interface{}{
						"function_name":    "my-func",
						"filename":         nil,
						"source_code_hash": nil,
						"publish":          nil,
					},
					After: map[string]interface{}{
						"function_name":    "my-func",
						"filename":         "placeholder.zip",
						"source_code_hash": "abc123",
						"publish":          false,
					},
				},
			},
		},
	}

	got, err := FixDriftFromPlan([]byte(hcl), plan)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	output := string(got)
	for _, attr := range []string{"filename", "source_code_hash", "publish"} {
		if !strings.Contains(output, attr) {
			t.Errorf("should ignore %q (drifted)", attr)
		}
	}
	// function_name should NOT be ignored (same before/after)
	body := parseHCLResource(t, got)
	for _, block := range body.Blocks() {
		if block.Type() == "lifecycle" {
			ic := block.Body().GetAttribute("ignore_changes")
			if ic != nil {
				icStr := string(ic.Expr().BuildTokens(nil).Bytes())
				if strings.Contains(icStr, "function_name") {
					t.Error("should NOT ignore function_name (it didn't drift)")
				}
			}
		}
	}
}

func TestFixDriftFromPlan_NilPlan(t *testing.T) {
	hcl := `resource "aws_sqs_queue" "q" { name = "q" }
`
	got, err := FixDriftFromPlan([]byte(hcl), nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if string(got) != hcl {
		t.Error("nil plan should return HCL unchanged")
	}
}

func TestFixDriftFromPlan_ImportOnly(t *testing.T) {
	hcl := `resource "aws_sqs_queue" "q" { name = "q" }
`
	// Import-only action (no update) — should not add lifecycle
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "aws_sqs_queue.q",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{"create"},
				},
			},
		},
	}

	got, err := FixDriftFromPlan([]byte(hcl), plan)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(string(got), "lifecycle") {
		t.Error("import-only action should not add lifecycle")
	}
}
