package composer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmitRootMainTF_MovedBlocks_TerraformValidate is a one-shot integration
// test that writes an emitted main.tf plus a minimal stub module to a tmpdir
// and runs `terraform init -backend=false && terraform validate` against it.
// This pins the contract that the emitted `moved {}` blocks are syntactically
// valid and accepted by terraform's validator — regex assertions elsewhere
// in this file can't catch subtle HCL-level errors.
//
// Skipped when -short is set or the `terraform` binary is not on PATH so the
// core test suite stays hermetic and fast.
func TestEmitRootMainTF_MovedBlocks_TerraformValidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform binary not on PATH: %v", err)
	}

	dir := t.TempDir()

	// Stub module that the root main.tf's module block will point at.
	// Empty body is a valid Terraform module; `terraform init` accepts it.
	stubDir := filepath.Join(dir, "modules", "vpc")
	require.NoError(t, os.MkdirAll(stubDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stubDir, "main.tf"), []byte("# empty stub module\n"), 0o644))

	// Root main.tf with one prefixed module + the auto-emitted moved block.
	blocks := []ModuleBlock{{Name: "aws_vpc", Source: "./modules/vpc"}}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), EmitRootMainTF(blocks), 0o644))

	// init with -backend=false avoids remote backend setup; no providers are
	// referenced, so no network round-trip happens.
	initCmd := exec.Command(tfBin, "init", "-backend=false", "-input=false", "-no-color")
	initCmd.Dir = dir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform init failed: %v\n%s", err, out)
	}

	validateCmd := exec.Command(tfBin, "validate", "-no-color")
	validateCmd.Dir = dir
	if out, err := validateCmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform validate failed on emitted main.tf — moved {} block malformed?\n%s", out)
	}
}
