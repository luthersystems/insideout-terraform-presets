package composer

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

var (
	printGenerated bool
	writeOutDir    string
)

func TestMain(m *testing.M) {
	flag.BoolVar(&printGenerated, "print", false, "print generated files to stdout")
	flag.StringVar(&writeOutDir, "outdir", "", "write generated files to this directory (will be created)")
	flag.Parse()
	os.Exit(m.Run())
}

func parseHCL(name string, b []byte) error {
	_, diags := hclwrite.ParseConfig(b, name, hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("%s: %s", name, diags.Error())
	}
	return nil
}

func writeBundle(t *testing.T, base string, files Files) {
	t.Helper()
	require.NotEmpty(t, base, "writeBundle base dir is empty")
	err := os.MkdirAll(base, 0o750)
	require.NoError(t, err, "mkdir outdir")

	for p, b := range files {
		full := filepath.Join(base, strings.TrimPrefix(p, "/"))
		err := os.MkdirAll(filepath.Dir(full), 0o750)
		require.NoError(t, err, "mkdir for %s", full)
		err = os.WriteFile(full, b, 0o600)
		require.NoError(t, err, "write file %s", full)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestComposeSingle_VM_WithTestify(t *testing.T) {
	// Ensure eks_nodegroup preset exists in the embedded FS (KeyAWSEKSNodeGroup maps to aws/eks_nodegroup)
	presetFiles, err := newTestClient().GetPresetFiles("aws/eks_nodegroup")
	require.NoError(t, err, "GetPresetFiles(aws/eks_nodegroup)")
	require.NotEmpty(t, presetFiles, "aws/eks_nodegroup preset should not be empty")
	_, hasVars := presetFiles["/variables.tf"]
	_, hasMain := presetFiles["/main.tf"]
	require.True(t, hasVars && hasMain, "aws/eks_nodegroup preset should include variables.tf and main.tf")

	// Compose
	c := newTestClient(WithTerraformVersion("1.7.5"))
	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:   "aws",
		Key:     KeyAWSEKSNodeGroup,
		Comps:   &Components{},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err, "ComposeSingle(vm)")
	require.NotEmpty(t, out, "composed bundle should not be empty")

	// Validate required root files
	mainTF, okMain := out["/main.tf"]
	varsTF, okVars := out["/variables.tf"]
	tfVer, okVer := out["/.terraform-version"]
	require.True(t, okMain, "expected /main.tf in output; got keys: %v", keysOf(out))
	require.True(t, okVars, "expected /variables.tf in output; got keys: %v", keysOf(out))
	require.True(t, okVer, "expected /.terraform-version in output; got keys: %v", keysOf(out))
	require.NotEmpty(t, bytes.TrimSpace(tfVer), ".terraform-version must not be empty")

	// Rebased preset should exist under modules/eks_nodegroup (KeyAWSEKSNodeGroup is EKS managed node group)
	_, ok := out["/modules/eks_nodegroup/variables.tf"]
	assert.True(t, ok, "expected /modules/eks_nodegroup/variables.tf; got keys: %v", keysOf(out))
	_, ok = out["/modules/eks_nodegroup/main.tf"]
	assert.True(t, ok, "expected /modules/eks_nodegroup/main.tf; got keys: %v", keysOf(out))

	// Parse HCL for generated files
	require.NoError(t, parseHCL("main.tf", mainTF), "main.tf should parse")
	require.NoError(t, parseHCL("variables.tf", varsTF), "variables.tf should parse")

	// main.tf should contain module block and wire project/region to namespaced vars
	mainStr := string(mainTF)
	assert.Contains(t, mainStr, `module "ec2"`, `main.tf should contain module "ec2" block`)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*project\s*=\s*var\.ec2_project\s*$`), mainStr,
		"project should be wired as var.ec2_project")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*region\s*=\s*var\.ec2_region\s*$`), mainStr,
		"region should be wired as var.ec2_region")

	// variables.tf should declare namespaced vars
	varsStr := string(varsTF)
	assert.Contains(t, varsStr, `variable "ec2_project"`, `variables.tf should declare "ec2_project"`)
	assert.Contains(t, varsStr, `variable "ec2_region"`, `variables.tf should declare "ec2_region"`)

	// ec2.auto.tfvars should include mapper-provided values with namespaced keys (allow aligned spacing)
	tfvars, ok := out["/ec2.auto.tfvars"]
	require.True(t, ok, "expected /ec2.auto.tfvars in output")
	tfvarsStr := string(tfvars)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*ec2_project\s*=\s*"demo"\s*$`), tfvarsStr,
		"/ec2.auto.tfvars should contain ec2_project (namespaced)")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*ec2_region\s*=\s*"us-east-1"\s*$`), tfvarsStr,
		"/ec2.auto.tfvars should contain ec2_region (namespaced)")

	// Optional: print and/or write files for manual inspection
	if printGenerated {
		t.Log("---- Generated files ----")
		names := keysOf(out)
		for _, p := range names {
			t.Logf("== %s ==\n%s\n", p, string(out[p]))
		}
	}
	if writeOutDir != "" {
		writeBundle(t, writeOutDir, out)
		t.Logf("Wrote generated bundle to: %s", writeOutDir)
	}
}

