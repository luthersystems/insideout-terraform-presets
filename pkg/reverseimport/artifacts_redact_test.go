package reverseimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// artifactLeakExternalID is a fake external ID. The job writes the real one
// as a literal into providers-imported.tf and genconfig/providers.tf (#960).
const artifactLeakExternalID = "external-id-artifact-leak-960"

// genconfigWithAssumeRole writes a genconfig providers.tf that holds the
// external ID, as the real genconfig pass does for an AWS run.
func genconfigWithAssumeRole(ctx context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error) {
	res, err := fakeGenconfig(ctx, opts, resources)
	if err != nil {
		return nil, err
	}
	body := "provider \"aws\" {\n  assume_role {\n    role_arn    = \"" + opts.AWSRoleARN + "\"\n    external_id = \"" + opts.AWSExternalID + "\"\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(opts.Workdir, "providers.tf"), []byte(body), 0o600); err != nil {
		return nil, err
	}
	return res, nil
}

// allArtifacts lists every artifact a run reports, the named ones and the
// debug set, so a new artifact is checked without a change to this test.
func allArtifacts(set job.ArtifactSet) []job.Artifact {
	var out []job.Artifact
	for _, a := range []*job.Artifact{set.ImportedJSON, set.ImportedTF, set.ValidateJSON, set.TFPlanJSON, set.PlanSummaryJSON, set.ResultJSON} {
		if a != nil {
			out = append(out, *a)
		}
	}
	return append(out, set.Debug...)
}

// TestRunArtifactsHoldNoCredential is the #960 guard: no artifact a run
// reports for upload holds the external ID or a credential tfvar value, and
// tfplan.bin (which holds every input variable value) is not one of them.
// The provider files Terraform reads keep the real external ID.
func TestRunArtifactsHoldNoCredential(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "outputs", "cloud-provision.json"), []byte(`{
  "terraform_role": {"value": "arn:aws:iam::123456789012:role/io-terraform", "type": "string"}
}`))
	mustWrite(t, filepath.Join(root, "tf", "auto-vars", "common.auto.tfvars.json"), []byte(`{
  "aws_external_id": "`+artifactLeakExternalID+`"
}`))
	t.Setenv("TF_VAR_aws_external_id", "")
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
	result, err := Run(context.Background(), req, Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: genconfigWithAssumeRole,
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           credentialVarsRunner{},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	arts := allArtifacts(result.Artifacts)
	var sawProviders, sawGenconfigProviders bool
	for _, a := range arts {
		if filepath.Base(a.Path) == tfplanFile || a.Name == tfplanFile {
			t.Errorf("tfplan.bin is an artifact: %+v", a)
		}
		body, err := os.ReadFile(a.Path)
		if err != nil {
			t.Fatalf("read artifact %s: %v", a.Path, err)
		}
		for _, secret := range []string{artifactLeakExternalID, tfplanLeakValue} {
			if strings.Contains(string(body), secret) {
				t.Errorf("artifact %s (%s) holds a credential", a.Name, a.Path)
			}
		}
		switch a.Name {
		case importedProvidersTF:
			sawProviders = true
		case "providers.tf":
			sawGenconfigProviders = true
		}
	}
	if !sawProviders || !sawGenconfigProviders {
		t.Fatalf("debug artifacts lack the provider files (providers-imported.tf %v, genconfig providers.tf %v): %+v", sawProviders, sawGenconfigProviders, arts)
	}

	// Terraform reads these in place, so they keep the real value.
	for _, p := range []string{filepath.Join(dir, importedProvidersTF), filepath.Join(dir, "genconfig", "providers.tf")} {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `external_id = "`+artifactLeakExternalID+`"`) {
			t.Errorf("%s lost the external ID Terraform needs:\n%s", p, body)
		}
	}
}
