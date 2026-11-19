package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Mapper and wiring for the three presets that were placeholders until
// #936: aws/githubactions, aws/codepipeline and aws/grafana.

func TestMapper_AWSGitHubActions_DefaultRepository(t *testing.T) {
	t.Parallel()
	vals, err := DefaultMapper{}.BuildModuleValues(KeyAWSGitHubActions, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)
	// The preset has no default, so a preview without config must still
	// plan. The placeholder can match no real repository.
	require.Equal(t, "placeholder.invalid/placeholder", vals["github_repository"])
	for _, k := range []string{"allowed_branches", "allowed_tags", "allowed_pull_request", "create_oidc_provider", "oidc_provider_arn", "managed_policy_arns"} {
		_, set := vals[k]
		require.False(t, set, "%s unset in config must keep the preset default", k)
	}
}

func TestMapper_AWSGitHubActions_Config(t *testing.T) {
	t.Parallel()
	tr := true
	cfg := &Config{AWSGitHubActions: &AWSGitHubActionsConfig{
		GitHubRepository:   " acme/app ",
		AllowedBranches:    []string{"main", "release"},
		AllowedTags:        []string{"v*"},
		AllowedPullRequest: &tr,
		CreateOIDCProvider: &tr,
		OIDCProviderARN:    "arn:aws:iam::111122223333:oidc-provider/token.actions.githubusercontent.com",
		ManagedPolicyARNs:  []string{"arn:aws:iam::aws:policy/ReadOnlyAccess"},
	}}
	vals, err := DefaultMapper{}.BuildModuleValues(KeyAWSGitHubActions, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)
	require.Equal(t, "acme/app", vals["github_repository"])
	require.Equal(t, []any{"main", "release"}, vals["allowed_branches"])
	require.Equal(t, []any{"v*"}, vals["allowed_tags"])
	require.Equal(t, true, vals["allowed_pull_request"])
	require.Equal(t, true, vals["create_oidc_provider"])
	require.Equal(t, "arn:aws:iam::111122223333:oidc-provider/token.actions.githubusercontent.com", vals["oidc_provider_arn"])
	require.Equal(t, []any{"arn:aws:iam::aws:policy/ReadOnlyAccess"}, vals["managed_policy_arns"])
}

func TestMapper_AWSCodePipeline(t *testing.T) {
	t.Parallel()
	vals, err := DefaultMapper{}.BuildModuleValues(KeyAWSCodePipeline, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)
	require.Equal(t, "placeholder.invalid/placeholder", vals["github_repository"], "a GITHUB source with no config needs a placeholder to plan")

	cfg := &Config{AWSCodePipeline: &AWSCodePipelineConfig{
		SourceType:     "S3",
		SourceS3Bucket: "src",
		SourceS3Key:    "app.zip",
		Branch:         "dev",
		ConnectionARN:  "arn:aws:codestar-connections:us-east-1:111122223333:connection/abc",
	}}
	vals, err = DefaultMapper{}.BuildModuleValues(KeyAWSCodePipeline, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)
	require.Equal(t, "S3", vals["source_type"])
	require.Equal(t, "src", vals["source_s3_bucket"])
	require.Equal(t, "app.zip", vals["source_s3_key"])
	require.Equal(t, "dev", vals["branch"])
	require.Equal(t, "arn:aws:codestar-connections:us-east-1:111122223333:connection/abc", vals["connection_arn"])
	_, set := vals["github_repository"]
	require.False(t, set, "an S3 source needs no repository placeholder")
}

func TestMapper_AWSGrafana(t *testing.T) {
	t.Parallel()
	vals, err := DefaultMapper{}.BuildModuleValues(KeyAWSGrafana, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)
	_, set := vals["authentication_providers"]
	require.False(t, set, "no config keeps the preset default (AWS_SSO)")

	cfg := &Config{AWSGrafana: &AWSGrafanaConfig{
		AuthenticationProviders: []string{"SAML"},
		AdminSSOGroupIDs:        []string{"g-1"},
		SAMLIdPMetadataURL:      "https://idp.example.com/m",
		SAMLAdminRoleValues:     []string{"admin"},
		SAMLEditorRoleValues:    []string{"editor"},
	}}
	vals, err = DefaultMapper{}.BuildModuleValues(KeyAWSGrafana, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)
	require.Equal(t, []any{"SAML"}, vals["authentication_providers"])
	require.Equal(t, []any{"g-1"}, vals["admin_sso_group_ids"])
	require.Equal(t, "https://idp.example.com/m", vals["saml_idp_metadata_url"])
	require.Equal(t, []any{"admin"}, vals["saml_admin_role_values"])
	require.Equal(t, []any{"editor"}, vals["saml_editor_role_values"])
}

// CodePipeline's Build stage runs the aws/codebuild project: selecting the
// pipeline adds CodeBuild, wires its outputs in, and switches the
// project's source and artifacts to CODEPIPELINE.
func TestComposeStack_AWSCodePipeline_WiresCodeBuild(t *testing.T) {
	t.Parallel()
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSCodePipeline},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	in := moduleInputs(t, out["/main.tf"])
	require.Contains(t, in, "aws_codebuild", "CodePipeline must pull in CodeBuild")
	p := in["aws_codepipeline"]
	require.Equal(t, "module.aws_codebuild.project_name", p["codebuild_project_name"])
	require.Equal(t, "module.aws_codebuild.project_arn", p["codebuild_project_arn"])
	require.Equal(t, "module.aws_codebuild.service_role_name", p["codebuild_service_role_name"])
	b := in["aws_codebuild"]
	require.Equal(t, `"CODEPIPELINE"`, b["source_type"])
	require.Equal(t, `"CODEPIPELINE"`, b["artifacts_type"])
}

// Without CodePipeline, CodeBuild keeps its own source settings.
func TestComposeStack_AWSCodeBuild_NoPipelineKeepsSource(t *testing.T) {
	t.Parallel()
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSCodeBuild},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	b := moduleInputs(t, out["/main.tf"])["aws_codebuild"]
	require.NotEqual(t, `"CODEPIPELINE"`, b["source_type"])
}
