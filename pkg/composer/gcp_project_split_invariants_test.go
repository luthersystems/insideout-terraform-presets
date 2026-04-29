package composer

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// presetsRoot returns the absolute path to the repository root by walking
// upward from the test binary's working directory until it finds the gcp/
// directory. The composer's tests run from pkg/composer, so the parent of
// the parent is the repo root.
func presetsRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	dir := wd
	for range 5 {
		if _, err := os.Stat(filepath.Join(dir, "gcp")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate gcp/ from %s", wd)
	return ""
}

// readTF returns the trimmed contents of a .tf file under the repo root.
func readTF(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err, "read %s", rel)
	return string(b)
}

// TestGKEWorkloadIdentity_ReferencesProjectID pins the most fragile site of
// the issue #157 split: the workload-identity pool name MUST be the real
// GCP project ID (not the naming prefix), because GCP creates the pool
// resource at "<projectID>.svc.id.goog" and any binding that targets the
// prefix-shaped name silently fails to resolve. A future contributor who
// reverts var.project_id back to var.project here would otherwise ship
// green — this test is the safety net.
//
// Both the main.tf binding and the outputs.tf echo are checked because
// both flow into Workload-Identity-aware downstream stacks.
func TestGKEWorkloadIdentity_ReferencesProjectID(t *testing.T) {
	t.Parallel()
	root := presetsRoot(t)

	main := readTF(t, root, "gcp/gke/main.tf")
	out := readTF(t, root, "gcp/gke/outputs.tf")

	// The pool name interpolation must reference var.project_id.
	require.Regexp(t,
		`"\$\{var\.project_id\}\.svc\.id\.goog"`,
		main,
		"gcp/gke/main.tf must build the workload identity pool name from var.project_id (real GCP project ID)")
	require.Regexp(t,
		`"\$\{var\.project_id\}\.svc\.id\.goog"`,
		out,
		"gcp/gke/outputs.tf identity_namespace echo must reference var.project_id")

	// And the prefix form must NOT appear — defends against accidental
	// reverts that leave both forms.
	require.NotContains(t, main, `"${var.project}.svc.id.goog"`,
		"gcp/gke/main.tf must not reference var.project for the workload identity pool")
	require.NotContains(t, out, `"${var.project}.svc.id.goog"`,
		"gcp/gke/outputs.tf must not reference var.project for the workload identity pool")
}

// labelCapableGCPResources mirrors LABEL_CAPABLE_GCP in
// tests/lint-project-label.sh. Keep sorted; if you add a new label-capable
// type to the lint script, add it here too.
var labelCapableGCPResources = []string{
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_api_gateway_gateway",
	"google_cloud_run_v2_service",
	"google_cloudfunctions2_function",
	"google_compute_global_address",
	"google_compute_global_forwarding_rule",
	"google_compute_instance",
	"google_compute_security_policy",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_redis_instance",
	"google_secret_manager_secret",
	"google_storage_bucket",
	"google_vertex_ai_dataset",
}

// labelBlockRe matches a `labels = { ... }` or `labels = merge({ ... }, ...)`
// attribute body. The (?s) flag lets `.` match newlines.
var labelBlockRe = regexp.MustCompile(`(?s)labels\s*=\s*(?:\{[^}]*\}|merge\([^)]*\))`)

