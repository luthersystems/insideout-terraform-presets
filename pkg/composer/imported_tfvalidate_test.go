//go:build tfvalidate

// Build-tagged Terraform-validate harness for the imported.tf codegen.
//
// Normal `go test` skips this file (it shells out to the `terraform`
// binary and does a one-time provider download). Run it explicitly:
//
//	go test -tags tfvalidate -run TestImportedTF_TerraformValidate -v ./pkg/composer/
//
// It emits imported.tf via EmitImportedTF for a set of representative
// imported resources, drops it next to a minimal providers.tf in a temp
// dir, and runs `terraform init -backend=false` + `terraform validate`.
//
// `terraform validate` is a pure provider-schema check — NO AWS
// credentials and no network beyond the one-time provider download
// (TF_PLUGIN_CACHE_DIR keeps that warm across runs). It catches
// schema-level errors the hclsyntax-parse check in imported_emit_test.go
// cannot — notably the #669 "Value for unconfigurable attribute" leak of
// computed-only attributes (e.g. aws_s3_bucket.arn) into a resource body.

package composer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// tfValidateProvidersHCL renders the providers.tf the harness drops next
// to the emitted imported.tf. It declares the aws.imported alias the
// emitted resource blocks reference; the skip_*/static creds keep
// `terraform validate` from ever touching AWS — validate is schema-only
// anyway, but the explicit creds avoid an interactive prompt under
// -input=false.
//
// #796: the AWS version constraint is NOT hand-pinned here. It is sourced
// from imported.BaseProviderPin("aws", "aws") — the SAME single source of
// truth the real reverse-import emitter feeds into its providers.tf
// (pkg/reverseimport/providers.go). Hand-pinning it (the pre-#796 `~> 5.0`)
// let this harness validate the imported.tf codegen against a different AWS
// provider major than production emits (`= 6.46.0`), because the shared
// TF_PLUGIN_CACHE_DIR resolved each constraint to a different cached major.
// A v6-only schema change to an imported resource body would therefore pass
// the v5-pinned harness. Deriving the pin from BaseProviderPin closes that
// schema-skew by construction: the harness can never validate against a
// different provider version than the emitter ships.
func tfValidateProvidersHCL(t *testing.T) string {
	t.Helper()
	pin := imported.BaseProviderPin("aws", "aws")
	if strings.TrimSpace(pin) == "" {
		t.Fatal(`imported.BaseProviderPin("aws", "aws") returned empty; ` +
			"the harness has no canonical AWS provider pin to validate against")
	}
	return fmt.Sprintf(`terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = %q
    }
  }
}

provider "aws" {
  alias                       = "imported"
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}
`, pin)
}

// TestImportedTF_ProvidersFixtureMatchesEmitterPin is the #796 schema-skew
// guard. It pins that the harness providers.tf validates the imported.tf
// codegen against the SAME AWS provider version the real reverse-import
// emitter ships — the canonical imported.BaseProviderPin("aws", "aws"),
// also consumed by pkg/reverseimport/providers.go — and never against a
// stale hand-pinned major. Pre-#796 the fixture hand-pinned `~> 5.0` while
// production emitted `= 6.46.0`; this test fails on that skew. Unlike
// TestImportedTF_TerraformValidate it needs no `terraform` binary, so it
// runs everywhere the tfvalidate tag is built.
func TestImportedTF_ProvidersFixtureMatchesEmitterPin(t *testing.T) {
	pin := imported.BaseProviderPin("aws", "aws")
	if strings.TrimSpace(pin) == "" {
		t.Fatal(`imported.BaseProviderPin("aws", "aws") returned empty`)
	}

	// Anchor 1 — the canonical pin the emitter ships must target the AWS v6
	// major, independent of the fixture. AWS preset modules require provider
	// >= 6.0 (CLAUDE.md), so a harness that validates the imported.tf codegen
	// against any other major reintroduces the #796 skew at the source. This
	// fails if BaseProviderPin itself ever drifts off v6 — it is NOT a
	// tautology against the fixture string.
	if !strings.HasPrefix(strings.TrimLeft(pin, "=~> "), "6.") {
		t.Fatalf("emitter AWS pin %q is not a v6 constraint; the imported.tf "+
			"tfvalidate harness must validate against the v6 schema AWS presets "+
			"require (#796)", pin)
	}

	// Anchor 2 — the rendered fixture must carry exactly that canonical pin,
	// so the providers.tf this harness hands `terraform validate` is wired to
	// the emitter source of truth and not a stale hand-pinned literal.
	hcl := tfValidateProvidersHCL(t)
	wantLine := fmt.Sprintf(`version = %q`, pin)
	if !strings.Contains(hcl, wantLine) {
		t.Errorf("harness providers.tf does not pin the canonical emitter AWS "+
			"version (#796 schema-skew):\n  want line: %s\n--- providers.tf ---\n%s",
			wantLine, hcl)
	}

	// Anchor 3 — tripwire for the retired v5 literal sneaking back into the
	// static template body (the alias/creds block anchors 1-2 don't read,
	// since they only inspect the interpolated pin) — the exact pre-#796 bug.
	if strings.Contains(hcl, "~> 5") || strings.Contains(hcl, `"5.`) {
		t.Errorf("harness providers.tf reintroduced a v5 AWS pin (#796 regression):\n%s", hcl)
	}
}

func TestImportedTF_TerraformValidate(t *testing.T) {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform binary not on PATH")
	}

	// Representative imported resources. The aws_s3_bucket carries a
	// synthesized `arn` in its typed Attrs — exactly the #669 leak: `arn`
	// is schema-Computed-only and must NOT reach the resource body.
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.io_uploads",
				ImportID: "io-uploads",
			},
			Tier: imported.TierImportedFlat,
			Attrs: []byte(`{` +
				`"bucket":{"literal":"io-uploads"},` +
				`"arn":{"literal":"arn:aws:s3:::io-uploads"}}`),
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.orders",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders",
			},
			Tier:  imported.TierImportedFlat,
			Attrs: []byte(`{"name":{"literal":"orders"}}`),
		},
	}

	out, _ := EmitImportedTF("aws", irs, EmitImportedOpts{})
	if len(out) == 0 {
		t.Fatal("EmitImportedTF returned no output")
	}

	dir := t.TempDir()
	if werr := os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(tfValidateProvidersHCL(t)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "imported.tf"), out, 0o644); werr != nil {
		t.Fatal(werr)
	}

	// Persistent plugin cache so the aws provider download is paid once.
	cache := filepath.Join(os.TempDir(), "tf-plugin-cache")
	_ = os.MkdirAll(cache, 0o755)
	env := append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+cache, "TF_IN_AUTOMATION=1")

	run := func(args ...string) (string, error) {
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		b, cerr := cmd.CombinedOutput()
		return string(b), cerr
	}

	if initOut, ierr := run("init", "-backend=false", "-input=false", "-no-color"); ierr != nil {
		t.Fatalf("terraform init failed: %v\n%s", ierr, initOut)
	}

	validateOut, verr := run("validate", "-no-color")
	t.Logf("terraform validate output:\n%s", validateOut)
	if verr != nil {
		t.Errorf("terraform validate failed (exit error: %v)", verr)
	}
	// The #669 regression assertion: a computed-only attribute leaking
	// into a resource block fails validate with this exact phrase.
	if strings.Contains(validateOut, "unconfigurable attribute") {
		t.Errorf("#669 regression — computed-only attribute leaked into a resource block:\n%s\n--- generated imported.tf ---\n%s", validateOut, out)
	}
}
