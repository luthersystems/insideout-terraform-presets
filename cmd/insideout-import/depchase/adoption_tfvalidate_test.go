//go:build tfvalidate

// Build-tagged Terraform-validate proof for the closure contract's
// reference-representation fallback (presets#864).
//
// Normal `go test` skips this file — it shells out to the `terraform` binary and
// does a one-time provider download. Run it explicitly:
//
//	go test -tags tfvalidate -run TestReferenceRepresentation_Validates -v ./cmd/insideout-import/depchase/
//
// The closure contract's core safety claim is: when dep-chase declines to adopt
// an out-of-scope dependency, it leaves the reference as a bare literal in the
// consumer's HCL, and the generated config STILL passes `terraform validate`
// with no import block and no managed resource for the target. This test proves
// that claim against the pinned hashicorp/aws ~> 6.0 provider: an aws_sqs_queue
// referencing a foreign KMS key purely by literal ARN (the reference
// representation) validates, whereas an equivalent stack that also declared and
// imported the key would be the adoption the contract avoids. `terraform
// validate` is a pure provider-schema check — no AWS credentials, no network
// beyond the one-time provider download.

package depchase

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceRepresentation_Validates(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH")
	}

	// A concrete external identifier left as a bare literal — exactly what the
	// closure contract retains when it declines to adopt an out-of-scope
	// dependency. There is NO aws_kms_key resource and NO import block for it.
	const foreignKMSARN = "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	main := `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
}

# Reference representation: the queue points at a foreign KMS key by literal ARN.
# The key itself is NOT adopted (no resource block, no import block) — the
# closure contract's bound. This must still validate.
resource "aws_sqs_queue" "orders" {
  name              = "io-orders"
  kms_master_key_id = "` + foreignKMSARN + `"
}
`

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := filepath.Join(os.TempDir(), "tf-plugin-cache")
	_ = os.MkdirAll(cache, 0o755)
	env := append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+cache, "TF_IN_AUTOMATION=1")
	run := func(args ...string) (string, error) {
		cmd := exec.Command("terraform", args...)
		cmd.Dir = dir
		cmd.Env = env
		b, cerr := cmd.CombinedOutput()
		return string(b), cerr
	}

	if initOut, ierr := run("init", "-backend=false", "-input=false", "-no-color"); ierr != nil {
		// An unprivileged sandbox may not reach the registry; skip rather than
		// fail (CI's terraform lane has access). Mirrors the reverseimport
		// tfvalidate harness.
		t.Skipf("terraform init could not fetch the provider (%v):\n%s", ierr, initOut)
	}

	validateOut, verr := run("validate", "-no-color")
	t.Logf("terraform validate output:\n%s", validateOut)
	if verr != nil {
		t.Fatalf("terraform validate FAILED on the reference-representation stack (exit %v) — a retained literal reference must remain valid without adopting the target:\n%s", verr, validateOut)
	}
	if strings.Contains(validateOut, "Error:") {
		t.Fatalf("terraform validate surfaced an error on the reference-representation stack:\n%s", validateOut)
	}
}
