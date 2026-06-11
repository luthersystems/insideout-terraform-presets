//go:build tfvalidate

// Build-tagged Terraform-validate harness for the reverseimport provider
// emitter (renderImportedProvidersTF).
//
// Normal `go test` skips this file (it shells out to the `terraform` binary
// and does a one-time provider download). Run it explicitly:
//
//	go test -tags tfvalidate -run TestRenderImportedProvidersTF_TerraformValidate -v ./pkg/reverseimport/
//
// It renders providers-imported.tf via the REAL renderImportedProvidersTF
// path for a multi-region AWS batch (base `aws.imported` + one
// `aws.imported_<region>` per region), drops it next to a resource that
// references one of the per-region aliases, and runs
// `terraform init -backend=false` + `terraform validate` against the pinned
// hashicorp/aws ~> 6.0 provider.
//
// Why this exists (PR #780 deferred-P2 hardening): the retry-tuning attrs
// `retry_mode = "adaptive"` / `max_retries = 25` (luthersystems/ui-core#420)
// emitted onto every imported provider block were otherwise only asserted by
// string/regex matching in providers_test.go — nothing ran the live provider
// schema over the real emitter output, so a future provider major that
// rejected those attrs would ship green. `terraform validate` is a pure
// provider-schema check — NO AWS credentials and no network beyond the
// one-time provider download (TF_PLUGIN_CACHE_DIR keeps that warm).

package reverseimport

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

func TestRenderImportedProvidersTF_TerraformValidate(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH")
	}

	// Representative multi-region AWS batch with assume-role plumbing — the
	// production shape EmitImportedTF references via aws.imported_<region>.
	body, err := renderImportedProvidersTF(importedProviderRenderOptions{
		Cloud:      "aws",
		Region:     "us-east-1",
		AWSRegions: []string{"us-east-1", "us-west-2"},
		ProvidersUsed: map[string]bool{
			composer.ProvidersUsedKeyAWS: true,
		},
		AWSAuth: AWSProviderAuth{
			RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
			ExternalID: "external-123",
		},
	})
	if err != nil {
		t.Fatalf("renderImportedProvidersTF: %v", err)
	}
	// Guard against a regression dropping the tuning before terraform runs.
	// Whitespace-tolerant (hclwrite re-aligns the `=` column).
	for _, pat := range []string{`retry_mode\s*=\s*"adaptive"`, `max_retries\s*=\s*25`} {
		if !regexp.MustCompile(pat).MatchString(string(body)) {
			t.Fatalf("emitted providers-imported.tf is missing retry tuning %q — the validate below would not be covering it:\n%s", pat, body)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "providers-imported.tf"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// A resource per aliased provider block so validate resolves each
	// `aws.imported*` config against the live schema (an aliased provider
	// with no consumer is not type-checked by validate).
	resourceHCL := `resource "aws_sqs_queue" "base" {
  provider = aws.imported
  name     = "base"
}

resource "aws_sqs_queue" "east" {
  provider = aws.imported_us_east_1
  name     = "east"
}

resource "aws_sqs_queue" "west" {
  provider = aws.imported_us_west_2
  name     = "west"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(resourceHCL), 0o644); err != nil {
		t.Fatal(err)
	}

	// Persistent plugin cache so the aws provider download is paid once.
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
		// A sandbox without registry access can't fetch the provider; skip
		// rather than fail to keep unprivileged environments green (CI's
		// terraform lane has access). Mirrors golden_stack_test.go.
		t.Skipf("terraform init could not fetch the provider (%v):\n%s", ierr, initOut)
	}

	validateOut, verr := run("validate", "-no-color")
	t.Logf("terraform validate output:\n%s", validateOut)
	if verr != nil {
		t.Errorf("terraform validate failed on emitted providers-imported.tf (exit error: %v)\n--- providers-imported.tf ---\n%s", verr, body)
	}
	// The PR #780 deferred-P2 assertion: a future provider that rejected a
	// retry-tuning attr surfaces here as an "Unsupported argument"
	// diagnostic rather than shipping green.
	if strings.Contains(validateOut, "Unsupported argument") {
		t.Errorf("provider rejected an emitted argument (retry tuning regression?):\n%s\n--- providers-imported.tf ---\n%s", validateOut, body)
	}
}
