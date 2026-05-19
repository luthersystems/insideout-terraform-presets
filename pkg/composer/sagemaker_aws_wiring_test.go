package composer

// sagemaker_aws_wiring_test.go covers the issue #615 composer wiring for
// the aws/sagemaker preset (AWS analog of gcp/vertex_ai for ML workspaces):
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin:
//   - Forward wiring: selecting KeyAWSSageMaker causes the composer to
//     emit `module "aws_sagemaker"` in the composed root.
//   - Mapper default: caller-empty cfg.AWSSageMaker produces only the
//     preview-safe vpc_id / subnet_ids stubs (vpc_id / subnet_ids are
//     required vars without defaults in the preset). No other overrides
//     emitted so the module's HCL defaults win.
//   - Mapper caller-supplied: cfg.AWSSageMaker fields flow through to the
//     namespaced module variables.
//   - Mapper partial-config: only fields the caller actually populated
//     are emitted — leaving others zero must let the preset defaults win.
//   - End-to-end ComposeStack with AWS + KeyAWSSageMaker succeeds.
//   - ComponentSelected + AWSIAMActions coverage.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireTfvarAssignment is shared with cloud_deploy_gcp_wiring_test.go
// (5-arg signature: t, tfvars, key, value, msg).

// -----------------------------------------------------------------------------
// Mapper tests
// -----------------------------------------------------------------------------

func TestMapper_AWSSageMaker_DefaultConfig(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSSageMaker, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)

	// Preview-safe stubs for the two required vars (vpc_id + subnet_ids).
	// Without them single-module preview compose fails with
	// `missing_required_variable`.
	vpcID, ok := vals["vpc_id"]
	require.True(t, ok, "mapper must always set vpc_id (preset has no default — required by AWS provider 6.x for aws_sagemaker_domain)")
	require.Equal(t, "vpc-00000000preview", vpcID,
		"default vpc_id should be the obvious-fake preview stub so leakage into a deploy fails loud at AWS apply")

	subnetIDs, ok := vals["subnet_ids"]
	require.True(t, ok, "mapper must always set subnet_ids (preset has no default)")
	require.Equal(t, []any{"subnet-00000000preview"}, subnetIDs,
		"default subnet_ids should be a single-element preview stub list")

	// Optional vars MUST be absent when caller didn't populate cfg.
	for _, key := range []string{"network_mode", "workspace_bucket", "workspace_bucket_force_destroy", "studio_users", "sagemaker_managed_policy_arn"} {
		_, has := vals[key]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left cfg.AWSSageMaker nil — module default must win",
			key)
	}
}

func TestMapper_AWSSageMaker_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	fd := true
	cfg := &Config{
		AWSSageMaker: &AWSSageMakerConfig{
			VPCID:                       "vpc-real",
			SubnetIDs:                   []string{"subnet-a", "subnet-b"},
			NetworkMode:                 "VpcOnly",
			WorkspaceBucket:             "my-bucket",
			WorkspaceBucketForceDestroy: &fd,
			StudioUsers:                 []string{"alice", "bob"},
			SageMakerManagedPolicyARN:   "arn:aws:iam::123456789012:policy/MyScopedSagemaker",
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSSageMaker, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "vpc-real", vals["vpc_id"])
	require.Equal(t, []any{"subnet-a", "subnet-b"}, vals["subnet_ids"])
	require.Equal(t, "VpcOnly", vals["network_mode"])
	require.Equal(t, "my-bucket", vals["workspace_bucket"])
	require.Equal(t, true, vals["workspace_bucket_force_destroy"])
	require.Equal(t, []any{"alice", "bob"}, vals["studio_users"])
	require.Equal(t, "arn:aws:iam::123456789012:policy/MyScopedSagemaker", vals["sagemaker_managed_policy_arn"])
}

// TestMapper_AWSSageMaker_PartialConfig confirms that when only one
// optional sub-field is set the mapper emits only that sub-field — the
// preset's variables.tf defaults must apply to the rest. Catches a class
// of bug where the mapper would unconditionally emit empty slices /
// false bools that override the module's defaults.
func TestMapper_AWSSageMaker_PartialConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSSageMaker: &AWSSageMakerConfig{
			NetworkMode: "VpcOnly",
			// VPCID, SubnetIDs, WorkspaceBucket, WorkspaceBucketForceDestroy,
			// StudioUsers, SageMakerManagedPolicyARN intentionally left at
			// zero values.
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSSageMaker, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "VpcOnly", vals["network_mode"])

	// vpc_id / subnet_ids stay at the preview stubs (caller didn't set them).
	require.Equal(t, "vpc-00000000preview", vals["vpc_id"])
	require.Equal(t, []any{"subnet-00000000preview"}, vals["subnet_ids"])

	for _, key := range []string{"workspace_bucket", "workspace_bucket_force_destroy", "studio_users", "sagemaker_managed_policy_arn"} {
		_, has := vals[key]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left it zero — module default must win",
			key)
	}
}

