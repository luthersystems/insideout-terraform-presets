package composer

import (
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
)

// TestCloudBuildTriggerHasServiceAccount asserts every
// google_cloudbuild_trigger resource declared anywhere in the preset
// library sets a service_account attribute. Cloud Build's regional /
// 2nd-gen webhook trigger API requires a BYOSA service account on
// create; omitting it surfaces as `400 INVALID_ARGUMENT` with no
// fieldViolations[] (issue #201). This guard would have caught the
// regression at publish time.
//
// Pure structural check — does NOT require GCP credentials, mocks, or
// real apply. Parses the embedded preset HCL offline.
func TestCloudBuildTriggerHasServiceAccount(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	keys, err := c.ListPresetKeysForCloud("gcp")
	require.NoError(t, err)

	checked := 0
	for _, presetPath := range keys {
		files, err := c.GetPresetFiles(presetPath)
		require.NoError(t, err, "GetPresetFiles(%s)", presetPath)

		for fileName, content := range files {
			if !strings.HasSuffix(fileName, ".tf") {
				continue
			}

			f, diags := hclsyntax.ParseConfig(content, fileName, hcl.InitialPos)
			require.False(t, diags.HasErrors(), "parse %s%s: %s", presetPath, fileName, diags.Error())

			body, ok := f.Body.(*hclsyntax.Body)
			if !ok {
				continue
			}

			for _, block := range body.Blocks {
				if block.Type != "resource" {
					continue
				}
				if len(block.Labels) < 2 || block.Labels[0] != "google_cloudbuild_trigger" {
					continue
				}
				_, hasServiceAccount := block.Body.Attributes["service_account"]
				require.True(t, hasServiceAccount,
					"%s%s: resource %q.%q must set service_account (BYOSA, issue #201)",
					presetPath, fileName, block.Labels[0], block.Labels[1])
				checked++
			}
		}
	}

	// Cardinality floor: the loop has to fire at least once for the
	// guard to be meaningful. If it falls to 0 (e.g. someone deletes the
	// cloud_build preset entirely or the iteration shape silently
	// breaks), this test would otherwise pass vacuously and the
	// regression returns.
	require.GreaterOrEqual(t, checked, 1,
		"expected at least one google_cloudbuild_trigger in the preset library; got 0 — test silently passing")
}

// TestGetPresetFiles_GCP_CloudBuild_HasBYOSARunner pins the v0.7.2
// BYOSA-runner shape at the embedded-FS layer. Companion to
// TestCloudBuildTriggerHasServiceAccount (which asserts every trigger
// has *some* service_account); this test pins the specific shape
// gcp/cloud_build emits — a dedicated runner SA + cloudbuild.builds.builder
// project IAM binding, with the trigger's depends_on transitively
// ordered runner-SA → IAM-binding → trigger-create.
//
// A regression that drops the runner SA, the builds.builder grant, or
// the trigger's service_account argument re-opens issue #201:
// `400 INVALID_ARGUMENT` on every webhook trigger create.
func TestGetPresetFiles_GCP_CloudBuild_HasBYOSARunner(t *testing.T) {
	t.Parallel()
	files, err := newTestClient().GetPresetFiles("gcp/cloud_build")
	require.NoError(t, err)

	mainTF, ok := files["/main.tf"]
	require.True(t, ok, "gcp/cloud_build must include main.tf")
	body := string(mainTF)

	// (1) Dedicated runner SA exists.
	require.Contains(t, body, `resource "google_service_account" "cloudbuild_runner"`,
		"gcp/cloud_build/main.tf must declare the BYOSA runner SA google_service_account.cloudbuild_runner (issue #201)")

	// (2) Project-level cloudbuild.builds.builder grant on the runner SA.
	require.Contains(t, body, `resource "google_project_iam_member" "cloudbuild_runner_builds_builder"`,
		"gcp/cloud_build/main.tf must bind roles/cloudbuild.builds.builder to the runner SA (issue #201)")
	require.Contains(t, body, `roles/cloudbuild.builds.builder`,
		"gcp/cloud_build/main.tf must grant roles/cloudbuild.builds.builder so the runner SA can execute builds (issue #201)")

	// (3) Trigger consumes the runner SA via service_account.
	require.Contains(t, body, `service_account = google_service_account.cloudbuild_runner.id`,
		"gcp/cloud_build/main.tf trigger must set service_account = google_service_account.cloudbuild_runner.id (BYOSA, issue #201)")

	// (4) Trigger depends_on the IAM binding so create fires post-grant.
	require.Contains(t, body, `google_project_iam_member.cloudbuild_runner_builds_builder`,
		"gcp/cloud_build/main.tf trigger depends_on must include the runner SA's IAM binding (issue #201)")

	// (5) Counter-pin: the v0.7.0/v0.7.1 IAM-propagation theater code
	// must be gone. The 90s time_sleep was solving the wrong problem —
	// the real issue was missing service_account, not propagation.
	require.NotContains(t, body, `resource "time_sleep" "wait_iam_propagation"`,
		"gcp/cloud_build/main.tf must not retain the v0.7.1 time_sleep.wait_iam_propagation block — root cause was BYOSA, not IAM propagation (issue #201)")
	require.NotContains(t, body, `source  = "hashicorp/time"`,
		"gcp/cloud_build/main.tf must not declare hashicorp/time once time_sleep is removed (issue #201)")
}
