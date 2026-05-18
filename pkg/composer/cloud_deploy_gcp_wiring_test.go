package composer

// cloud_deploy_gcp_wiring_test.go covers the issue #613 composer wiring for
// the gcp/cloud_deploy preset (Cloud Deploy managed delivery pipeline):
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin:
//   - Mapper default (cfg.GCPCloudDeploy == nil) emits nothing — the preset's
//     variables.tf defaults (staging->prod Cloud Run pair) must apply.
//   - Mapper caller-supplied (full config) flows through verbatim.
//   - Mapper partial-config emits ONLY the supplied sub-field — catches the
//     class of bug where the mapper would unconditionally emit empty
//     slices / false bools that override module defaults.
//   - Forward wiring: selecting KeyGCPCloudDeploy emits
//     `module "gcp_cloud_deploy"` in the composed root + the auto.tfvars
//     file.
//   - End-to-end ComposeStack with caller-supplied targets carries those
//     targets into the tfvars file.
//   - ComponentSelected pins the coherence.go entry (mirrors
//     TestComponentSelected_GCPGitHubActions — without it the orphan-strip
//     pass silently clears cfg.GCPCloudDeploy).
//   - GCPIAMPermissions pins the iam_actions.go entry so the SA pre-flight
//     check fires on the right surface (ui-core #192).

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireTfvarAssignment asserts that tfvars (the raw bytes of an
// .auto.tfvars file) contains a top-level assignment of the form
//
//	<key> = <value>
//
// where the `=` may be preceded by any whitespace (the composer's
// HCL pretty-printer column-aligns assignments and the gap width
// changes whenever the longest key in the file changes). Anchored
// to start-of-line so substrings appearing inside nested objects
// or comments cannot satisfy the check.
func requireTfvarAssignment(t *testing.T, tfvars, key, value, msg string) {
	t.Helper()
	pattern := `(?m)^` + regexp.QuoteMeta(key) + `\s*= ` + regexp.QuoteMeta(value) + `$`
	matched, err := regexp.MatchString(pattern, tfvars)
	require.NoError(t, err, "regexp compile error for %q", pattern)
	require.True(t, matched, "%s\n--- tfvars ---\n%s", msg, tfvars)
}

// TestMapper_GCPCloudDeploy_DefaultConfig pins the no-config path. When
// cfg.GCPCloudDeploy is nil the mapper MUST emit no Cloud Deploy specific
// tfvars — the preset's variables.tf defaults are the source of truth
// (staging->prod Cloud Run pair, "delivery" pipeline short name,
// "clouddeploy-runner" SA short name). Emitting an empty list or empty
// string here would override those defaults and break the single-module
// preview UX.
func TestMapper_GCPCloudDeploy_DefaultConfig(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPCloudDeploy, &Components{}, &Config{}, "demo", "us-central1")
	require.NoError(t, err)

	// The mapper should NOT emit any of the optional Cloud Deploy fields
	// when the caller hasn't set them. The composer's preset-inspection
	// layer will fall back to variables.tf defaults for every unset key.
	for _, k := range []string{"service_account_short_name", "pipeline_short_name", "targets"} {
		_, has := vals[k]
		require.False(t, has,
			"mapper must NOT emit %q when cfg.GCPCloudDeploy is nil — module variables.tf default must win", k)
	}
}

