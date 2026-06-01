package genconfig

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestGoldenStackValidates is the repeatable large-stack regression gate for
// invalid generated HCL (#708). It replays the post-generate-config-out
// cleanup pipeline (schema clean → fixups → un-importable prune → orphan
// prune → cross-ref → terraform validate) against a committed real-world
// capture — the Dario reverse-import selection (154 imports, 92 generated
// bodies, every quirk class #708 cares about: Lambda, ALB/target-group, SNS,
// EBS, ENI over-emission, AWS-managed KMS aliases) — and asserts the result
// passes `terraform validate`.
//
// It exercises the EXACT production path (cleanValidateExtract), the same
// function runSingleRegion calls. The only difference is generate-config-out
// is pre-captured into testdata, so the test is deterministic and needs NO
// AWS credentials — only the terraform binary + the AWS provider (which
// `terraform init` fetches). Gated by RUN_GOLDEN_HCL=1 (see
// `make test-golden-stack`) so it stays out of the plain `go test -race ./...`
// unit lane.
//
// To refresh the fixture after a provider bump or a new quirk class, re-run
// the live reverse import and re-capture generated.tf/imports.tf (the
// procedure is documented in cmd/insideout-import/genconfig/testdata/golden/README.md).
func TestGoldenStackValidates(t *testing.T) {
	if os.Getenv("RUN_GOLDEN_HCL") == "" {
		t.Skip("golden-stack HCL test: set RUN_GOLDEN_HCL=1 to run (needs terraform + AWS provider, no creds)")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("golden-stack HCL test: terraform not found on PATH")
	}

	fixtureDir := filepath.Join("testdata", "golden", "dario")
	work := t.TempDir()
	for _, name := range []string{"imports.tf", "providers.tf", generatedFile} {
		copyFixtureFile(t, filepath.Join(fixtureDir, name), filepath.Join(work, name))
	}

	ctx := context.Background()
	runner, err := newExecRunner(work, nil)
	if err != nil {
		t.Fatalf("newExecRunner: %v", err)
	}
	if err := runner.Init(ctx); err != nil {
		// init must fetch the AWS provider. A sandbox without registry
		// access can't run this gate, so skip (rather than fail) to keep
		// unprivileged environments green — CI's terraform lane has access.
		t.Skipf("golden-stack HCL test: terraform init could not fetch the provider (%v)", err)
	}

	resources := resourcesFromImportsTF(t, filepath.Join(work, "imports.tf"))
	if len(resources) == 0 {
		t.Fatal("fixture imports.tf yielded zero resources")
	}

	opts := Options{Workdir: work, Region: "us-east-1", Provider: ProviderAWS}
	out, err := cleanValidateExtract(ctx, opts, runner, ProviderAWS, "golden:dario",
		filepath.Join(work, generatedFile), resources)
	if err != nil {
		t.Fatalf("golden Dario stack failed the cleanup+validate pipeline — this is the #708 regression:\n%v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected retained resources after cleanup, got 0")
	}

	// The un-importable prune must have dropped the AWS-managed KMS aliases
	// and the NAT-gateway ENI the selection carried; pin that each was
	// recorded against its OWN address with the right reason (not just that
	// the reason strings appear somewhere), so a regression that stops
	// pruning them — or mis-tags a different resource — is caught even if
	// validate somehow passed.
	skippedRaw, err := os.ReadFile(filepath.Join(work, orphanImportsFile))
	if err != nil {
		t.Fatalf("read %s: %v", orphanImportsFile, err)
	}
	var manifest orphanImportsWrapper
	if err := json.Unmarshal(skippedRaw, &manifest); err != nil {
		t.Fatalf("decode %s: %v", orphanImportsFile, err)
	}
	reasonByAddr := map[string]string{}
	for _, s := range manifest.Imports {
		reasonByAddr[s.Address] = s.Reason
	}
	wantPruned := map[string]string{
		"aws_kms_alias.alias_aws_rds":                 reasonAWSManagedKMSAlias,
		"aws_network_interface.eni_0ce4fc160b647c275": reasonServiceManagedENI,
	}
	for addr, wantReason := range wantPruned {
		if got := reasonByAddr[addr]; got != wantReason {
			t.Errorf("imports-skipped.json: %s recorded reason %q, want %q", addr, got, wantReason)
		}
	}
	t.Logf("golden Dario stack validated: %d resource(s) retained", len(out))
}

// copyFixtureFile copies a fixture into the scratch workdir.
func copyFixtureFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// resourcesFromImportsTF reconstructs the minimal ImportedResource slice the
// cleanup pipeline needs (Address, Type, ImportID) from a captured imports.tf.
// That is sufficient for prune + cross-ref + validate; NativeIDs/Attributes
// are not needed to prove the HCL is schema-valid.
func resourcesFromImportsTF(t *testing.T, path string) []imported.ImportedResource {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read imports.tf: %v", err)
	}
	f, diags := hclwrite.ParseConfig(raw, "imports.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("parse imports.tf: %s", diags.Error())
	}
	var out []imported.ImportedResource
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "import" {
			continue
		}
		addr := traversalAddrFromAttr(blk.Body().GetAttribute("to"))
		if addr == "" {
			continue
		}
		tfType, _, _ := strings.Cut(addr, ".")
		out = append(out, imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     tfType,
				Address:  addr,
				ImportID: stringLitFromAttr(blk.Body().GetAttribute("id")),
			},
		})
	}
	return out
}
