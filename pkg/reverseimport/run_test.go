package reverseimport

import (
	"context"
	"fmt"
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

func TestRunExpandsSelectedParentClosure(t *testing.T) {
	dir := t.TempDir()
	discoverer := &fakeClosureDiscoverer{
		resources: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_s3_bucket",
					Address:  "aws_s3_bucket.discovered_uploads",
					ImportID: "io-uploads",
					Region:   "us-east-1",
				},
				Tier:   imported.TierImportedFlat,
				Source: imported.SourceImporter,
			},
			{
				Identity: imported.ResourceIdentity{
					Cloud:         "aws",
					Type:          "aws_s3_bucket_versioning",
					Address:       "aws_s3_bucket_versioning.uploads",
					ImportID:      "io-uploads",
					Region:        "us-east-1",
					ParentAddress: "aws_s3_bucket.discovered_uploads",
				},
				Tier:   imported.TierImportedFlat,
				Source: imported.SourceImporter,
			},
		},
	}
	req := job.Request{
		Version: job.Version,
		Resources: []job.ResourceSpec{{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.uploads",
				ImportID: "io-uploads",
				Region:   "us-east-1",
			},
			Tier:   imported.TierImportedFlat,
			Source: imported.SourceImporter,
		}},
	}

	result, err := Run(context.Background(), req, Options{
		OutputDir:         dir,
		SkipDepChase:      true,
		DiscoverProject:   "io-test",
		DiscoverRegions:   []string{"us-east-1"},
		ClosureDiscoverer: discoverer,
		deps: deps{
			runGenconfig: fakeGenconfig,
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           fakeTerraformRunner{importCount: 2},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PlanSummary.ImportCount != 2 {
		t.Fatalf("ImportCount = %d, want 2", result.PlanSummary.ImportCount)
	}
	if len(result.Imported) != 2 {
		t.Fatalf("Imported len = %d, want 2", len(result.Imported))
	}
	if !containsString(discoverer.req.ParentTypes, "aws_s3_bucket") {
		t.Fatalf("ParentTypes = %v, want aws_s3_bucket", discoverer.req.ParentTypes)
	}
	if !containsString(discoverer.req.ChildTypes, "aws_s3_bucket_versioning") {
		t.Fatalf("ChildTypes = %v, want aws_s3_bucket_versioning", discoverer.req.ChildTypes)
	}
	parentResult, ok := resourceResultByAddress(result.Resources, "aws_s3_bucket.uploads")
	if !ok {
		t.Fatalf("missing parent resource result: %#v", result.Resources)
	}
	if len(parentResult.Dependencies) != 1 {
		t.Fatalf("parent dependencies = %#v, want one closure child", parentResult.Dependencies)
	}
	child := parentResult.Dependencies[0]
	if child.Type != "aws_s3_bucket_versioning" {
		t.Fatalf("dependency type = %q, want aws_s3_bucket_versioning", child.Type)
	}
	if child.ParentAddress != "aws_s3_bucket.uploads" {
		t.Fatalf("dependency ParentAddress = %q, want selected parent address", child.ParentAddress)
	}
	importedTF, err := os.ReadFile(filepath.Join(dir, "imported.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(importedTF), `resource "aws_s3_bucket_versioning" "uploads"`) {
		t.Fatalf("imported.tf missing versioning child:\n%s", importedTF)
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
	for i := range out {
		switch out[i].Identity.Type {
		case "aws_sqs_queue":
			out[i].Attrs = []byte(`{"name":{"literal":"orders"}}`)
		case "aws_s3_bucket":
			out[i].Attrs = []byte(`{"bucket":{"literal":"io-uploads"},"region":{"literal":"us-east-1"}}`)
		case "aws_s3_bucket_versioning":
			out[i].Attrs = []byte(`{"bucket":{"literal":"io-uploads"},"region":{"literal":"us-east-1"},"versioning_configuration":[{"status":{"literal":"Enabled"}}]}`)
		}
	}
	return &genconfig.Result{GeneratedPath: generatedPath, Resources: out}, nil
}

func fakeDriftfix(_ context.Context, opts driftfix.Options) (*driftfix.Result, error) {
	return &driftfix.Result{GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"), Iterations: 1}, nil
}

func fakeDepChase(_ context.Context, _ depchase.Options, resources []imported.ImportedResource) (*depchase.Result, error) {
	return &depchase.Result{Resources: resources}, nil
}

type fakeTerraformRunner struct {
	importCount int
}

func (fakeTerraformRunner) Init(context.Context, string) error { return nil }

func (fakeTerraformRunner) Validate(context.Context, string) ([]byte, error) {
	return []byte(`{"valid":true,"diagnostics":[]}`), nil
}

func (fakeTerraformRunner) Plan(context.Context, string, string) error { return nil }

func (r fakeTerraformRunner) ShowPlanJSON(context.Context, string, string) ([]byte, error) {
	importCount := r.importCount
	if importCount == 0 {
		importCount = 1
	}
	var changes strings.Builder
	for i := 0; i < importCount; i++ {
		if i > 0 {
			changes.WriteString(",")
		}
		fmt.Fprintf(&changes, `{
      "address": "aws_sqs_queue.orders_%d",
      "mode": "managed",
      "type": "aws_sqs_queue",
      "name": "orders_%d",
      "change": {
        "actions": ["no-op"],
        "before": null,
        "after": null,
        "after_unknown": {},
        "importing": {"id": "https://sqs.us-east-1.amazonaws.com/123/orders_%d"}
      }
    }`, i, i, i)
	}
	return []byte(fmt.Sprintf(`{
  "format_version": "1.2",
  "terraform_version": "1.13.0",
  "resource_changes": [%s]
}`, changes.String())), nil
}

type fakeClosureDiscoverer struct {
	req       ClosureRequest
	resources []imported.ImportedResource
}

func (f *fakeClosureDiscoverer) DiscoverClosure(_ context.Context, req ClosureRequest) ([]imported.ImportedResource, error) {
	f.req = req
	return f.resources, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func resourceResultByAddress(resources []job.ResourceResult, address string) (job.ResourceResult, bool) {
	for _, resource := range resources {
		if resource.Identity.Address == address {
			return resource, true
		}
	}
	return job.ResourceResult{}, false
}
