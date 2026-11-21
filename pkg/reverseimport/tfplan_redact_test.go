package reverseimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// tfplanLeakValue is a fake credential planted as a tfvar value, where
// `terraform show -json` writes the real ones (luthersystems/reliable#2553).
const tfplanLeakValue = "AKIAREVERSEIMPORTLEAK1"

// credentialVarsRunner returns the fake plan with an input variable that
// carries a credential, as the real plan does once the job's credential
// tfvars are set.
type credentialVarsRunner struct{ fakeTerraformRunner }

func (r credentialVarsRunner) ShowPlanJSON(ctx context.Context, dir, planPath string) ([]byte, error) {
	raw, err := r.fakeTerraformRunner.ShowPlanJSON(ctx, dir, planPath)
	if err != nil {
		return nil, err
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	plan["variables"] = map[string]any{"aws_access_key_id": map[string]any{"value": tfplanLeakValue}}
	return json.Marshal(plan)
}

// TestRunWritesRedactedTFPlanJSON: the tfplan.json the job leaves for upload
// holds no credential tfvar value, and the plan summary, read from the exact
// plan, is unchanged.
func TestRunWritesRedactedTFPlanJSON(t *testing.T) {
	dir := t.TempDir()
	req := job.Request{
		Version: job.Version,
		Resources: []job.ResourceSpec{{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.orders",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
				Region:   "us-east-1",
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
	}
	result, err := Run(context.Background(), req, Options{
		OutputDir:       dir,
		SkipDepChase:    true,
		ImportProjectID: "io-test",
		ImportSessionID: "sess-test",
		deps: deps{
			runGenconfig: fakeGenconfig,
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           credentialVarsRunner{},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PlanSummary.ImportCount != 1 {
		t.Fatalf("ImportCount = %d, want 1", result.PlanSummary.ImportCount)
	}
	written, err := os.ReadFile(filepath.Join(dir, "tfplan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), tfplanLeakValue) {
		t.Fatalf("tfplan.json still holds the credential tfvar value:\n%s", written)
	}
	if !strings.Contains(string(written), `"aws_access_key_id"`) {
		t.Fatalf("tfplan.json lost the variable name:\n%s", written)
	}
	if !strings.Contains(string(written), "aws_sqs_queue.orders") {
		t.Fatalf("tfplan.json lost its resource changes:\n%s", written)
	}
}

// badPlanRunner returns plan bytes that are not one JSON object, so the
// redaction fails.
type badPlanRunner struct{ fakeTerraformRunner }

func (badPlanRunner) ShowPlanJSON(context.Context, string, string) ([]byte, error) {
	return []byte(`{"format_version":"1.2","variables":{}} {}`), nil
}

// TestRunRemovesStaleTFPlanJSONWhenRedactionFails: a plan that cannot be
// redacted fails the run, and a tfplan.json left by an earlier pass is not
// left for upload.
func TestRunRemovesStaleTFPlanJSONWhenRedactionFails(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "tfplan.json")
	if err := os.WriteFile(stale, []byte(`{"stale":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	req := job.Request{
		Version: job.Version,
		Resources: []job.ResourceSpec{{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.orders",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
				Region:   "us-east-1",
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
	}
	_, err := Run(context.Background(), req, Options{
		OutputDir:       dir,
		SkipDepChase:    true,
		ImportProjectID: "io-test",
		ImportSessionID: "sess-test",
		deps: deps{
			runGenconfig: fakeGenconfig,
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           badPlanRunner{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "redact tfplan.json") {
		t.Fatalf("Run error = %v, want a redaction error", err)
	}
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Fatalf("stale tfplan.json still on disk (stat err %v)", statErr)
	}
}
