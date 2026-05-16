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
	assert.Contains(t, mainStr, `module "aws_eks_nodegroup"`, `main.tf should contain module "aws_eks_nodegroup" block`)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*project\s*=\s*var\.aws_eks_nodegroup_project\s*$`), mainStr,
		"project should be wired as var.aws_eks_nodegroup_project")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*region\s*=\s*var\.aws_eks_nodegroup_region\s*$`), mainStr,
		"region should be wired as var.aws_eks_nodegroup_region")

	// variables.tf should declare namespaced vars
	varsStr := string(varsTF)
	assert.Contains(t, varsStr, `variable "aws_eks_nodegroup_project"`, `variables.tf should declare "aws_eks_nodegroup_project"`)
	assert.Contains(t, varsStr, `variable "aws_eks_nodegroup_region"`, `variables.tf should declare "aws_eks_nodegroup_region"`)

	// aws_eks_nodegroup.auto.tfvars should include mapper-provided values with namespaced keys (allow aligned spacing)
	tfvars, ok := out["/aws_eks_nodegroup.auto.tfvars"]
	require.True(t, ok, "expected /aws_eks_nodegroup.auto.tfvars in output")
	tfvarsStr := string(tfvars)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*aws_eks_nodegroup_project\s*=\s*"demo"\s*$`), tfvarsStr,
		"/aws_eks_nodegroup.auto.tfvars should contain aws_eks_nodegroup_project (namespaced)")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*aws_eks_nodegroup_region\s*=\s*"us-east-1"\s*$`), tfvarsStr,
		"/aws_eks_nodegroup.auto.tfvars should contain aws_eks_nodegroup_region (namespaced)")

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

// extractMovedBlocks parses a (comment-stripped) HCL body and returns
// the from→to address pairs declared in `moved {}` blocks. Used to
// pin state-migration assertions in structural tests.
//
// The regex tolerates any whitespace between `moved`, `{`, `from =`,
// `to =`, and `}`. It captures the address expressions verbatim
// (including bracket-keyed indices like `["default"]` and `[0]`).
//
// fatals via the testing.T on a parse failure rather than returning
// an error — these are deterministic structural pins, never expected
// to silently degrade.
func extractMovedBlocks(t *testing.T, hclBody string) map[string]string {
	t.Helper()
	// (?ms) → multiline + dotall. Each capture group:
	//   1: the `from = ...` rhs (terminated by newline)
	//   2: the `to = ...` rhs (terminated by newline)
	re := regexp.MustCompile(`(?ms)moved\s*\{\s*from\s*=\s*([^\n]+?)\s*to\s*=\s*([^\n]+?)\s*\}`)
	matches := re.FindAllStringSubmatch(hclBody, -1)
	out := make(map[string]string, len(matches))
	for _, m := range matches {
		require.Len(t, m, 3, "regex capture shape changed; expected 3 groups (full match + from + to)")
		from := strings.TrimSpace(m[1])
		to := strings.TrimSpace(m[2])
		require.NotContains(t, out, from,
			"duplicate `from = %s` in moved blocks — terraform forbids two moved blocks rebinding the same source", from)
		out[from] = to
	}
	return out
}

// stripFullLineComments removes lines whose first non-whitespace
// character begins a `#`- or `//`-style comment. It does NOT handle:
//
//   - Inline trailing comments (e.g. `name = "foo" # bar` keeps the
//     entire line, including the trailing `# bar`).
//   - Block comments (`/* ... */` is preserved verbatim).
//   - String literals that happen to contain a `#` or `//` (e.g.
//     `"#abc123"` is preserved, which is the correct behavior).
//
// Use it for structural pins that need to ignore documentation
// blocks (e.g. the migration recipe in gcp/kms/main.tf's header
// comment legitimately references the upstream's old module
// addresses — those references are documentation, not active
// code). Callers MUST keep banned-token references on full-line
// comments only — appending `# preserved for slice() history` to an
// active code line would silently invalidate any
// `require.NotContains(stripped, "slice(")` assertion that depends
// on this helper.
//
// moved {} blocks describing old upstream addresses are intentionally
// NOT stripped: they ARE active HCL (terraform reads them on plan)
// and structural pins treat them as such — a `moved.from` address
// pointing at `module.kms` is expected and necessary.
func stripFullLineComments(s string) string {
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
	bodyNoComments := stripFullLineComments(body)

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
	// keyring + protected[default] + ephemeral[default]. The exact
	// from→to pairs matter — a refactor that renames `protected` to
	// `current` and forgets to update the moved block silently
	// destroys customers' keys (the rebind targets a non-existent
	// address, then the new resource creates fresh and the old
	// state entry is orphaned). The pin asserts each expected pair
	// rather than just counting blocks.
	moved := extractMovedBlocks(t, bodyNoComments)
	expectedMoves := map[string]string{
		"module.kms.google_kms_key_ring.key_ring":           "google_kms_key_ring.this",
		`module.kms.google_kms_crypto_key.key[0]`:           `google_kms_crypto_key.protected["default"]`,
		`module.kms.google_kms_crypto_key.key_ephemeral[0]`: `google_kms_crypto_key.ephemeral["default"]`,
	}
	require.Len(t, moved, len(expectedMoves),
		"gcp/kms/main.tf must include exactly %d moved {} blocks for default-config state migration (issue #182); got %d: %v",
		len(expectedMoves), len(moved), moved)
	for from, wantTo := range expectedMoves {
		gotTo, ok := moved[from]
		require.True(t, ok,
			"gcp/kms/main.tf is missing the moved {} block for `from = %s` (issue #182 — customers' state for this address will not migrate)", from)
		require.Equal(t, wantTo, gotTo,
			"gcp/kms/main.tf moved block for `from = %s` rebinds to `%s` but should rebind to `%s` (issue #182 — wrong target silently orphans state and creates a fresh resource)", from, gotTo, wantTo)
	}

	outputsTF, ok := files["/outputs.tf"]
	require.True(t, ok, "gcp/kms must include outputs.tf")
	outputsBodyNoComments := stripFullLineComments(string(outputsTF))
	require.Contains(t, outputsBodyNoComments, "google_kms_key_ring.this",
		"gcp/kms/outputs.tf must read from the direct resource (issue #182)")
	require.NotContains(t, outputsBodyNoComments, "module.kms",
		"gcp/kms/outputs.tf must not reference the removed upstream module (issue #182)")
	require.NotContains(t, outputsBodyNoComments, "try(",
		"gcp/kms/outputs.tf must not retain the PR #181 try() wrap — the upstream is gone (issue #182)")
}

