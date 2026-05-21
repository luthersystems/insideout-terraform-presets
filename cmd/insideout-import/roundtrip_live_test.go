//go:build integration

// roundtrip_live_test.go is the local live-AWS round-trip harness for the
// import flow (issue #652). It exercises the whole cycle that previously
// could only be tested on prod (Reliable + Argo): create real AWS
// resources, discover them, re-emit imported.tf via the composer, and
// assert `terraform plan` on that imported.tf is clean — then tear the
// fixture down.
//
// It is gated three ways so it never runs in normal `go test ./...` or
// CI: the `integration` build tag, the RUN_LIVE_ROUNDTRIP=1 env guard,
// and a credential probe that skips cleanly when no AWS creds are
// loaded. Run it with real creds (e.g. after `aws_jump <acct> <role>`):
//
//	make test-roundtrip
//
// or directly:
//
//	RUN_LIVE_ROUNDTRIP=1 go test -tags=integration -run TestLiveRoundTrip \
//	    ./cmd/insideout-import/... -v -timeout 20m

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// roundtripRegion is the region the fixture and the plan workspace use.
const roundtripRegion = "us-east-1"

// roundtripTypes is the resource-type subset the discover run scans —
// exactly the four #652-implicated types plus the IAM role the Lambda
// depends on. Narrowing the scan keeps the run fast.
const roundtripTypes = "aws_s3_bucket,aws_cloudwatch_log_group,aws_lambda_function,aws_iam_role,aws_iam_policy"