// TestMapper_AWSSageMaker_EmptyStringsIgnored pins the trimspace gates:
// caller-supplied whitespace-only strings (e.g. an unset form field that
// arrived as "   ") must be treated as not-set so the preset defaults
// kick in instead of being overridden with garbage.
func TestMapper_AWSSageMaker_EmptyStringsIgnored(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSSageMaker: &AWSSageMakerConfig{
			VPCID:                     "   ",
			SubnetIDs:                 []string{"", "  ", "subnet-real"},
			NetworkMode:               "",
			WorkspaceBucket:           "   ",
			StudioUsers:               []string{"alice", "  ", ""},
			SageMakerManagedPolicyARN: "  ",
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSSageMaker, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	// vpc_id: whitespace ignored → falls back to preview stub.
	require.Equal(t, "vpc-00000000preview", vals["vpc_id"])

	// subnet_ids: empty / whitespace entries dropped, real one kept.
	require.Equal(t, []any{"subnet-real"}, vals["subnet_ids"])

	// studio_users: empty / whitespace entries dropped.
	require.Equal(t, []any{"alice"}, vals["studio_users"])

	// Whitespace-only strings on optional scalar fields → not emitted at all.
	for _, key := range []string{"network_mode", "workspace_bucket", "sagemaker_managed_policy_arn"} {
		_, has := vals[key]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller supplied a whitespace-only string",
			key)
	}
}

// -----------------------------------------------------------------------------
// End-to-end ComposeStack tests
// -----------------------------------------------------------------------------

func TestComposeStack_AWSSageMaker_Forward(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSSageMaker},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, `module "aws_sagemaker"`,
		"composed root must declare module aws_sagemaker when KeyAWSSageMaker is selected")
	// AWS modules use the legacy `modules/<x>` ModulePath (not `aws/<x>`)
	// — kept for backwards compatibility with the in-account deployer
	// service that already resolves that path. See PresetKeyMap +
	// ModulePath in contracts.go.
	require.Contains(t, rootStr, `"./modules/sagemaker"`,
		"module source path must resolve to modules/sagemaker per ModulePath")

	// KeyAWSSageMaker → KeyAWSVPC implicit dep: aws_vpc must also appear.
	require.Contains(t, rootStr, `module "aws_vpc"`,
		"composer must auto-add KeyAWSVPC when KeyAWSSageMaker is selected (ImplicitDependencies)")

	// Confirm the tfvars file landed.
	tfvars, ok := out["/aws_sagemaker.auto.tfvars"]
	require.True(t, ok, "expected aws_sagemaker.auto.tfvars")
	tfvarsStr := string(tfvars)
	// The composer namespaces tfvar keys with the module key prefix
	// (`aws_sagemaker_`) to avoid collisions across modules in the
	// composed root. project / region are always populated by the mapper.
	requireTfvarAssignment(t, tfvarsStr, "aws_sagemaker_project", `"test"`,
		"composer must emit aws_sagemaker_project tfvar from the always-required project mapping")
	requireTfvarAssignment(t, tfvarsStr, "aws_sagemaker_region", `"us-east-1"`,
		"composer must emit aws_sagemaker_region tfvar from the always-required region mapping")
}

