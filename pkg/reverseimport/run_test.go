package reverseimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
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

// TestRunStreamsPhaseProgressToStdout is the regression guard for
// luthersystems/mars#178: the reverse-import engine went silent through its
// pre-plan phases, so the import wizard's live log console stalled until the
// job ended. Run must now emit a human-readable progress line at each major
// phase to the Options.Stdout writer the Mars job supplies.
func TestRunStreamsPhaseProgressToStdout(t *testing.T) {
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

	var progress bytes.Buffer
	_, err := Run(context.Background(), req, Options{
		OutputDir:    dir,
		SkipDepChase: true,
		Stdout:       &progress,
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

	got := progress.String()
	for _, want := range []string{
		"expanding selection closure",
		"generating terraform config",
		"running driftfix",
		"terraform init",
		"terraform validate",
		"terraform plan",
		"plan complete",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress stream missing %q; got:\n%s", want, got)
		}
	}
}

// TestRunWithoutStdoutStaysSilent confirms the nil-Stdout default path is
// inert: a caller (or existing test) that supplies no progress writer must
// not panic and must not write to any global stream. The run still succeeds
// and produces its artifacts.
func TestRunWithoutStdoutStaysSilent(t *testing.T) {
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
		OutputDir:    dir,
		SkipDepChase: true,
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
}

func TestRunRejectsUnimportableSelectionBeforePlan(t *testing.T) {
	cases := map[string]struct {
		identity imported.ResourceIdentity
		wantCode string
	}{
		"insideout imported marker": {
			identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.orders",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
				Region:   "us-east-1",
				Tags: map[string]string{
					"InsideOutImported":      "true",
					"InsideOutImportProject": "4b982735-ff89-4295-a3fa-8a75a554ffc9",
				},
			},
			wantCode: imported.ReasonInsideOutImported,
		},
		"aws managed kms key": {
			identity: imported.ResourceIdentity{
				Cloud:     "aws",
				Type:      "aws_kms_key",
				Address:   "aws_kms_key.aws_managed",
				ImportID:  "1234abcd-12ab-34cd-56ef-1234567890ab",
				Region:    "us-east-1",
				NativeIDs: map[string]string{"key_manager": "AWS"},
			},
			wantCode: imported.ReasonAWSManagedKMSKey,
		},
		"aws managed kms alias": {
			identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_kms_alias",
				Address:  "aws_kms_alias.aws_rds",
				ImportID: "alias/aws/rds",
				Region:   "us-east-1",
			},
			wantCode: imported.ReasonAWSManagedKMSAlias,
		},
		"service managed eni": {
			identity: imported.ResourceIdentity{
				Cloud:     "aws",
				Type:      "aws_network_interface",
				Address:   "aws_network_interface.nat",
				ImportID:  "eni-0123456789abcdef0",
				Region:    "us-east-1",
				NativeIDs: map[string]string{"interface_type": "nat_gateway"},
			},
			wantCode: imported.ReasonServiceManagedENI,
		},
		"ephemeral log stream": {
			identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_cloudwatch_log_stream",
				Address:  "aws_cloudwatch_log_stream.ephemeral",
				ImportID: "app-log-group:2026/06/03/example",
				Region:   "us-east-1",
			},
			wantCode: imported.ReasonEphemeralLogStream,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			req := job.Request{
				Version: job.Version,
				Resources: []job.ResourceSpec{{
					Identity: tc.identity,
					Tier:     imported.TierImportedFlat,
					Source:   imported.SourceImporter,
				}},
			}

			genconfigCalled := false
			result, err := Run(context.Background(), req, Options{
				OutputDir: dir,
				deps: deps{
					runGenconfig: func(context.Context, genconfig.Options, []imported.ImportedResource) (*genconfig.Result, error) {
						genconfigCalled = true
						return nil, fmt.Errorf("genconfig should not run")
					},
					runDriftfix: fakeDriftfix,
					runDepChase: fakeDepChase,
					tf:          fakeTerraformRunner{},
				},
			})
			if err == nil {
				t.Fatal("Run returned nil error for unimportable resource")
			}
			if genconfigCalled {
				t.Fatal("Run reached genconfig for unimportable resource")
			}
			if result.Status != job.StatusFailed {
				t.Fatalf("Status = %q, want %q", result.Status, job.StatusFailed)
			}
			if len(result.ValidationIssues) != 1 {
				t.Fatalf("ValidationIssues len = %d, want 1: %#v", len(result.ValidationIssues), result.ValidationIssues)
			}
			if result.ValidationIssues[0].Code != tc.wantCode {
				t.Fatalf("ValidationIssues[0].Code = %q, want %q", result.ValidationIssues[0].Code, tc.wantCode)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "reverse-result.json")); statErr != nil {
				t.Fatalf("reverse-result.json not written: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "imported.tf")); !os.IsNotExist(statErr) {
				t.Fatalf("imported.tf should not be written before rejection, stat err=%v", statErr)
			}
		})
	}
}