func TestListClouds(t *testing.T) {
	clouds, err := newTestClient().ListClouds()
	require.NoError(t, err, "ListClouds should succeed")
	require.Contains(t, clouds, "aws", "should contain aws")
	require.Contains(t, clouds, "gcp", "should contain gcp")
}

func TestListPresetKeysForCloud(t *testing.T) {
	// Test AWS modules
	c := newTestClient()
	awsKeys, err := c.ListPresetKeysForCloud("aws")
	require.NoError(t, err, "ListPresetKeysForCloud(aws) should succeed")
	require.Contains(t, awsKeys, "aws/vpc", "AWS should have vpc module")
	require.Contains(t, awsKeys, "aws/ec2", "AWS should have ec2 module (standalone)")
	require.Contains(t, awsKeys, "aws/eks_nodegroup", "AWS should have eks_nodegroup module")
	require.Contains(t, awsKeys, "aws/rds", "AWS should have rds module")
	require.Contains(t, awsKeys, "aws/s3", "AWS should have s3 module")

	// Test GCP modules
	gcpKeys, err := c.ListPresetKeysForCloud("gcp")
	require.NoError(t, err, "ListPresetKeysForCloud(gcp) should succeed")
	require.Contains(t, gcpKeys, "gcp/vpc", "GCP should have vpc module")
	require.Contains(t, gcpKeys, "gcp/compute", "GCP should have compute module")
	require.Contains(t, gcpKeys, "gcp/cloudsql", "GCP should have cloudsql module")
	require.Contains(t, gcpKeys, "gcp/gcs", "GCP should have gcs module")
	require.Contains(t, gcpKeys, "gcp/gke", "GCP should have gke module")
	require.Contains(t, gcpKeys, "gcp/loadbalancer", "GCP should have loadbalancer module")
	require.Contains(t, gcpKeys, "gcp/kms", "GCP should have kms module")
	require.Contains(t, gcpKeys, "gcp/secretmanager", "GCP should have secretmanager module")
}

func TestGetPresetFiles_GCP_VPC(t *testing.T) {
	// Ensure GCP VPC preset exists in the embedded FS
	presetFiles, err := newTestClient().GetPresetFiles("gcp/vpc")
	require.NoError(t, err, "GetPresetFiles(gcp/vpc)")
	require.NotEmpty(t, presetFiles, "gcp/vpc preset should not be empty")
	_, hasVars := presetFiles["/variables.tf"]
	_, hasMain := presetFiles["/main.tf"]
	require.True(t, hasVars && hasMain, "gcp/vpc preset should include variables.tf and main.tf")

	// Parse HCL for generated files
	for name, content := range presetFiles {
		if strings.HasSuffix(name, ".tf") {
			require.NoError(t, parseHCL(name, content), "%s should parse as valid HCL", name)
		}
	}
}