func TestComposeStack_AWSSageMaker_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSSageMaker},
		Comps:        &Components{Cloud: "AWS"},
		Cfg: &Config{
			Region: "us-east-1",
			AWSSageMaker: &AWSSageMakerConfig{
				NetworkMode:               "VpcOnly",
				WorkspaceBucket:           "shared-ml-bucket",
				StudioUsers:               []string{"alice", "bob"},
				SageMakerManagedPolicyARN: "arn:aws:iam::123456789012:policy/MyScopedSagemaker",
			},
		},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	tfvars, ok := out["/aws_sagemaker.auto.tfvars"]
	require.True(t, ok)
	tfvarsStr := string(tfvars)

	// All tfvar keys are namespaced with the module-key prefix to avoid
	// collisions across modules in the composed root.
	requireTfvarAssignment(t, tfvarsStr, "aws_sagemaker_network_mode", `"VpcOnly"`,
		"caller-supplied NetworkMode must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_sagemaker_workspace_bucket", `"shared-ml-bucket"`,
		"caller-supplied WorkspaceBucket must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_sagemaker_sagemaker_managed_policy_arn",
		`"arn:aws:iam::123456789012:policy/MyScopedSagemaker"`,
		"caller-supplied SageMakerManagedPolicyARN must flow through to the namespaced tfvar")

	// studio_users is a list, which the tfvars pretty-printer renders on
	// multiple lines for longer lists. Bound the substring check to the
	// studio_users line + the list literal that follows (closing `]`)
	// so we don't bleed into a later tfvar's value.
	studioUsersIdx := strings.Index(tfvarsStr, "aws_sagemaker_studio_users")
	require.GreaterOrEqual(t, studioUsersIdx, 0, "tfvars must contain a studio_users assignment")
	endIdx := strings.Index(tfvarsStr[studioUsersIdx:], "]")
	require.GreaterOrEqual(t, endIdx, 0, "studio_users list literal must terminate with ]")
	studioUsersBlock := tfvarsStr[studioUsersIdx : studioUsersIdx+endIdx+1]
	require.Contains(t, studioUsersBlock, `"alice"`,
		"caller-supplied studio_users[0] must flow into the tfvars list")
	require.Contains(t, studioUsersBlock, `"bob"`,
		"caller-supplied studio_users[1] must flow into the tfvars list")
}

// -----------------------------------------------------------------------------
// Coherence / IAM permission coverage
// -----------------------------------------------------------------------------

// TestComponentSelected_AWSSageMaker pins the coherence.go entry —
// without it ComponentSelected returns false for KeyAWSSageMaker and the
// orphan-strip pass silently clears cfg.AWSSageMaker even when
// comps.AWSSageMaker = &true.
func TestComponentSelected_AWSSageMaker(t *testing.T) {
	t.Parallel()

	tr := true
	c := &Components{AWSSageMaker: &tr}
	require.True(t, ComponentSelected(c, KeyAWSSageMaker),
		"ComponentSelected must return true when comps.AWSSageMaker=&true")

	fa := false
	c2 := &Components{AWSSageMaker: &fa}
	require.False(t, ComponentSelected(c2, KeyAWSSageMaker),
		"ComponentSelected must return false when comps.AWSSageMaker=&false (explicit deselect)")

	c3 := &Components{}
	require.False(t, ComponentSelected(c3, KeyAWSSageMaker),
		"ComponentSelected must return false when comps.AWSSageMaker is nil")
}

// TestAWSIAMPermissions_SageMakerCovered pins the iam_actions.go entry.
// Without it RequiredAWSIAMActions silently omits the SageMaker /
// IAM-role / workspace-S3 permissions a real deploy needs — surfacing as
// a 403 at apply time instead of at SimulatePrincipalPolicy pre-deploy
// check (ui-core #192).
func TestAWSIAMPermissions_SageMakerCovered(t *testing.T) {
	t.Parallel()

	actions, ok := AWSIAMActions[KeyAWSSageMaker]
	require.True(t, ok, "AWSIAMActions must have an entry for KeyAWSSageMaker")
	require.NotEmpty(t, actions, "AWSIAMActions[KeyAWSSageMaker] must list at least one action — domain create + role create + workspace bucket setup all require explicit perms beyond the always-required set")

	required := RequiredAWSIAMActions([]ComponentKey{KeyAWSSageMaker})
	require.Contains(t, required, "sagemaker:CreateDomain",
		"SageMaker domain create permission must be in the required set")
	require.Contains(t, required, "sagemaker:CreateUserProfile",
		"SageMaker user profile create permission must be in the required set")
	require.Contains(t, required, "iam:PassRole",
		"PassRole permission must be in the required set (the SageMaker control plane needs to assume the exec role we create)")
	require.Contains(t, required, "iam:PutRolePolicy",
		"PutRolePolicy permission must be in the required set (inline workspace-access policy on the exec role)")
	require.Contains(t, required, "s3:CreateBucket",
		"S3 CreateBucket permission must be in the required set (preset-managed workspace bucket)")
	require.Contains(t, required, "s3:PutBucketPublicAccessBlock",
		"PutBucketPublicAccessBlock permission must be in the required set (security default on the workspace bucket)")
}