// TestLiveRoundTrip applies the roundtrip fixture, discovers it, re-emits
// imported.tf through the composer, and asserts `terraform plan` on the
// emitted file completes without error — the #652 failure mode. It does
// not apply the import; a clean plan is sufficient proof.
func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("RUN_LIVE_ROUNDTRIP") == "" {
		t.Skip("live round-trip test: set RUN_LIVE_ROUNDTRIP=1 to run (creates real AWS resources)")
	}
	requireTerraform(t)
	requireAWSCredentials(t)

	project := "iort" + randomSuffix(t)
	t.Logf("round-trip project prefix: %s", project)

	// --- Stage 1: apply the fixture (real AWS resources) ---------------
	fixtureWS := t.TempDir()
	copyFile(t, filepath.Join("testdata", "roundtrip-fixture", "main.tf"),
		filepath.Join(fixtureWS, "main.tf"))

	tfVars := []string{"-var", "project=" + project, "-var", "region=" + roundtripRegion}
	runTF(t, fixtureWS, "fixture init", "init", "-input=false", "-no-color")
	// Destroy is registered before apply so a partial apply still cleans up.
	t.Cleanup(func() {
		out, err := tryTF(fixtureWS, append([]string{"destroy", "-auto-approve", "-input=false", "-no-color"}, tfVars...)...)
		if err != nil {
			t.Errorf("fixture destroy failed — manual cleanup of project %q may be needed:\n%s", project, out)
		}
	})
	runTF(t, fixtureWS, "fixture apply",
		append([]string{"apply", "-auto-approve", "-input=false", "-no-color"}, tfVars...)...)

	// --- Stage 2: discover the fixture resources -----------------------
	outDir := t.TempDir()
	t.Logf("running insideout-import discover --project %s", project)
	if code := runDiscover([]string{
		"--provider", "aws",
		"--project", project,
		"--regions", roundtripRegion,
		"--resource-types", roundtripTypes,
		"--output-dir", outDir,
	}); code != 0 {
		t.Fatalf("discover exited %d, want 0", code)
	}

	irs, err := readManifest(filepath.Join(outDir, "imported.json"), "aws")
	if err != nil {
		t.Fatalf("read discovered manifest: %v", err)
	}
	if len(irs) == 0 {
		t.Fatal("discover found zero resources; the fixture apply or the project-tag filter is broken")
	}
	t.Logf("discovered %d resource(s)", len(irs))

	// No AWS-managed IAM policy may enter the discovered set (#652) — the
	// discover project-tag scope plus the SkipIdentifier filter guarantee
	// this; pin it so a regression on either is caught here.
	for _, ir := range irs {
		if strings.Contains(ir.Identity.ImportID, ":iam::aws:policy/") {
			t.Errorf("discovered an AWS-managed IAM policy %q; it must never enter customer state", ir.Identity.ImportID)
		}
	}

	// --- Stage 3: re-emit imported.tf via the composer -----------------
	importedTF, used := composer.EmitImportedTF("aws", irs, composer.EmitImportedOpts{
		ImportProjectID: project,
		ImportSessionID: "roundtrip-live-test",
		ImportedAt:      time.Now().UTC(),
	})
	if len(importedTF) == 0 {
		t.Fatal("EmitImportedTF produced no output for a non-empty discovered set")
	}
	if !used["aws"] {
		t.Fatal("EmitImportedTF did not flag the aws provider as used")
	}

	// --- Stage 4: terraform plan the emitted imported.tf ---------------
	planWS := t.TempDir()
	writeFile(t, filepath.Join(planWS, "imported.tf"), importedTF)
	writeFile(t, filepath.Join(planWS, "providers.tf"), []byte(roundtripProvidersTF))

	runTF(t, planWS, "imported.tf init", "init", "-input=false", "-no-color")
	planOut, planErr := tryTF(planWS, "plan", "-input=false", "-no-color")
	t.Logf("terraform plan output:\n%s", planOut)
	if planErr != nil {
		t.Fatalf("terraform plan on the composed imported.tf FAILED — this is the #652 regression:\n%s", planOut)
	}
	// Defense in depth: a zero exit with `Error:` diagnostics in the body
	// would still be the #652 failure mode.
	if strings.Contains(planOut, "\nError:") || strings.HasPrefix(planOut, "Error:") {
		t.Fatalf("terraform plan emitted error diagnostics on the composed imported.tf (#652):\n%s", planOut)
	}

	// --- Stage 5: terraform apply — the real import --------------------
	// Applying the import {} blocks adopts the four live resources into
	// the plan workspace's (ephemeral) state. Once that succeeds the
	// plan workspace owns them, so its destroy — registered here, and
	// therefore run BEFORE the fixture destroy (t.Cleanup is LIFO) — is
	// the real teardown; the fixture destroy then refreshes, finds the
	// resources already gone, and no-ops.
	t.Cleanup(func() {
		out, err := tryTF(planWS, "destroy", "-auto-approve", "-input=false", "-no-color")
		if err != nil {
			t.Errorf("plan-workspace destroy failed — manual cleanup of project %q may be needed:\n%s", project, out)
		}
	})
	applyOut, applyErr := tryTF(planWS, "apply", "-auto-approve", "-input=false", "-no-color")
	t.Logf("terraform apply (import) output:\n%s", applyOut)
	if applyErr != nil {
		t.Fatalf("terraform apply on the composed imported.tf FAILED — the import does not work end to end:\n%s", applyOut)
	}

	// --- Stage 6: verify every resource landed in Terraform state ------
	stateOut, err := tryTF(planWS, "state", "list")
	if err != nil {
		t.Fatalf("terraform state list failed after import: %v\n%s", err, stateOut)
	}
	for _, want := range []string{
		"aws_lambda_function.",
		"aws_s3_bucket.",
		"aws_cloudwatch_log_group.",
		"aws_iam_policy.",
	} {
		if !strings.Contains(stateOut, want) {
			t.Errorf("imported Terraform state is missing a %s resource:\n%s", want, stateOut)
		}
	}
	t.Logf("round-trip OK: %d resource(s) discovered, composed, and imported into Terraform state for project %s",
		len(irs), project)
}

// roundtripProvidersTF is the providers.tf for the plan workspace. The
// imported resources all carry `provider = aws.imported`, so the aliased
// provider must be declared; the default provider satisfies init.
const roundtripProvidersTF = `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = "` + roundtripRegion + `"
}

provider "aws" {
  alias  = "imported"
  region = "` + roundtripRegion + `"
}
`

// requireTerraform skips the test when the terraform binary is absent.
func requireTerraform(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("live round-trip test: terraform not found on PATH")
	}
}

// requireAWSCredentials skips the test when no usable AWS credentials are
// loaded, rather than failing — the harness is opt-in and an unprivileged
// environment should not turn red.
func requireAWSCredentials(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(roundtripRegion))
	if err != nil {
		t.Skipf("live round-trip test: cannot load AWS config: %v", err)
	}
	if _, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		t.Skipf("live round-trip test: no usable AWS credentials (run aws_jump first): %v", err)
	}
}

// randomSuffix returns 8 lowercase hex chars for unique resource naming.
func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// runTF runs terraform in dir and fails the test on a non-zero exit.
func runTF(t *testing.T, dir, label string, args ...string) {
	t.Helper()
	out, err := tryTF(dir, args...)
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", label, err, out)
	}
}

// tryTF runs terraform in dir and returns the combined output and error
// without failing the test — callers decide how to react.
func tryTF(dir string, args ...string) (string, error) {
	cmd := exec.Command("terraform", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	writeFile(t, dst, data)
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