func TestRunEmitsAWSAssumeRoleFromProjectBundle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "outputs", "cloud-provision.json"), []byte(`{
  "terraform_role": {
    "value": "arn:aws:iam::123456789012:role/io-terraform",
    "type": "string"
  }
}`))
	mustWrite(t, filepath.Join(root, "tf", "auto-vars", "common.auto.tfvars.json"), []byte(`{
  "aws_external_id": "external-123"
}`))
	dir := filepath.Join(root, "outputs", "reverse-import")
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

	var gotGenconfig genconfig.Options
	_, err := Run(context.Background(), req, Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: func(ctx context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error) {
				gotGenconfig = opts
				return fakeGenconfig(ctx, opts, resources)
			},
			runDriftfix: fakeDriftfix,
			runDepChase: fakeDepChase,
			tf:          fakeTerraformRunner{},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotGenconfig.AWSRoleARN != "arn:aws:iam::123456789012:role/io-terraform" {
		t.Fatalf("genconfig AWSRoleARN = %q, want project Terraform role", gotGenconfig.AWSRoleARN)
	}
	if gotGenconfig.AWSExternalID != "external-123" {
		t.Fatalf("genconfig AWSExternalID = %q, want external-123", gotGenconfig.AWSExternalID)
	}
	providersTF, err := os.ReadFile(filepath.Join(dir, "providers-imported.tf"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(providersTF)
	for _, want := range []string{
		`assume_role`,
		`role_arn    = "arn:aws:iam::123456789012:role/io-terraform"`,
		`external_id = "external-123"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("providers-imported.tf missing %q:\n%s", want, s)
		}
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

func TestRunBackfillsImportedAttrsFromFinalPlan(t *testing.T) {
	dir := t.TempDir()
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
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: fakeGenconfig,
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           s3VersioningPlanRunner{},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.PlanSummary.ImportCount != 1 {
		t.Fatalf("ImportCount = %d, want 1", result.PlanSummary.ImportCount)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("Imported len = %d, want 1", len(result.Imported))
	}
	decoded, err := generated.UnmarshalAttrs("aws_s3_bucket", result.Imported[0].Attrs)
	if err != nil {
		t.Fatalf("result imported attrs did not decode: %v\n%s", err, result.Imported[0].Attrs)
	}
	bucket := decoded.(*generated.AWSS3Bucket)
	if len(bucket.Versioning) != 1 || bucket.Versioning[0].Enabled == nil || bucket.Versioning[0].Enabled.Literal == nil || !*bucket.Versioning[0].Enabled.Literal {
		t.Fatalf("result typed versioning not backfilled: %#v", bucket.Versioning)
	}

	var persisted []imported.ImportedResource
	raw, err := os.ReadFile(filepath.Join(dir, "imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || len(persisted[0].Attrs) == 0 {
		t.Fatalf("persisted imported resources missing attrs: %#v", persisted)
	}
	importedTF, err := os.ReadFile(filepath.Join(dir, "imported.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(importedTF), "versioning {") {
		t.Fatalf("imported.tf missing plan-backed versioning block:\n%s", importedTF)
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

type s3VersioningPlanRunner struct{}

func (s3VersioningPlanRunner) Init(context.Context, string) error { return nil }

func (s3VersioningPlanRunner) Validate(context.Context, string) ([]byte, error) {
	return []byte(`{"valid":true,"diagnostics":[]}`), nil
}

func (s3VersioningPlanRunner) Plan(context.Context, string, string) error { return nil }

func (s3VersioningPlanRunner) ShowPlanJSON(context.Context, string, string) ([]byte, error) {
	return []byte(`{
  "format_version": "1.2",
  "terraform_version": "1.13.0",
  "planned_values": {
    "root_module": {
      "resources": [{
        "address": "aws_s3_bucket.uploads",
        "mode": "managed",
        "type": "aws_s3_bucket",
        "name": "uploads",
        "values": {
          "bucket": "io-uploads",
          "region": "us-east-1",
          "versioning": [{
            "enabled": true,
            "mfa_delete": false
          }]
        }
      }]
    }
  },
  "resource_changes": [{
    "address": "aws_s3_bucket.uploads",
    "mode": "managed",
    "type": "aws_s3_bucket",
    "name": "uploads",
    "change": {
      "actions": ["no-op"],
      "before": null,
      "after": {
        "bucket": "io-uploads",
        "region": "us-east-1",
        "versioning": [{
          "enabled": true,
          "mfa_delete": false
        }]
      },
      "after_unknown": {},
      "importing": {"id": "io-uploads"}
    }
  }]
}`), nil
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
