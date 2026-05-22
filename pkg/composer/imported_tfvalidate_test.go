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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// tfValidateProvidersHCL declares the aws.imported alias the emitted
// resource blocks reference. The skip_*/static creds keep `terraform
// validate` from ever touching AWS — validate is schema-only anyway, but
// the explicit creds avoid an interactive prompt under -input=false.
const tfValidateProvidersHCL = `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
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
`

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
	if werr := os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(tfValidateProvidersHCL), 0o644); werr != nil {
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