// TestMapper_GCPCloudDeploy_CallerSuppliedConfig pins the full caller-
// supplied path. Every sub-field flows through to its module variable
// unchanged.
func TestMapper_GCPCloudDeploy_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	saShort := "deployer"
	pipeShort := "main-pipeline"
	approve := true
	noApprove := false

	cfg := &Config{
		GCPCloudDeploy: &GCPCloudDeployConfig{
			ServiceAccountShortName: &saShort,
			PipelineShortName:       &pipeShort,
			Targets: []GCPCloudDeployTarget{
				// targets[0]: RequireApproval intentionally nil — pins the
				// per-element "leave it to the preset's optional(bool, false)
				// default" path. A mapper bug that emitted require_approval=false
				// unconditionally for nil pointers would silently couple the
				// on-the-wire shape to a hard-coded default and break that
				// invariant — this assertion catches it.
				{Name: "qa", Runtime: "run", RuntimeTarget: "us-west1"},
				{Name: "staging", Runtime: "run", RuntimeTarget: "us-east1", RequireApproval: &noApprove},
				{Name: "prod", Runtime: "gke", RuntimeTarget: "projects/p/locations/us-central1/clusters/c", RequireApproval: &approve},
			},
		},
	}

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPCloudDeploy, &Components{}, cfg, "demo", "us-central1")
	require.NoError(t, err)

	require.Equal(t, "deployer", vals["service_account_short_name"])
	require.Equal(t, "main-pipeline", vals["pipeline_short_name"])

	targets, ok := vals["targets"].([]any)
	require.True(t, ok, "targets must be a []any (each entry is a map per the preset's list-of-objects schema)")
	require.Len(t, targets, 3)

	zeroth, ok := targets[0].(map[string]any)
	require.True(t, ok, "targets[0] must be a map")
	require.Equal(t, "qa", zeroth["name"])
	require.Equal(t, "run", zeroth["runtime"])
	require.Equal(t, "us-west1", zeroth["runtime_target"])
	_, hasApproval := zeroth["require_approval"]
	require.False(t, hasApproval,
		"mapper must NOT emit require_approval when caller left the per-element *bool nil — preset's optional(bool, false) default must apply per element")

	first, ok := targets[1].(map[string]any)
	require.True(t, ok, "targets[1] must be a map")
	require.Equal(t, "staging", first["name"])
	require.Equal(t, "run", first["runtime"])
	require.Equal(t, "us-east1", first["runtime_target"])
	require.Equal(t, false, first["require_approval"])

	second, ok := targets[2].(map[string]any)
	require.True(t, ok, "targets[2] must be a map")
	require.Equal(t, "prod", second["name"])
	require.Equal(t, "gke", second["runtime"])
	require.Equal(t, "projects/p/locations/us-central1/clusters/c", second["runtime_target"])
	require.Equal(t, true, second["require_approval"])
}

// TestMapper_GCPCloudDeploy_PartialConfig pins the partial-config rule.
// When only one optional sub-field is set the mapper MUST emit only that
// sub-field; the preset's variables.tf defaults must apply to the rest.
// Without this rule the mapper would emit empty slices / strings /
// false-bools that override the preset's defaults, regressing the
// single-module preview UX.
func TestMapper_GCPCloudDeploy_PartialConfig(t *testing.T) {
	t.Parallel()

	pipeShort := "release-pipeline"
	cfg := &Config{
		GCPCloudDeploy: &GCPCloudDeployConfig{
			PipelineShortName: &pipeShort,
			// ServiceAccountShortName, Targets intentionally left zero.
		},
	}

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPCloudDeploy, &Components{}, cfg, "demo", "us-central1")
	require.NoError(t, err)

	require.Equal(t, "release-pipeline", vals["pipeline_short_name"])

	_, hasSA := vals["service_account_short_name"]
	require.False(t, hasSA,
		"mapper must NOT emit service_account_short_name when caller left the *string nil — module default \"clouddeploy-runner\" must win")
	_, hasTargets := vals["targets"]
	require.False(t, hasTargets,
		"mapper must NOT emit targets when caller left the slice nil — module default (staging->prod Cloud Run pair) must win")
}

// TestMapper_GCPCloudDeploy_EmptyStringShortNameIsIgnored confirms the
// trimspace gate inside the mapper: a caller-supplied *string pointer to
// an empty / whitespace-only value is treated as "not supplied" rather
// than emitted as an empty string (which the preset's regex validation
// would reject at plan-time with a noisy error).
func TestMapper_GCPCloudDeploy_EmptyStringShortNameIsIgnored(t *testing.T) {
	t.Parallel()

	empty := ""
	whitespace := "   "

	cfg := &Config{
		GCPCloudDeploy: &GCPCloudDeployConfig{
			ServiceAccountShortName: &empty,
			PipelineShortName:       &whitespace,
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPCloudDeploy, &Components{}, cfg, "demo", "us-central1")
	require.NoError(t, err)

	_, hasSA := vals["service_account_short_name"]
	require.False(t, hasSA,
		"empty-string ServiceAccountShortName must be treated as not supplied — module default wins")
	_, hasPipe := vals["pipeline_short_name"]
	require.False(t, hasPipe,
		"whitespace-only PipelineShortName must be treated as not supplied — module default wins")
}

// TestComposeStack_GCPCloudDeploy_Forward exercises the end-to-end
// composer: selecting KeyGCPCloudDeploy must emit the
// `module "gcp_cloud_deploy"` block in the composed root and produce the
// corresponding auto.tfvars file.
func TestComposeStack_GCPCloudDeploy_Forward(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPCloudDeploy},
		Comps:        &Components{Cloud: "GCP"},
		Cfg:          &Config{Region: "us-central1"},
		Project:      "test",
		Region:       "us-central1",
		GCPProjectID: "test-project-12345",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, `module "gcp_cloud_deploy"`,
		"composed root must declare module gcp_cloud_deploy when KeyGCPCloudDeploy is selected")
	require.Contains(t, rootStr, `"./gcp/cloud_deploy"`,
		"module source path must resolve to gcp/cloud_deploy per ModulePath")

	// The tfvars file lands as gcp_cloud_deploy.auto.tfvars; with default
	// config (no overrides) it should be present even if it carries only
	// the always-emitted project / region / environment trio.
	_, ok = out["/gcp_cloud_deploy.auto.tfvars"]
	require.True(t, ok, "expected gcp_cloud_deploy.auto.tfvars")
}

