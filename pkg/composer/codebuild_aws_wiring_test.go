package composer

// codebuild_aws_wiring_test.go covers the issue #619 composer wiring
// for the aws/codebuild preset (AWS analog of gcp/cloud_build for
// managed CI/build runners):
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin:
//   - Forward wiring: selecting KeyAWSCodeBuild causes the composer to
//     emit `module "aws_codebuild"` in the composed root, and threads
//     `module.aws_vpc.vpc_id` / private subnets into the module block.
//   - Mapper default: caller-empty cfg.AWSCodeBuild emits no overrides
//     (the preset's variables.tf defaults must win).
//   - Mapper caller-supplied: cfg.AWSCodeBuild fields flow through to
//     the namespaced module variables.
//   - Mapper partial-config: only fields the caller actually populated
//     are emitted.
//   - Mapper empty-strings-ignored: whitespace-only scalar fields are
//     treated as not-set.
//   - End-to-end ComposeStack with AWS + KeyAWSCodeBuild succeeds.
//   - ComponentSelected + AWSIAMActions coverage.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// requireTfvarAssignment / requireNoTfvarAssignment are shared with
// cloud_deploy_gcp_wiring_test.go + apprunner_aws_wiring_test.go +
// sagemaker_aws_wiring_test.go.

// -----------------------------------------------------------------------------
// Mapper tests
// -----------------------------------------------------------------------------

// TestMapper_AWSCodeBuild_DefaultConfig pins the no-config path. When
// cfg.AWSCodeBuild is nil the mapper MUST emit no CodeBuild-specific
// tfvars — the preset's variables.tf defaults
// (BUILD_GENERAL1_SMALL / aws/codebuild/standard:7.0 / GITHUB source /
// NO_ARTIFACTS / no S3 logs / no VPC config) are the source of truth.
func TestMapper_AWSCodeBuild_DefaultConfig(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSCodeBuild, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)

	// The mapper should NOT emit any of the optional CodeBuild fields
	// when the caller hasn't set them. The preset's variables.tf default
	// is the source of truth for every unset key.
	for _, k := range []string{
		"codebuild_project_name", "build_image", "compute_type",
		"source_type", "source_location", "buildspec",
		"artifacts_type", "artifacts_location",
		"enable_s3_logs",
		"vpc_id", "subnet_ids", "security_group_ids",
	} {
		_, has := vals[k]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left cfg.AWSCodeBuild nil — module variables.tf default must win",
			k)
	}
}

func TestMapper_AWSCodeBuild_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	enableS3 := true

	cfg := &Config{
		AWSCodeBuild: &AWSCodeBuildConfig{
			ProjectName:       "ci",
			BuildImage:        "123456789012.dkr.ecr.us-east-1.amazonaws.com/myimage:latest",
			ComputeType:       "BUILD_GENERAL1_LARGE",
			SourceType:        "CODECOMMIT",
			SourceLocation:    "https://git-codecommit.us-east-1.amazonaws.com/v1/repos/myrepo",
			Buildspec:         "version: 0.2",
			ArtifactsType:     "S3",
			ArtifactsLocation: "my-artifacts-bucket",
			EnableS3Logs:      &enableS3,
			VPCID:             "vpc-real",
			SubnetIDs:         []string{"subnet-a", "subnet-b"},
			SecurityGroupIDs:  []string{"sg-real"},
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSCodeBuild, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "ci", vals["codebuild_project_name"])
	require.Equal(t, "123456789012.dkr.ecr.us-east-1.amazonaws.com/myimage:latest", vals["build_image"])
	require.Equal(t, "BUILD_GENERAL1_LARGE", vals["compute_type"])
	require.Equal(t, "CODECOMMIT", vals["source_type"])
	require.Equal(t, "https://git-codecommit.us-east-1.amazonaws.com/v1/repos/myrepo", vals["source_location"])
	require.Equal(t, "version: 0.2", vals["buildspec"])
	require.Equal(t, "S3", vals["artifacts_type"])
	require.Equal(t, "my-artifacts-bucket", vals["artifacts_location"])
	require.Equal(t, true, vals["enable_s3_logs"])
	require.Equal(t, "vpc-real", vals["vpc_id"])
	require.Equal(t, []any{"subnet-a", "subnet-b"}, vals["subnet_ids"])
	require.Equal(t, []any{"sg-real"}, vals["security_group_ids"])
}

