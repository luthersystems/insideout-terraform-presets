//go:build tfvalidate

// Build-tagged Terraform-validate harness proving the composer path no longer
// ships the mutually-exclusive provider attributes that faithful
// resource-adoption HCL carries (issue #708, composer path).
//
// Normal `go test` skips this file (it shells out to the `terraform` binary
// and does a one-time provider download). Run it explicitly:
//
//	go test -tags tfvalidate -run TestImportedConflicts_ComposePathTerraformValidate -v ./pkg/composer/
//
// It exercises BOTH sides of the fix against the real AWS provider schema
// (pinned to imported.BaseProviderPin("aws","aws"), = 6.46.0):
//
//   - the RAW EmitImportedTF output (shared by both compose paths, no
//     normalization) still fails `terraform validate` with
//     "Conflicting configuration arguments" — the bug reproduces at the pinned
//     provider, so it is NOT provider-version-specific; and
//   - the composer's emitted /imported.tf — now normalized in
//     composeStackImpl — carries NEITHER "Conflicting configuration arguments"
//     nor "Invalid combination of arguments".
//
// `terraform validate` is a pure provider-schema check — no AWS credentials
// and no network beyond the one-time provider download (TF_PLUGIN_CACHE_DIR
// keeps it warm). The reduced fixture is a complete, self-contained closure
// (every referenced subnet/VPC is declared), so the normalized composer
// /imported.tf validates clean with no residual `Error:` diagnostics.

package composer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// validateImportedTF drops hcl next to a minimal providers.tf declaring the
// aws.imported alias and returns the combined `terraform validate` output.
func validateImportedTF(t *testing.T, tfBin string, hcl []byte) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(tfValidateProvidersHCL(t)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "imported.tf"), hcl, 0o644))

	cache := filepath.Join(os.TempDir(), "tf-plugin-cache")
	_ = os.MkdirAll(cache, 0o755)
	env := append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+cache, "TF_IN_AUTOMATION=1")
	run := func(args ...string) (string, error) {
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	if out, err := run("init", "-backend=false", "-input=false", "-no-color"); err != nil {
		t.Fatalf("terraform init failed: %v\n%s", err, out)
	}
	out, _ := run("validate", "-no-color") // non-nil err expected (undeclared subnet refs)
	return out
}

const (
	conflictErr     = "Conflicting configuration arguments"
	invalidComboErr = "Invalid combination of arguments"
)

func TestImportedConflicts_ComposePathTerraformValidate(t *testing.T) {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform binary not on PATH")
	}

	irs := loadConflictFixture(t)

	// --- before: raw emit (un-normalized), shared by both compose paths ---
	// The fixture is a self-contained dependency closure (ENI -> subnet -> vpc,
	// plus the ALB and an S3 bucket), so the ENI attribute conflict is not
	// masked by an unresolved subnet reference — both conflict classes surface.
	raw, _ := EmitImportedTF("aws", irs, EmitImportedOpts{})
	require.NotEmpty(t, raw)
	rawOut := validateImportedTF(t, tfBin, raw)
	t.Logf("=== validate(raw EmitImportedTF) ===\n%s", rawOut)
	require.Containsf(t, rawOut, conflictErr,
		"raw EmitImportedTF must reproduce the aws_network_interface "+
			"private_ip_list/private_ips conflict at the pinned provider; if it does "+
			"not, the fixture or emitter changed and the after-assertion is vacuous\n%s", rawOut)
	require.Containsf(t, rawOut, invalidComboErr,
		"raw EmitImportedTF must reproduce the aws_lb subnets/subnet_mapping conflict "+
			"at the pinned provider\n%s", rawOut)

	// --- after: composer path emitted /imported.tf (normalized) ---
	c := newTestClient()
	res, cerr := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "io-f-v6e-hzw-zt",
		Region:       "us-east-1",
		Imported:     irs,
	})
	require.NoError(t, cerr)
	composed := res.Files["/imported.tf"]
	require.NotEmpty(t, composed)

	composedOut := validateImportedTF(t, tfBin, composed)
	t.Logf("=== validate(composer /imported.tf) ===\n%s", composedOut)
	if strings.Contains(composedOut, conflictErr) {
		t.Errorf("composer /imported.tf still emits %q — the composer path did not normalize the imported.tf (#708):\n%s\n--- imported.tf ---\n%s",
			conflictErr, composedOut, composed)
	}
	if strings.Contains(composedOut, invalidComboErr) {
		t.Errorf("composer /imported.tf still emits %q — the composer path did not normalize the imported.tf (#708):\n%s\n--- imported.tf ---\n%s",
			invalidComboErr, composedOut, composed)
	}
	// The complete closure validates clean once normalized (the provider is
	// pinned at imported.BaseProviderPin, so this is not version-fragile): the
	// only remaining diagnostics are S3 inline-block deprecation WARNINGS, which
	// `terraform validate` reports as success. Any surviving `Error:` means the
	// normalized composer artifact is still not deployable.
	if strings.Contains(composedOut, "Error:") {
		t.Errorf("composer /imported.tf still fails `terraform validate` after normalization:\n%s\n--- imported.tf ---\n%s",
			composedOut, composed)
	}
}
