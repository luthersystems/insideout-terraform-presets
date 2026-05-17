package composer

// github_actions_gcp_wiring_test.go covers the issue #597 row 1 composer
// wiring for the gcp/github_actions preset (GitHub Actions Workload
// Identity Federation):
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin:
//   - Forward wiring: selecting KeyGCPGitHubActions causes the composer to
//     emit `module "gcp_github_actions"` in the composed root.
//   - Mapper default: caller-empty cfg.GCPGitHubActions produces the
//     placeholder.invalid/placeholder repo (preset's variable has no
//     default, so without this the single-module preview fails).
//   - Mapper caller-supplied: cfg.GCPGitHubActions.GitHubRepository
//     flows through unchanged to the github_repository module variable.
//   - End-to-end ComposeStack with GCP + KeyGCPGitHubActions succeeds and
//     produces a valid composed root.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapper_GCPGitHubActions_DefaultRepository(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPGitHubActions, &Components{}, &Config{}, "demo", "us-central1")
	require.NoError(t, err)

	repo, ok := vals["github_repository"]
	require.True(t, ok, "mapper must always set github_repository (preset has no default)")
	require.Equal(t, "placeholder.invalid/placeholder", repo,
		"mapper should fall back to placeholder.invalid/placeholder when cfg.GCPGitHubActions.GitHubRepository is unset; the placeholder is shaped to satisfy the OWNER/REPO regex without colliding with any real GitHub identity")
}

func TestMapper_GCPGitHubActions_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	tr := true
	cfg := &Config{
		GCPGitHubActions: &struct {
			GitHubRepository   string   `json:"githubRepository,omitempty"`
			AllowedBranches    []string `json:"allowedBranches,omitempty"`
			AllowedTags        []string `json:"allowedTags,omitempty"`
			AllowedPullRequest *bool    `json:"allowedPullRequest,omitempty"`
			DeployRoles        []string `json:"deployRoles,omitempty"`
		}{
			GitHubRepository:   "luthersystems/foo",
			AllowedBranches:    []string{"main", "release"},
			AllowedTags:        []string{"v1.0.0"},
			AllowedPullRequest: &tr,
			DeployRoles:        []string{"roles/run.admin", "roles/container.developer"},
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPGitHubActions, &Components{}, cfg, "demo", "us-central1")
	require.NoError(t, err)

	require.Equal(t, "luthersystems/foo", vals["github_repository"])
	require.Equal(t, []any{"main", "release"}, vals["allowed_branches"])
	require.Equal(t, []any{"v1.0.0"}, vals["allowed_tags"])
	require.Equal(t, true, vals["allowed_pull_request"])
	require.Equal(t, []any{"roles/run.admin", "roles/container.developer"}, vals["deploy_roles"])
}

// TestMapper_GCPGitHubActions_PartialConfig confirms that when only one
// optional sub-field is set the mapper emits only that sub-field — the
// preset's variables.tf defaults must apply to the rest. Catches a class
// of bug where the mapper would unconditionally emit empty slices /
// false bools that override the module's defaults.
func TestMapper_GCPGitHubActions_PartialConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		GCPGitHubActions: &struct {
			GitHubRepository   string   `json:"githubRepository,omitempty"`
			AllowedBranches    []string `json:"allowedBranches,omitempty"`
			AllowedTags        []string `json:"allowedTags,omitempty"`
			AllowedPullRequest *bool    `json:"allowedPullRequest,omitempty"`
			DeployRoles        []string `json:"deployRoles,omitempty"`
		}{
			GitHubRepository: "luthersystems/foo",
			// AllowedBranches, AllowedTags, AllowedPullRequest, DeployRoles
			// intentionally left at zero values.
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyGCPGitHubActions, &Components{}, cfg, "demo", "us-central1")
	require.NoError(t, err)

	require.Equal(t, "luthersystems/foo", vals["github_repository"])
	_, hasBranches := vals["allowed_branches"]
	require.False(t, hasBranches, "mapper must NOT emit allowed_branches when caller left the slice nil — module default [\"main\"] must win")
	_, hasTags := vals["allowed_tags"]
	require.False(t, hasTags, "mapper must NOT emit allowed_tags when caller left the slice nil")
	_, hasPR := vals["allowed_pull_request"]
	require.False(t, hasPR, "mapper must NOT emit allowed_pull_request when caller left the *bool nil")
	_, hasRoles := vals["deploy_roles"]
	require.False(t, hasRoles, "mapper must NOT emit deploy_roles when caller left the slice nil — module default [run.admin, iam.serviceAccountUser] must win")
}

