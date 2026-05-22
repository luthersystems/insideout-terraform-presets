package job_test

import (
	"encoding/json"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	reversejob "github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

func TestPlanSummaryExportsImportCount(t *testing.T) {
	summary := reversejob.PlanSummary{
		ImportCount: 3,
		AddCount:    0,
		ChangeCount: 0,
	}

	b, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal plan summary: %v", err)
	}
	if !strings.Contains(string(b), `"import_count":3`) {
		t.Fatalf("plan summary did not export import_count: %s", b)
	}

	var got reversejob.PlanSummary
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal plan summary: %v", err)
	}
	if got.ImportCount != 3 {
		t.Fatalf("ImportCount = %d, want 3", got.ImportCount)
	}
	if !got.HasNoNonImportChanges() {
		t.Fatal("expected summary with only imports to be clean")
	}
}

func TestRequestRoundTripCarriesImportIdentity(t *testing.T) {
	req := reversejob.Request{
		Version: reversejob.Version,
		Resources: []reversejob.ResourceSpec{{
			Identity: imported.ResourceIdentity{
				Cloud:     "aws",
				Type:      "aws_lambda_function",
				Address:   "aws_lambda_function.worker",
				ImportID:  "worker",
				Region:    "us-east-1",
				AccountID: "123456789012",
				NativeIDs: map[string]string{
					"arn": "arn:aws:lambda:us-east-1:123456789012:function:worker",
				},
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var got reversejob.Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if got.Version != reversejob.Version {
		t.Fatalf("Version = %d, want %d", got.Version, reversejob.Version)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("Resources length = %d, want 1", len(got.Resources))
	}
	id := got.Resources[0].Identity
	if id.Address != "aws_lambda_function.worker" || id.ImportID != "worker" {
		t.Fatalf("identity = %+v, want address and import ID preserved", id)
	}
	if id.NativeIDs["arn"] == "" {
		t.Fatalf("NativeIDs did not preserve arn: %+v", id.NativeIDs)
	}
}

func TestDecodeRequestAcceptsImportedResourceManifest(t *testing.T) {
	manifest := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.orders",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	req, err := reversejob.DecodeRequest(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.Version != reversejob.Version {
		t.Fatalf("Version = %d, want %d", req.Version, reversejob.Version)
	}
	resources := req.ImportedResources()
	if len(resources) != 1 || resources[0].Identity.Address != "aws_sqs_queue.orders" {
		t.Fatalf("ImportedResources = %+v, want aws_sqs_queue.orders", resources)
	}
}

func TestPlanSummaryFromTerraformPlanCountsImportsAndChanges(t *testing.T) {
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address: "aws_sqs_queue.orders",
				Change: &tfjson.Change{
					Actions:   tfjson.Actions{tfjson.ActionNoop},
					Importing: &tfjson.Importing{},
				},
			},
			{
				Address: "aws_sqs_queue.extra",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionCreate},
				},
			},
			{
				Address: "aws_sqs_queue.replace",
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
				},
			},
		},
	}

	got := reversejob.PlanSummaryFromTerraformPlan(plan)
	if got.ImportCount != 1 {
		t.Fatalf("ImportCount = %d, want 1", got.ImportCount)
	}
	if got.AddCount != 1 {
		t.Fatalf("AddCount = %d, want 1", got.AddCount)
	}
	if got.ReplaceCount != 1 {
		t.Fatalf("ReplaceCount = %d, want 1", got.ReplaceCount)
	}
	if got.HasNoNonImportChanges() {
		t.Fatal("summary with add/replace should not be clean")
	}
}

func TestPlanSummaryFromText(t *testing.T) {
	got, ok := reversejob.PlanSummaryFromText("Plan: 3 to import, 0 to add, 1 to change, 2 to destroy.")
	if !ok {
		t.Fatal("PlanSummaryFromText did not find summary line")
	}
	if got.ImportCount != 3 || got.AddCount != 0 || got.ChangeCount != 1 || got.DestroyCount != 2 {
		t.Fatalf("summary = %+v, want import=3 add=0 change=1 destroy=2", got)
	}
}

func TestResultRoundTripCarriesArtifactsAndImportedResources(t *testing.T) {
	result := reversejob.Result{
		Version: reversejob.Version,
		Status:  reversejob.StatusSucceeded,
		Imported: []imported.ImportedResource{{
			Identity: imported.ResourceIdentity{
				Cloud:   "aws",
				Type:    "aws_kms_key",
				Address: "aws_kms_key.primary",
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
		PlanSummary: reversejob.PlanSummary{ImportCount: 1},
		Artifacts: reversejob.ArtifactSet{
			ImportedJSON: &reversejob.Artifact{Name: "imported.json", Path: "/work/imported.json"},
			ImportedTF:   &reversejob.Artifact{Name: "imported.tf", Path: "/work/imported.tf"},
			TFPlanJSON:   &reversejob.Artifact{Name: "tfplan.json", Path: "/work/tfplan.json"},
		},
	}

	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var got reversejob.Result
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Status != reversejob.StatusSucceeded {
		t.Fatalf("Status = %q, want %q", got.Status, reversejob.StatusSucceeded)
	}
	if got.PlanSummary.ImportCount != 1 {
		t.Fatalf("ImportCount = %d, want 1", got.PlanSummary.ImportCount)
	}
	if len(got.Imported) != 1 || got.Imported[0].Identity.Address != "aws_kms_key.primary" {
		t.Fatalf("Imported = %+v, want aws_kms_key.primary", got.Imported)
	}
	if got.Artifacts.ImportedJSON == nil || got.Artifacts.ImportedJSON.Name != "imported.json" {
		t.Fatalf("ImportedJSON artifact = %+v, want imported.json", got.Artifacts.ImportedJSON)
	}
}
