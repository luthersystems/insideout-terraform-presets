//go:build tfvalidate

// Build-tagged Terraform-validate harness for the genconfig provider emitter.
//
// Normal `go test` skips this file (it shells out to the `terraform` binary
// and does a one-time provider download). Run it explicitly:
//
//	go test -tags tfvalidate -run TestEmitProviders_TerraformValidate -v ./cmd/insideout-import/genconfig/
//
// It emits providers.tf via the REAL emitProviders / configureAWSProviderBody
// path (NOT a hand-written fixture), drops it in a temp dir, and runs
// `terraform init -backend=false` + `terraform validate` against the pinned
// hashicorp/aws ~> 6.0 provider.
//
// Why this exists (PR #780 deferred-P2 hardening): the retry-tuning attrs
// `retry_mode = "adaptive"` / `max_retries = 25` (luthersystems/ui-core#420)
// are otherwise only asserted by string/regex matching in emit_test.go, and
// the RUN_GOLDEN_HCL golden gate validates a hand-edited static providers.tf
// fixture rather than re-emitting via configureAWSProviderBody. A future
// provider major that rejected those attrs would ship green. This harness
// runs the live provider schema over the real emitter output so a rejected
// retry attribute fails the gate. `terraform validate` is a pure
// provider-schema check — NO AWS credentials and no network beyond the
// one-time provider download (TF_PLUGIN_CACHE_DIR keeps that warm).

package genconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tfValidateGenconfigCases exercises the provider-block shapes
// configureAWSProviderBody can emit, each one carrying the retry tuning.
// A resource referencing the (default, unaliased) provider is dropped
// alongside so validate exercises the provider config, not just parses it.
func TestEmitProviders_TerraformValidate(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform binary not on PATH")
	}

	cases := []struct {
		name string
		opts providerEmitOptions
		// resourceHCL is dropped next to providers.tf so validate resolves
		// the default provider config against the live schema.
		resourceHCL string
	}{
		{
			name: "aws_plain",
			opts: providerEmitOptions{Provider: ProviderAWS, Region: "us-west-2"},
			resourceHCL: `resource "aws_sqs_queue" "orders" {
  name = "orders"
}
`,
		},
		{
			name: "aws_assume_role",
			opts: providerEmitOptions{
				Provider: ProviderAWS,
				Region:   "us-east-1",
				AWSAuth: awsProviderAuth{
					RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
					ExternalID: "external-123",
				},
			},
			resourceHCL: `resource "aws_sqs_queue" "orders" {
  name = "orders"
}
`,
		},
		{
			name: "aws_localstack",
			opts: providerEmitOptions{
				Provider:       ProviderAWS,
				Region:         "us-east-1",
				AWSEndpointURL: "http://localhost:4566",
			},
			resourceHCL: `resource "aws_sqs_queue" "orders" {
  name = "orders"
}
`,
		},
	}

	// Persistent plugin cache so the aws provider download is paid once
	// across cases and across the imported.tf harness. Mirrors
	// pkg/composer/imported_tfvalidate_test.go.
	cache := filepath.Join(os.TempDir(), "tf-plugin-cache")
	_ = os.MkdirAll(cache, 0o755)
	env := append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+cache, "TF_IN_AUTOMATION=1")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := emitProviders(dir, tc.opts); err != nil {
				t.Fatalf("emitProviders: %v", err)
			}
			// The real retry tuning must be present in what we validate —
			// guard against a regression that drops it before this test even
			// reaches terraform. Whitespace-tolerant: hclwrite re-aligns the
			// `=` column when a block gains a wider attribute (e.g. the
			// LocalStack skip_* attrs), so anchor on the value, not the gutter.
			provBody, err := os.ReadFile(filepath.Join(dir, providersFile))
			if err != nil {
				t.Fatalf("read providers.tf: %v", err)
			}
			for _, pat := range []string{`retry_mode\s*=\s*"adaptive"`, `max_retries\s*=\s*25`} {
				if !regexp.MustCompile(pat).MatchString(string(provBody)) {
					t.Fatalf("emitted providers.tf is missing retry tuning %q — the validate below would not be covering it:\n%s", pat, provBody)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tc.resourceHCL), 0o644); err != nil {
				t.Fatal(err)
			}

			run := func(args ...string) (string, error) {
				cmd := exec.Command("terraform", args...)
				cmd.Dir = dir
				cmd.Env = env
				b, cerr := cmd.CombinedOutput()
				return string(b), cerr
			}

			if initOut, ierr := run("init", "-backend=false", "-input=false", "-no-color"); ierr != nil {
				// A sandbox without registry access can't fetch the provider;
				// skip rather than fail to keep unprivileged environments green
				// (CI's terraform lane has access). Mirrors golden_stack_test.go.
				t.Skipf("terraform init could not fetch the provider (%v):\n%s", ierr, initOut)
			}

			validateOut, verr := run("validate", "-no-color")
			t.Logf("terraform validate output:\n%s", validateOut)
			if verr != nil {
				t.Errorf("terraform validate failed on emitted providers.tf (exit error: %v)\n--- providers.tf ---\n%s", verr, provBody)
			}
			// The PR #780 deferred-P2 assertion: a future provider that
			// rejected a retry-tuning attr surfaces here as an
			// "Unsupported argument" diagnostic rather than shipping green.
			if strings.Contains(validateOut, "Unsupported argument") {
				t.Errorf("provider rejected an emitted argument (retry tuning regression?):\n%s\n--- providers.tf ---\n%s", validateOut, provBody)
			}
		})
	}
}