func TestGetPresetFiles_GCP_AllModules(t *testing.T) {
	gcpModules := []string{
		"gcp/vpc",
		"gcp/compute",
		"gcp/gke",
		"gcp/cloudsql",
		"gcp/gcs",
		"gcp/loadbalancer",
		"gcp/kms",
		"gcp/secretmanager",
	}

	for _, mod := range gcpModules {
		t.Run(mod, func(t *testing.T) {
			files, err := newTestClient().GetPresetFiles(mod)
			require.NoError(t, err, "GetPresetFiles(%s)", mod)
			require.NotEmpty(t, files, "%s preset should not be empty", mod)

			// Validate required files exist
			_, hasVars := files["/variables.tf"]
			_, hasMain := files["/main.tf"]
			require.True(t, hasVars, "%s should have variables.tf", mod)
			require.True(t, hasMain, "%s should have main.tf", mod)

			// Parse all .tf files as valid HCL
			for name, content := range files {
				if strings.HasSuffix(name, ".tf") {
					require.NoError(t, parseHCL(name, content), "%s/%s should parse as valid HCL", mod, name)
				}
			}
		})
	}
}

// stripHCLComments returns the input HCL with `#`- and `//`-prefixed
// comment lines removed. Useful for structural pins that need to
// assert against active code only — the migration recipes in
// gcp/kms/main.tf's header comment legitimately reference the
// upstream's old module addresses, but those references are
// documentation, not active code. moved {} blocks describing the old
// upstream addresses are intentionally NOT stripped: they ARE active
// HCL (terraform reads them on plan) and the structural pin treats
// them as such — a moved.from address pointing at module.kms is
// expected and necessary.
func stripHCLComments(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// TestGetPresetFiles_GCP_KMS_NoUpstreamModule pins the issue #182
// upstream-replacement at the embedded-FS layer.
//
// History: the preset originally wrapped
// terraform-google-modules/kms/google ~> 3.0. That module's
// local.keys_by_name calls slice() on a count-controlled splat which
// errors during plan against an empty state (issue #180). PR #181
// surgically wrapped the consumption in try() to unblock the
// default-config customer in #178's repro, but iam_bindings still
// referenced the slice expression directly so non-default users hit a
// hole. Issue #182 replaced the upstream module entirely with direct
// google_kms_* resources keyed by for_each — eliminating the slice
// expression so the failure mode cannot recur and closing the
// iam_bindings hole by construction.
//
// This structural pin asserts (against comment-stripped HCL, since
// the header comment legitimately documents the migration recipe by
// referencing the old upstream addresses):
//   - The upstream module is gone (no module "kms" block, no
//     terraform-google-modules/kms source, no slice() call, no
//     try() wrap of the upstream).
//   - The replacement resources (key_ring + protected/ephemeral
//     for_each crypto_keys) are present.
//   - The default-config moved {} blocks (3 of them) are present so
//     existing customers' state migrates without replace plans. The
//     moved {} blocks intentionally reference module.kms.* on the
//     `from` side — those are active HCL describing prior state.
//   - outputs.tf reads from the direct resource, not from a module
//     reference.
//
// A revert that re-vendors the upstream now fails this test loud.
func TestGetPresetFiles_GCP_KMS_NoUpstreamModule(t *testing.T) {
	t.Parallel()
	files, err := newTestClient().GetPresetFiles("gcp/kms")
	require.NoError(t, err)

	mainTF, ok := files["/main.tf"]
	require.True(t, ok, "gcp/kms must include main.tf")

	body := string(mainTF)
	bodyNoComments := stripHCLComments(body)

	// The upstream module source is gone (only the active HCL is
	// checked — the migration recipe in the header comment
	// legitimately mentions the old source).
	require.NotContains(t, bodyNoComments, "terraform-google-modules/kms/google",
		"gcp/kms/main.tf must not re-vendor the upstream module (issue #182)")
	// No module "kms" {} block. Asserting on the block-header form
	// (with quotes) lets the moved {} blocks below legitimately use
	// the bare `module.kms.*` address syntax on their `from` side.
	require.NotContains(t, bodyNoComments, `module "kms"`,
		"gcp/kms/main.tf must not declare a module \"kms\" block (issue #182)")
	require.NotContains(t, bodyNoComments, "slice(",
		"gcp/kms/main.tf must not contain slice() expressions — the upstream's failure mode (issue #180/#182)")
	require.NotContains(t, bodyNoComments, "try(module.kms",
		"gcp/kms/main.tf must not retain the surgical try() wrap from PR #181 — the upstream is gone (issue #182)")

	// The replacement resources are present. Asserting on the
	// resource-block headers (rather than on resource address
	// references) catches a rename of the resources themselves, which
	// would also break the moved {} blocks below.
	require.Contains(t, bodyNoComments, `resource "google_kms_key_ring" "this"`,
		"gcp/kms/main.tf must declare google_kms_key_ring.this (issue #182)")
	require.Contains(t, bodyNoComments, `resource "google_kms_crypto_key" "protected"`,
		"gcp/kms/main.tf must declare google_kms_crypto_key.protected for prevent_destroy=true (issue #182)")
	require.Contains(t, bodyNoComments, `resource "google_kms_crypto_key" "ephemeral"`,
		"gcp/kms/main.tf must declare google_kms_crypto_key.ephemeral for prevent_destroy=false (issue #182)")

	// Three moved {} blocks for the default-config state migration:
	// keyring + protected[default] + ephemeral[default]. Anything
	// less and customers upgrading from the upstream module see
	// replace plans on existing keys.
	require.GreaterOrEqual(t, strings.Count(bodyNoComments, "moved {"), 3,
		"gcp/kms/main.tf must include 3 moved {} blocks for default-config state migration (issue #182)")

	outputsTF, ok := files["/outputs.tf"]
	require.True(t, ok, "gcp/kms must include outputs.tf")
	outputsBodyNoComments := stripHCLComments(string(outputsTF))
	require.Contains(t, outputsBodyNoComments, "google_kms_key_ring.this",
		"gcp/kms/outputs.tf must read from the direct resource (issue #182)")
	require.NotContains(t, outputsBodyNoComments, "module.kms",
		"gcp/kms/outputs.tf must not reference the removed upstream module (issue #182)")
	require.NotContains(t, outputsBodyNoComments, "try(",
		"gcp/kms/outputs.tf must not retain the PR #181 try() wrap — the upstream is gone (issue #182)")
}

// TestGetPresetFiles_GCP_CloudBuild_HasWebhookSecretIAM pins the
// issue #190 fix at the embedded-FS layer.
//
// google_cloudbuild_trigger validates webhook secret access on the
// create call. Without an IAM binding granting
// roles/secretmanager.secretAccessor on the webhook secret to the
// Cloud Build P4SA (service-PROJECT_NUMBER@gcp-sa-cloudbuild) the
// create fails with `400 INVALID_ARGUMENT: Request contains an
// invalid argument`. The fix:
//
//   1. data.google_project.this resolves the project number
//      after-enable.
//   2. google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor
//      grants secretAccessor on the webhook secret to the P4SA.
//   3. The trigger depends_on the binding so the binding propagates
//      before the trigger create call fires.
//
// A regression that drops any of those three pieces leaves customers
// hitting INVALID_ARGUMENT on every fresh-project deploy.
func TestGetPresetFiles_GCP_CloudBuild_HasWebhookSecretIAM(t *testing.T) {
	t.Parallel()
	files, err := newTestClient().GetPresetFiles("gcp/cloud_build")
	require.NoError(t, err)

	mainTF, ok := files["/main.tf"]
	require.True(t, ok, "gcp/cloud_build must include main.tf")
	body := string(mainTF)

	// (1) Project number lookup with depends_on on the API enable.
	require.Contains(t, body, `data "google_project" "this"`,
		"gcp/cloud_build/main.tf must look up data.google_project.this for the P4SA project number (issue #190)")

	// (2) IAM binding granting secretAccessor on the webhook secret.
	require.Contains(t, body, `resource "google_secret_manager_secret_iam_member" "cloudbuild_webhook_accessor"`,
		"gcp/cloud_build/main.tf must declare google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor (issue #190)")
	require.Contains(t, body, `roles/secretmanager.secretAccessor`,
		"gcp/cloud_build/main.tf must grant roles/secretmanager.secretAccessor on the webhook secret (issue #190)")
	require.Contains(t, body, `gcp-sa-cloudbuild.iam.gserviceaccount.com`,
		"gcp/cloud_build/main.tf must target the Cloud Build P4SA service-{PROJECT_NUMBER}@gcp-sa-cloudbuild (issue #190)")

	// (3) The trigger depends on the IAM binding so propagation happens
	// before trigger create fires. A trigger that only depends on the
	// API enable still races the IAM propagation.
	require.Contains(t, body, "google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor",
		"gcp/cloud_build/main.tf trigger must depend_on the IAM binding (issue #190)")
}
