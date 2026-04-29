package composer

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gkeMapper wraps DefaultMapper to set gke_cluster_name on KeyGCPVPC so the
// secondary_ranges block (which has its own for_each map-key surface in the
// upstream wrapper module) is exercised by the regression test below.
type gkeMapper struct {
	DefaultMapper
}

func (m gkeMapper) BuildModuleValues(
	k ComponentKey,
	comps *Components,
	cfg *Config,
	project, region string,
) (map[string]any, error) {
	vals, err := m.DefaultMapper.BuildModuleValues(k, comps, cfg, project, region)
	if err != nil {
		return nil, err
	}
	if k == KeyGCPVPC {
		vals["gke_cluster_name"] = "test-cluster"
	}
	return vals, nil
}

// TestComposeGCPVPC_PlanHasNoForEachUnknownKey is the load-bearing regression
// test for issue #163. It composes the gcp/vpc preset (with gke_cluster_name
// set so the secondary_ranges block is exercised), writes the bundle to a
// tempdir, runs `terraform init -backend=false` then `terraform plan
// -refresh=false`, and asserts the plan output does NOT contain "Invalid
// for_each argument".
//
// This catches the entire family of "apply-time-unknown value flowing into a
// for_each map key" bugs regardless of which symbol leaks the unknown value
// (random_id, timestamp, uuid, computed resource attribute, …) — the bug
// manifests during graph evaluation in `terraform plan`, before any provider
// API call. Static lints can only flag explicit cases (see
// tests/lint-foreach-unknown-keys.sh); this test is the verifier.
//
// Skipped when -short is set or the `terraform` binary is not on PATH so the
// core test suite stays hermetic and fast.
//
// The plan step does not need real GCP credentials: the for_each error
// surfaces during graph evaluation, which precedes provider authentication.
// We tolerate non-regression failures (missing creds, registry hiccups) by
// only failing the test when the regression-specific error string appears.
func TestComposeGCPVPC_PlanHasNoForEachUnknownKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform binary not on PATH: %v", err)
	}

	c := New(WithMapper(gkeMapper{}))
	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:        "gcp",
		Key:          KeyGCPVPC,
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "regress163",
		Region:       "us-central1",
		GCPProjectID: "regress163-12345",
	})
	require.NoError(t, err, "ComposeSingle(gcp_vpc) should succeed")
	require.NotEmpty(t, out, "composed bundle should not be empty")

	dir := t.TempDir()
	writeBundle(t, dir, out)

	initCmd := exec.Command(tfBin, "init", "-backend=false", "-input=false", "-no-color")
	initCmd.Dir = dir
	if initOut, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform init failed:\n%s", initOut)
	}

	// Plan with refresh disabled and a fake credential. We don't need real
	// auth: the for_each evaluation happens before any provider API call.
	planCmd := exec.Command(tfBin, "plan", "-refresh=false", "-input=false", "-lock=false", "-no-color")
	planCmd.Dir = dir
	planCmd.Env = append(os.Environ(), "GOOGLE_OAUTH_ACCESS_TOKEN=fake-regression-test-token")
	planOut, planErr := planCmd.CombinedOutput()
	planText := string(planOut)

	if strings.Contains(planText, "Invalid for_each argument") {
		t.Fatalf("REGRESSION (#163): apply-time-unknown value reached a for_each map key.\nterraform plan output:\n%s", planText)
	}

	// Plan may still fail for unrelated reasons (no real GCP creds, registry
	// glitch). Log non-regression failures but don't fail the test — the
	// regression-specific assertion above is what we care about.
	if planErr != nil {
		t.Logf("terraform plan failed for non-regression reason (acceptable):\n%s", planText)
	}
}
