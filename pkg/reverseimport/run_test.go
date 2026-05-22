package reverseimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

func TestRunEmitsArtifactsAndImportSummary(t *testing.T) {
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
			tf:           fakeTerraformRunner{},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != job.StatusSucceeded {
		t.Fatalf("Status = %q, want %q", result.Status, job.StatusSucceeded)
	}
	if result.PlanSummary.ImportCount != 1 {
		t.Fatalf("ImportCount = %d, want 1", result.PlanSummary.ImportCount)
	}
	for _, name := range []string{"imported.json", "imported.tf", "providers-imported.tf", "validate.json", "tfplan.json", "plan-summary.json", "reverse-result.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
	}
	importedTF, err := os.ReadFile(filepath.Join(dir, "imported.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(importedTF), `import {`) {
		t.Fatalf("imported.tf missing import block:\n%s", importedTF)
	}
	if !strings.Contains(string(importedTF), `resource "aws_sqs_queue" "orders"`) {
		t.Fatalf("imported.tf missing queue resource:\n%s", importedTF)
	}
}

func fakeGenconfig(_ context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error) {
	if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
		return nil, err
	}
	generatedPath := filepath.Join(opts.Workdir, "generated.tf")
	if err := os.WriteFile(generatedPath, []byte(`resource "aws_sqs_queue" "orders" {
  name = "orders"
}
`), 0o644); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(opts.Workdir, "imports.tf"), []byte("import {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(opts.Workdir, "providers.tf"), []byte("terraform {}\n"), 0o644)

	out := make([]imported.ImportedResource, len(resources))
	copy(out, resources)
	out[0].Attrs = []byte(`{"name":{"literal":"orders"}}`)
	return &genconfig.Result{GeneratedPath: generatedPath, Resources: out}, nil
}

func fakeDriftfix(_ context.Context, opts driftfix.Options) (*driftfix.Result, error) {
	return &driftfix.Result{GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"), Iterations: 1}, nil
}

func fakeDepChase(_ context.Context, _ depchase.Options, resources []imported.ImportedResource) (*depchase.Result, error) {
	return &depchase.Result{Resources: resources}, nil
}

type fakeTerraformRunner struct{}

func (fakeTerraformRunner) Init(context.Context, string) error { return nil }

func (fakeTerraformRunner) Validate(context.Context, string) ([]byte, error) {
	return []byte(`{"valid":true,"diagnostics":[]}`), nil
}

func (fakeTerraformRunner) Plan(context.Context, string, string) error { return nil }

func (fakeTerraformRunner) ShowPlanJSON(context.Context, string, string) ([]byte, error) {
	return []byte(`{
  "format_version": "1.2",
  "terraform_version": "1.13.0",
  "resource_changes": [
    {
      "address": "aws_sqs_queue.orders",
      "mode": "managed",
      "type": "aws_sqs_queue",
      "name": "orders",
      "change": {
        "actions": ["no-op"],
        "before": null,
        "after": null,
        "after_unknown": {},
        "importing": {"id": "https://sqs.us-east-1.amazonaws.com/123/orders"}
      }
    }
  ]
}`), nil
}