// TestMapper_AWSCodeBuild_PartialConfig confirms that when only a
// subset of fields is set the mapper emits only those fields — the
// preset's variables.tf defaults must apply to the rest. Catches a
// class of bug where the mapper would unconditionally emit empty
// slices / false bools that override module defaults.
func TestMapper_AWSCodeBuild_PartialConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSCodeBuild: &AWSCodeBuildConfig{
			ComputeType: "BUILD_GENERAL1_LARGE",
			BuildImage:  "aws/codebuild/amazonlinux2-x86_64-standard:5.0",
			// Every other field intentionally left at zero values.
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSCodeBuild, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "BUILD_GENERAL1_LARGE", vals["compute_type"])
	require.Equal(t, "aws/codebuild/amazonlinux2-x86_64-standard:5.0", vals["build_image"])

	for _, k := range []string{
		"codebuild_project_name", "source_type", "source_location",
		"buildspec", "artifacts_type", "artifacts_location",
		"enable_s3_logs",
		"vpc_id", "subnet_ids", "security_group_ids",
	} {
		_, has := vals[k]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left it zero — module default must win",
			k)
	}
}

// TestMapper_AWSCodeBuild_EmptyStringsIgnored pins the trimspace
// gates. Whitespace-only string fields (e.g. an unset form field that
// arrived as "   ") must be treated as not-set so the preset defaults
// kick in instead of being overridden with garbage. Sibling pattern of
// TestMapper_AWSAppRunner_EmptyStringsIgnored.
func TestMapper_AWSCodeBuild_EmptyStringsIgnored(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSCodeBuild: &AWSCodeBuildConfig{
			ProjectName:       "   ",
			BuildImage:        "  ",
			ComputeType:       "",
			SourceType:        " ",
			SourceLocation:    "  ",
			Buildspec:         "   ",
			ArtifactsType:     "",
			ArtifactsLocation: " ",
			VPCID:             "  ",
			SubnetIDs:         []string{"", "  ", "subnet-real"},
			SecurityGroupIDs:  []string{"  ", "sg-real", ""},
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSCodeBuild, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	// Slices: whitespace/empty entries dropped, real ones kept.
	require.Equal(t, []any{"subnet-real"}, vals["subnet_ids"])
	require.Equal(t, []any{"sg-real"}, vals["security_group_ids"])

	for _, k := range []string{
		"codebuild_project_name", "build_image", "compute_type",
		"source_type", "source_location", "buildspec",
		"artifacts_type", "artifacts_location",
		"vpc_id",
	} {
		_, has := vals[k]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller supplied a whitespace-only string",
			k)
	}
}

// -----------------------------------------------------------------------------
// End-to-end ComposeStack tests
// -----------------------------------------------------------------------------

func TestComposeStack_AWSCodeBuild_Forward(t *testing.T) {
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

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, `module "aws_codebuild"`,
		"composed root must declare module aws_codebuild when KeyAWSCodeBuild is selected")
	// AWS modules use the legacy `modules/<x>` ModulePath (not `aws/<x>`)
	// — kept for backwards compatibility with the in-account deployer
	// service that already resolves that path.
	require.Contains(t, rootStr, `"./modules/codebuild"`,
		"module source path must resolve to modules/codebuild per ModulePath")

	// KeyAWSCodeBuild → KeyAWSVPC implicit dep: aws_vpc must also appear.
	require.Contains(t, rootStr, `module "aws_vpc"`,
		"composer must auto-add KeyAWSVPC when KeyAWSCodeBuild is selected (ImplicitDependencies)")

	// Wiring assertion: aws_codebuild's vpc_id argument must literally
	// reference module.aws_vpc.vpc_id. Without this, the previous check
	// only pins that aws_vpc is emitted — it doesn't pin that the value
	// actually flows into aws_codebuild.
	require.Contains(t, rootStr, `module.aws_vpc.vpc_id`,
		"DefaultWiring must thread module.aws_vpc.vpc_id into the aws_codebuild module block")
	// Symmetric subnet wiring — DefaultWiring threads private subnets on
	// non-public VPCs. The default Components shape (no AWSVPC string set)
	// is treated as non-public by isPublicVPC, so the wiring code falls
	// into the private-subnet branch.
	require.Contains(t, rootStr, `module.aws_vpc.private_subnet_ids`,
		"DefaultWiring must thread module.aws_vpc.private_subnet_ids into aws_codebuild.subnet_ids (symmetric with the vpc_id wiring above)")

	// Confirm the tfvars file landed and carries the always-required
	// project / region mappings.
	tfvars, ok := out["/aws_codebuild.auto.tfvars"]
	require.True(t, ok, "expected aws_codebuild.auto.tfvars")
	tfvarsStr := string(tfvars)
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_project", `"test"`,
		"composer must emit aws_codebuild_project tfvar from the always-required project mapping")
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_region", `"us-east-1"`,
		"composer must emit aws_codebuild_region tfvar from the always-required region mapping")
}