func TestComposeStack_GCPGitHubActions_Forward(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPGitHubActions},
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

	require.Contains(t, rootStr, `module "gcp_github_actions"`,
		"composed root must declare module gcp_github_actions when KeyGCPGitHubActions is selected")
	require.Contains(t, rootStr, `"./gcp/github_actions"`,
		"module source path must resolve to gcp/github_actions per ModulePath (composer formats with variable padding around =)")

	// Confirm the tfvars file landed with the placeholder repo.
	tfvars, ok := out["/gcp_github_actions.auto.tfvars"]
	require.True(t, ok, "expected gcp_github_actions.auto.tfvars")
	require.Contains(t, string(tfvars), "placeholder.invalid/placeholder",
		"standalone GitHub Actions module should land the placeholder repo so terraform plan can compile; callers MUST override before terraform apply")
}

func TestComposeStack_GCPGitHubActions_CallerSuppliedRepo(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPGitHubActions},
		Comps:        &Components{Cloud: "GCP"},
		Cfg: &Config{
			Region: "us-central1",
			GCPGitHubActions: &struct {
				GitHubRepository   string   `json:"githubRepository,omitempty"`
				AllowedBranches    []string `json:"allowedBranches,omitempty"`
				AllowedTags        []string `json:"allowedTags,omitempty"`
				AllowedPullRequest *bool    `json:"allowedPullRequest,omitempty"`
				DeployRoles        []string `json:"deployRoles,omitempty"`
			}{
				GitHubRepository: "luthersystems/insideout-terraform-presets",
			},
		},
		Project:      "test",
		Region:       "us-central1",
		GCPProjectID: "test-project-12345",
	})
	require.NoError(t, err)

	tfvars, ok := out["/gcp_github_actions.auto.tfvars"]
	require.True(t, ok)
	require.Contains(t, string(tfvars), "luthersystems/insideout-terraform-presets",
		"caller-supplied github_repository must flow through to the tfvars file")
	require.NotContains(t, string(tfvars), "placeholder.invalid",
		"caller-supplied github_repository must override the placeholder default")
}

// TestComponentSelected_GCPGitHubActions pins the coherence.go entry —
// without it ComponentSelected returns false for KeyGCPGitHubActions
// and the orphan-strip pass silently clears cfg.GCPGitHubActions even
// when comps.GCPGitHubActions=&true.
func TestComponentSelected_GCPGitHubActions(t *testing.T) {
	t.Parallel()

	tr := true
	c := &Components{GCPGitHubActions: &tr}
	require.True(t, ComponentSelected(c, KeyGCPGitHubActions),
		"ComponentSelected must return true when comps.GCPGitHubActions=&true")

	fa := false
	c2 := &Components{GCPGitHubActions: &fa}
	require.False(t, ComponentSelected(c2, KeyGCPGitHubActions),
		"ComponentSelected must return false when comps.GCPGitHubActions=&false (explicit deselect)")

	c3 := &Components{}
	require.False(t, ComponentSelected(c3, KeyGCPGitHubActions),
		"ComponentSelected must return false when comps.GCPGitHubActions is nil")
}

// TestGCPIAMPermissions_GitHubActionsCovered pins the iam_actions.go
// entry. Without it RequiredGCPIAMPermissions silently omits the WIF /
// SA-creation permissions a real deploy needs — surfacing as a 403 at
// apply time instead of at the SimulatePrincipalPolicy / testIamPermissions
// pre-deploy check (ui-core #192).
func TestGCPIAMPermissions_GitHubActionsCovered(t *testing.T) {
	t.Parallel()

	perms, ok := GCPIAMPermissions[KeyGCPGitHubActions]
	require.True(t, ok, "GCPIAMPermissions must have an entry for KeyGCPGitHubActions")
	require.NotEmpty(t, perms, "GCPIAMPermissions[KeyGCPGitHubActions] must list at least one permission — WIF + SA + IAM-binding all require explicit perms beyond the always-required set")

	required := RequiredGCPIAMPermissions([]ComponentKey{KeyGCPGitHubActions})
	require.Contains(t, required, "iam.workloadIdentityPools.create",
		"WIF pool create permission must be in the required set")
	require.Contains(t, required, "iam.workloadIdentityPoolProviders.create",
		"WIF provider create permission must be in the required set")
	require.Contains(t, required, "iam.serviceAccounts.create",
		"SA create permission must be in the required set")
	require.Contains(t, required, "resourcemanager.projects.setIamPolicy",
		"Project IAM-binding permission must be in the required set (the deploy SA needs roles bound at the project level)")
}