// TestGetPresetFiles_GCP_CloudBuild_HasWebhookSecretIAM pins the
// issue #190 webhook-secret IAM binding shape at the embedded-FS
// layer. (#197's time_sleep mitigation was retired in v0.7.2 once the
// real root cause — missing BYOSA on the trigger — was isolated; see
// pkg/composer/cloud_build_byosa_test.go for the BYOSA pins.)
//
// The Cloud Build P4SA (service-PROJECT_NUMBER@gcp-sa-cloudbuild)
// needs roles/secretmanager.secretAccessor on the webhook secret so
// the trigger can read the secret at invocation time. Without the
// binding, webhook calls fail at runtime even if the trigger creates
// successfully:
//
//  1. data.google_project.this resolves the project number
//     after-enable.
//  2. google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor
//     grants secretAccessor on the webhook secret to the P4SA.
//  3. The trigger depends_on the IAM binding so the binding lands
//     before the trigger is created.
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

	// (3) Trigger depends_on the IAM binding (transitive ordering).
	require.Contains(t, body, "google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor",
		"gcp/cloud_build/main.tf trigger depends_on must reference the webhook-secret IAM binding (issue #190)")
}

// TestGetPresetFiles_GCP_IdentityPlatform_NoRootOnlyBlocks pins the
// issue #199 regression fix at the embedded-FS layer.
//
// v0.7.0 attempted to fix #197 (Identity Platform singleton non-
// idempotency) with a child-module `import {}` block. Terraform 1.5+
// rejects `import {}` and `removed {}` blocks anywhere except the root
// module — when the composer instantiates this preset as
// `module "gcp_identity_platform" {}`, `terraform init` fails with
// "An import block was detected in 'module.gcp_identity_platform'.
// Import blocks are only allowed in the root module." That regressed
// harder than #197: hard fail at init, before apply could even start.
//
// v0.7.2 (#201) lands the proper adoption fix in-tree via the
// gcp/adopt_singleton helper module: `module "adopt"` issues a
// plan-time REST GET against the IP config endpoint and outputs a
// plan-time-known should_create boolean that gates `count` on the
// singleton resource. No cross-module `import {}` block needed.
//
// A regression that re-introduces `import {}` (or `removed {}`) inside
// this child module reopens #199 — every customer deploy hits the init
// failure before any other fix can run.
func TestGetPresetFiles_GCP_IdentityPlatform_NoRootOnlyBlocks(t *testing.T) {
	t.Parallel()
	files, err := newTestClient().GetPresetFiles("gcp/identity_platform")
	require.NoError(t, err)

	mainTF, ok := files["/main.tf"]
	require.True(t, ok, "gcp/identity_platform must include main.tf")
	body := string(mainTF)

	// (1) Root-only block guard. `import {}` and `removed {}` are TF
	// 1.5+ root-module-only constructs (#199). Match a top-level
	// declaration: a line that starts with `import {` or `removed {`
	// (no leading whitespace), which excludes the word appearing inside
	// comments (which begin with `#` or `//`) or inside other blocks.
	for line := range strings.SplitSeq(body, "\n") {
		require.False(t, strings.HasPrefix(line, "import {"),
			"gcp/identity_platform/main.tf must not contain a top-level `import {}` block — root-only in TF 1.5+ (issue #199)")
		require.False(t, strings.HasPrefix(line, "removed {"),
			"gcp/identity_platform/main.tf must not contain a top-level `removed {}` block — root-only in TF 1.5+ (issue #199 sibling)")
	}

	// (2) The CREATE-with-ignore_changes path remains in place. Once
	// the resource is in state, ignore_changes = all prevents drift
	// fights against console edits or post-CREATE GCP-side changes.
	require.Contains(t, body, "ignore_changes = all",
		"gcp/identity_platform/main.tf must pin lifecycle.ignore_changes = all on the singleton config (issues #197, #199)")

	// (3) Regression guard: the API-enable resource must remain in
	// place — every Identity Platform deploy starts here.
	require.Contains(t, body, `resource "google_project_service" "identity_platform"`,
		"gcp/identity_platform/main.tf must keep the identitytoolkit.googleapis.com API enable (issue #197)")
	require.Contains(t, body, `service = "identitytoolkit.googleapis.com"`,
		"gcp/identity_platform/main.tf must enable identitytoolkit.googleapis.com (issue #197)")
}