func TestComposeStack_AWSCodeBuild_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	enableS3 := true

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSCodeBuild},
		Comps:        &Components{Cloud: "AWS"},
		Cfg: &Config{
			Region: "us-east-1",
			AWSCodeBuild: &AWSCodeBuildConfig{
				ProjectName:    "ci",
				BuildImage:     "aws/codebuild/standard:7.0",
				ComputeType:    "BUILD_GENERAL1_MEDIUM",
				SourceType:     "GITHUB",
				SourceLocation: "https://github.com/example/repo.git",
				EnableS3Logs:   &enableS3,
			},
		},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	tfvars, ok := out["/aws_codebuild.auto.tfvars"]
	require.True(t, ok)
	tfvarsStr := string(tfvars)

	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_codebuild_project_name", `"ci"`,
		"caller-supplied ProjectName must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_build_image", `"aws/codebuild/standard:7.0"`,
		"caller-supplied BuildImage must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_compute_type", `"BUILD_GENERAL1_MEDIUM"`,
		"caller-supplied ComputeType must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_source_type", `"GITHUB"`,
		"caller-supplied SourceType must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_source_location", `"https://github.com/example/repo.git"`,
		"caller-supplied SourceLocation must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_codebuild_enable_s3_logs", `true`,
		"caller-supplied EnableS3Logs must flow through to the namespaced tfvar")

	// Partial-config contract: the unset Buildspec field must NOT
	// appear in the tfvars (leaving Buildspec "" means the preset's
	// variables.tf default of null wins). Same robust-anchored check
	// as the apprunner port assertion.
	requireNoTfvarAssignment(t, tfvarsStr, "aws_codebuild_buildspec",
		"unset Buildspec must NOT be emitted in tfvars (the preset's null default must win)")
	requireNoTfvarAssignment(t, tfvarsStr, "aws_codebuild_artifacts_type",
		"unset ArtifactsType must NOT be emitted in tfvars (the preset's NO_ARTIFACTS default must win)")
}

// -----------------------------------------------------------------------------
// Coherence / IAM permission coverage
// -----------------------------------------------------------------------------

// TestComponentSelected_AWSCodeBuild pins the coherence.go entry —
// without it ComponentSelected returns false for KeyAWSCodeBuild and
// the orphan-strip pass silently clears cfg.AWSCodeBuild even when
// comps.AWSCodeBuild = &true.
func TestComponentSelected_AWSCodeBuild(t *testing.T) {
	t.Parallel()

	tr := true
	c := &Components{AWSCodeBuild: &tr}
	require.True(t, ComponentSelected(c, KeyAWSCodeBuild),
		"ComponentSelected must return true when comps.AWSCodeBuild=&true")

	fa := false
	c2 := &Components{AWSCodeBuild: &fa}
	require.False(t, ComponentSelected(c2, KeyAWSCodeBuild),
		"ComponentSelected must return false when comps.AWSCodeBuild=&false (explicit deselect)")

	c3 := &Components{}
	require.False(t, ComponentSelected(c3, KeyAWSCodeBuild),
		"ComponentSelected must return false when comps.AWSCodeBuild is nil")
}

// TestAWSIAMPermissions_CodeBuildCovered pins the iam_actions.go
// entry. Without it RequiredAWSIAMActions silently omits the
// CodeBuild / IAM / S3 / EC2 permissions a real deploy needs —
// surfacing as a 403 at apply time instead of at
// SimulatePrincipalPolicy pre-deploy check (ui-core #192).
func TestAWSIAMPermissions_CodeBuildCovered(t *testing.T) {
	t.Parallel()

	actions, ok := AWSIAMActions[KeyAWSCodeBuild]
	require.True(t, ok, "AWSIAMActions must have an entry for KeyAWSCodeBuild")
	require.NotEmpty(t, actions, "AWSIAMActions[KeyAWSCodeBuild] must list at least one action — project create + IAM role create + optional S3/VPC perms all require explicit perms beyond the always-required set")

	required := RequiredAWSIAMActions([]ComponentKey{KeyAWSCodeBuild})
	require.Contains(t, required, "codebuild:CreateProject",
		"CodeBuild project create permission must be in the required set")
	require.Contains(t, required, "iam:PassRole",
		"PassRole permission must be in the required set (the CodeBuild control plane needs to assume the service role we create)")
	require.Contains(t, required, "iam:PutRolePolicy",
		"PutRolePolicy permission must be in the required set (the preset attaches inline policies to the service role)")
	require.Contains(t, required, "logs:CreateLogGroup",
		"CloudWatch Logs CreateLogGroup permission must be in the required set (every build emits a log stream)")
	require.Contains(t, required, "s3:CreateBucket",
		"S3 CreateBucket permission must be in the required set (covers the optional enable_s3_logs path)")
	require.Contains(t, required, "ec2:CreateNetworkInterface",
		"EC2 CreateNetworkInterface permission must be in the required set (covers the optional VPC config path)")
}