// TestGCPLabels_UseProjectNotProjectID pins the labels-vs-project_id
// contract from issue #157. Labels MUST keep using var.project (the
// per-stack naming prefix) so reliable3's inspector — which groups GCP
// resources by exact label-value match — continues to work. A "consistency
// cleanup" PR that mass-renames var.project -> var.project_id inside
// label merges would silently break that grouping; this test catches it
// statically.
func TestGCPLabels_UseProjectNotProjectID(t *testing.T) {
	t.Parallel()
	root := presetsRoot(t)

	gcpDir := filepath.Join(root, "gcp")
	entries, err := os.ReadDir(gcpDir)
	require.NoError(t, err)

	allow := make(map[string]bool, len(labelCapableGCPResources))
	for _, r := range labelCapableGCPResources {
		allow[r] = true
	}

	resourceRe := regexp.MustCompile(`(?m)^resource\s+"(google_[A-Za-z0-9_]+)"\s+"[A-Za-z0-9_]+"\s*\{`)

	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mainPath := filepath.Join(gcpDir, e.Name(), "main.tf")
		body, err := os.ReadFile(mainPath)
		if err != nil {
			continue // some modules (e.g. cloud_cdn) have no main.tf
		}
		src := string(body)

		// Walk every `resource "google_*" "..." { ... }` block. We index
		// blocks by the offset of the opening header and slice forward
		// until the matching close brace at column 0.
		headers := resourceRe.FindAllSubmatchIndex([]byte(src), -1)
		for _, h := range headers {
			resType := src[h[2]:h[3]]
			if !allow[resType] {
				continue
			}
			// Find the body — first `}` at column 0 after the header.
			bodyStart := h[1] // just after the `{`
			rest := src[bodyStart:]
			block, _, ok := strings.Cut(rest, "\n}")
			if !ok {
				continue
			}

			// Pull the labels = ... attribute (if any) and assert it
			// references var.project, NOT var.project_id, as the
			// `project = ...` key inside the merge.
			labelMatch := labelBlockRe.FindString(block)
			if labelMatch == "" {
				// Lint script ensures labels presence — trust that
				// gate; we're only validating the shape here.
				continue
			}
			checked++
			require.Contains(t, labelMatch, "var.project",
				"%s: %s labels block should reference var.project (naming prefix); got %q",
				mainPath, resType, labelMatch)
			require.NotRegexp(t,
				`project\s*=\s*var\.project_id`,
				labelMatch,
				"%s: %s labels block must NOT use var.project_id — that would break reliable3 inspector grouping (issue #157, #81). Got: %q",
				mainPath, resType, labelMatch)
		}
	}

	require.Greater(t, checked, 0, "expected at least one label-capable GCP resource to inspect — has the allowlist drifted from the modules?")
}

// TestGCPModules_ProjectIDDeclaredAndUsed walks every GCP module that
// references google_*.project = var.project_id and asserts it also
// declares variable "project_id". Pins the bottom half of the contract:
// the var must be declared everywhere it's consumed.
func TestGCPModules_ProjectIDDeclaredAndUsed(t *testing.T) {
	t.Parallel()
	root := presetsRoot(t)

	gcpDir := filepath.Join(root, "gcp")
	entries, err := os.ReadDir(gcpDir)
	require.NoError(t, err)

	// Modules that intentionally have NO project-scoped resources and
	// therefore don't declare project_id. Listed explicitly so a future
	// addition of project-scoped resources to one of these surfaces as a
	// test failure rather than silent miss.
	//
	// cloud_build and cloud_logging were on this list as "skeletons", but
	// the project_id-less form was a latent bug — both create real
	// project-scoped resources (a build trigger and a logging sink) that
	// would land in whatever default project the provider was configured
	// with. Issue #159's self-review fixed that and they are now full
	// project-scoped modules. cloud_monitoring was on the list for the
	// same reason and was fixed in the issue #168 sibling pass — its
	// dashboard now declares project = var.project_id explicitly.
	exempt := map[string]bool{
		"cloud_cdn": true, // locals-only stub
	}

	consumesPattern := regexp.MustCompile(`var\.project_id`)
	declaresPattern := regexp.MustCompile(`(?m)^variable\s+"project_id"\s*\{`)

	var modules []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		modules = append(modules, e.Name())
	}
	sort.Strings(modules)

	for _, m := range modules {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			mainPath := filepath.Join(gcpDir, m, "main.tf")
			varsPath := filepath.Join(gcpDir, m, "variables.tf")

			mainBody, err := os.ReadFile(mainPath)
			if err != nil {
				return // module without a main.tf, skip
			}
			varsBody, err := os.ReadFile(varsPath)
			require.NoError(t, err, "%s: variables.tf should exist", m)

			mainHasProjectID := consumesPattern.MatchString(string(mainBody))
			outputsBody, _ := os.ReadFile(filepath.Join(gcpDir, m, "outputs.tf"))
			outputsHasProjectID := consumesPattern.MatchString(string(outputsBody))
			declared := declaresPattern.MatchString(string(varsBody))

			if exempt[m] {
				require.False(t, mainHasProjectID || outputsHasProjectID,
					"%s is exempt but references var.project_id — remove from exempt list and add the variable declaration", m)
				return
			}

			require.True(t, mainHasProjectID || outputsHasProjectID,
				"%s/main.tf or outputs.tf should reference var.project_id (every project-scoped GCP module should — see issue #157)", m)
			require.True(t, declared,
				"%s/variables.tf should declare variable \"project_id\" since it references var.project_id", m)
		})
	}
}