// TestComposeStack_GCPCloudDeploy_CallerSuppliedConfig exercises the end-
// to-end composer with caller-supplied targets. The custom target list
// must flow through to the tfvars file.
func TestComposeStack_GCPCloudDeploy_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	pipeShort := "release"
	saShort := "deployer-sa"
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPCloudDeploy},
		Comps:        &Components{Cloud: "GCP"},
		Cfg: &Config{
			Region: "us-central1",
			GCPCloudDeploy: &GCPCloudDeployConfig{
				ServiceAccountShortName: &saShort,
				PipelineShortName:       &pipeShort,
				Targets: []GCPCloudDeployTarget{
					{Name: "qa", Runtime: "run", RuntimeTarget: "us-west2"},
					{Name: "prod", Runtime: "run", RuntimeTarget: "us-east1"},
				},
			},
		},
		Project:      "test",
		Region:       "us-central1",
		GCPProjectID: "test-project-12345",
	})
	require.NoError(t, err)

	tfvars, ok := out["/gcp_cloud_deploy.auto.tfvars"]
	require.True(t, ok)
	tfvarsStr := string(tfvars)

	// Top-level scalars: pin the full assignments so a future composer
	// change that re-keys / re-formats the line breaks the test instead
	// of silently passing. The composer namespaces variables with
	// `<component>_` prefix to avoid collisions across modules in the
	// composed root (CLAUDE.md "Downstream Composition"), so the tfvars
	// keys are gcp_cloud_deploy_<var>. The HCL pretty-printer column-
	// aligns `=` across the file so we use a multiline-anchored regex
	// with flexible whitespace before `=` rather than a fixed-width
	// substring match. Every caller-controllable mapper output is end-
	// to-end-pinned here (mapper -> tfvars file) so a bug emitting a
	// value under the wrong namespaced key would fail this test, not
	// just the lower-level TestMapperKeysSubsetOfModuleVariables
	// invariant.
	requireTfvarAssignment(t, tfvarsStr, "gcp_cloud_deploy_pipeline_short_name", `"release"`,
		"caller-supplied pipeline_short_name must flow through to tfvars under the namespaced key")
	requireTfvarAssignment(t, tfvarsStr, "gcp_cloud_deploy_service_account_short_name", `"deployer-sa"`,
		"caller-supplied service_account_short_name must flow through to tfvars under the namespaced key")
	// Nested object values inside the targets list have column-aligned `=`
	// (whitespace width is a function of the longest field name in the
	// block — `runtime_target` here), so we can't pin a single-space
	// `key = value` form. Scope the substring check to the
	// `gcp_cloud_deploy_targets = [...]` section so a value appearing in
	// a comment / unrelated key elsewhere in the file cannot satisfy it.
	targetsIdx := strings.Index(tfvarsStr, "gcp_cloud_deploy_targets = [")
	require.NotEqual(t, -1, targetsIdx, "gcp_cloud_deploy_targets list block must be emitted in tfvars")
	// Bound the section to the matching `}]` so values from later tfvars
	// can't satisfy the per-target substring checks below. The list
	// contains nested `{}` blocks but no nested `[]`, so the first `}]`
	// after targetsIdx closes the targets list.
	closeOffset := strings.Index(tfvarsStr[targetsIdx:], "}]")
	require.NotEqual(t, -1, closeOffset, "targets list block must close with `}]`\n--- tfvars ---\n"+tfvarsStr)
	targetsSection := tfvarsStr[targetsIdx : targetsIdx+closeOffset+2]
	require.Contains(t, targetsSection, `"qa"`,
		"caller-supplied target name \"qa\" must land inside the targets list")
	require.Contains(t, targetsSection, `"us-west2"`,
		"caller-supplied runtime_target \"us-west2\" must land inside the targets list")
	require.Contains(t, targetsSection, `"prod"`,
		"caller-supplied target name \"prod\" must land inside the targets list")
	require.Contains(t, targetsSection, `"us-east1"`,
		"caller-supplied runtime_target \"us-east1\" must land inside the targets list")
}

// TestComponentSelected_GCPCloudDeploy pins the coherence.go entry. Without
// it ComponentSelected returns false for KeyGCPCloudDeploy and the orphan-
// strip pass silently clears cfg.GCPCloudDeploy even when
// comps.GCPCloudDeploy=&true.
func TestComponentSelected_GCPCloudDeploy(t *testing.T) {
	t.Parallel()

	tr := true
	c := &Components{GCPCloudDeploy: &tr}
	require.True(t, ComponentSelected(c, KeyGCPCloudDeploy),
		"ComponentSelected must return true when comps.GCPCloudDeploy=&true")

	fa := false
	c2 := &Components{GCPCloudDeploy: &fa}
	require.False(t, ComponentSelected(c2, KeyGCPCloudDeploy),
		"ComponentSelected must return false when comps.GCPCloudDeploy=&false (explicit deselect)")

	c3 := &Components{}
	require.False(t, ComponentSelected(c3, KeyGCPCloudDeploy),
		"ComponentSelected must return false when comps.GCPCloudDeploy is nil")
}

// TestGCPIAMPermissions_CloudDeployCovered pins the iam_actions.go entry.
// Without it RequiredGCPIAMPermissions silently omits the Cloud Deploy
// permissions a real deploy needs — surfacing as a 403 at apply time
// instead of at the SimulatePrincipalPolicy / testIamPermissions
// pre-deploy check (ui-core #192).
func TestGCPIAMPermissions_CloudDeployCovered(t *testing.T) {
	t.Parallel()

	perms, ok := GCPIAMPermissions[KeyGCPCloudDeploy]
	require.True(t, ok, "GCPIAMPermissions must have an entry for KeyGCPCloudDeploy")
	require.NotEmpty(t, perms, "GCPIAMPermissions[KeyGCPCloudDeploy] must list at least one permission")

	required := RequiredGCPIAMPermissions([]ComponentKey{KeyGCPCloudDeploy})
	require.Contains(t, required, "clouddeploy.deliveryPipelines.create",
		"delivery pipeline create permission must be in the required set")
	require.Contains(t, required, "clouddeploy.targets.create",
		"target create permission must be in the required set")
	require.Contains(t, required, "iam.serviceAccounts.create",
		"runner SA create permission must be in the required set")
	require.Contains(t, required, "resourcemanager.projects.setIamPolicy",
		"project IAM binding permission must be in the required set (the runner SA needs roles/clouddeploy.jobRunner bound at the project level — releaser is held out-of-stack by the release-creator, not the runner)")
	require.Contains(t, required, "serviceusage.services.enable",
		"service-enable permission must be in the required set — the preset's google_project_service activates clouddeploy.googleapis.com, and without this perm the first apply fails with SERVICE_DISABLED 403 on a fresh project")
}

// TestGCPServices_CloudDeployCovered pins the gcp_services.go entry. The
// preset's google_project_service activates clouddeploy.googleapis.com,
// but the pre-deploy serviceusage.batchGet check needs an entry here to
// surface a missing-API error on a fresh project before terraform apply.
func TestGCPServices_CloudDeployCovered(t *testing.T) {
	t.Parallel()

	services, ok := GCPServices[KeyGCPCloudDeploy]
	require.True(t, ok, "GCPServices must have an entry for KeyGCPCloudDeploy")
	require.NotEmpty(t, services, "GCPServices[KeyGCPCloudDeploy] must list at least one service")

	required := RequiredGCPServices([]ComponentKey{KeyGCPCloudDeploy})
	names := make([]string, 0, len(required))
	for _, s := range required {
		names = append(names, s.Name)
	}
	require.Contains(t, names, "clouddeploy.googleapis.com",
		"Cloud Deploy service must be in the required set so a fresh project's missing-API check catches it pre-deploy")
}
