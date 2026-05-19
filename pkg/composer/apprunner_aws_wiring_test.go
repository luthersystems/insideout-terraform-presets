package composer

// apprunner_aws_wiring_test.go covers the issue #598 row 2 composer wiring
// for the aws/apprunner preset (AWS analog of gcp/cloud_run for
// managed-container services):
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin:
//   - Forward wiring: selecting KeyAWSAppRunner causes the composer to
//     emit `module "aws_apprunner"` in the composed root, and threads
//     `module.aws_vpc.vpc_id` / private subnets into the module block.
//   - Mapper default: caller-empty cfg.AWSAppRunner emits no overrides
//     (the preset's variables.tf defaults must win).
//   - Mapper caller-supplied: cfg.AWSAppRunner fields flow through to
//     the namespaced module variables.
//   - Mapper partial-config: only fields the caller actually populated
//     are emitted.
//   - Mapper empty-strings-ignored: whitespace-only scalar fields are
//     treated as not-set.
//   - End-to-end ComposeStack with AWS + KeyAWSAppRunner succeeds.
//   - ComponentSelected + AWSIAMActions coverage.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// requireTfvarAssignment is shared with cloud_deploy_gcp_wiring_test.go +
// sagemaker_aws_wiring_test.go (5-arg signature: t, tfvars, key, value, msg).

// -----------------------------------------------------------------------------
// Mapper tests
// -----------------------------------------------------------------------------

// TestMapper_AWSAppRunner_DefaultConfig pins the no-config path. When
// cfg.AWSAppRunner is nil the mapper MUST emit no App Runner-specific
// tfvars — the preset's variables.tf defaults (1 vCPU, 2 GB, ECR_PUBLIC
// hello-app, min=1/max=10, public-accessible, no VPC connector, no custom
// domain) are the source of truth.
func TestMapper_AWSAppRunner_DefaultConfig(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSAppRunner, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)

	// The mapper should NOT emit any of the optional App Runner fields
	// when the caller hasn't set them. The preset's variables.tf default
	// is the source of truth for every unset key.
	for _, k := range []string{
		"service_name", "image_repository_url", "image_repository_type", "port",
		"env_vars", "cpu", "memory", "min_size", "max_size", "max_concurrency",
		"is_publicly_accessible", "auto_deployments_enabled",
		"health_check_protocol", "health_check_path",
		"enable_vpc_connector", "vpc_id", "subnet_ids",
		"custom_domain_name", "enable_www_subdomain",
	} {
		_, has := vals[k]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left cfg.AWSAppRunner nil — module variables.tf default must win",
			k)
	}
}

func TestMapper_AWSAppRunner_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	port := 9090
	minSize := 2
	maxSize := 20
	maxConcurrency := 50
	pubAcc := false
	autoDeploy := true
	enableVPC := true
	enableWWW := true

	cfg := &Config{
		AWSAppRunner: &AWSAppRunnerConfig{
			ServiceName:            "api",
			ImageRepositoryURL:     "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest",
			ImageRepositoryType:    "ECR",
			Port:                   &port,
			EnvVars:                map[string]string{"LOG_LEVEL": "info"},
			CPU:                    "2 vCPU",
			Memory:                 "4 GB",
			MinSize:                &minSize,
			MaxSize:                &maxSize,
			MaxConcurrency:         &maxConcurrency,
			IsPubliclyAccessible:   &pubAcc,
			AutoDeploymentsEnabled: &autoDeploy,
			HealthCheckProtocol:    "HTTP",
			HealthCheckPath:        "/healthz",
			EnableVPCConnector:     &enableVPC,
			VPCID:                  "vpc-real",
			SubnetIDs:              []string{"subnet-a", "subnet-b"},
			CustomDomainName:       "api.example.com",
			EnableWWWSubdomain:     &enableWWW,
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSAppRunner, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "api", vals["service_name"])
	require.Equal(t, "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest", vals["image_repository_url"])
	require.Equal(t, "ECR", vals["image_repository_type"])
	require.Equal(t, 9090, vals["port"])
	require.Equal(t, map[string]any{"LOG_LEVEL": "info"}, vals["env_vars"])
	require.Equal(t, "2 vCPU", vals["cpu"])
	require.Equal(t, "4 GB", vals["memory"])
	require.Equal(t, 2, vals["min_size"])
	require.Equal(t, 20, vals["max_size"])
	require.Equal(t, 50, vals["max_concurrency"])
	require.Equal(t, false, vals["is_publicly_accessible"])
	require.Equal(t, true, vals["auto_deployments_enabled"])
	require.Equal(t, "HTTP", vals["health_check_protocol"])
	require.Equal(t, "/healthz", vals["health_check_path"])
	require.Equal(t, true, vals["enable_vpc_connector"])
	require.Equal(t, "vpc-real", vals["vpc_id"])
	require.Equal(t, []any{"subnet-a", "subnet-b"}, vals["subnet_ids"])
	require.Equal(t, "api.example.com", vals["custom_domain_name"])
	require.Equal(t, true, vals["enable_www_subdomain"])
}

// TestMapper_AWSAppRunner_PartialConfig confirms that when only a subset
// of fields is set the mapper emits only those fields — the preset's
// variables.tf defaults must apply to the rest. Catches a class of bug
// where the mapper would unconditionally emit empty slices / false bools
// that override module defaults.
func TestMapper_AWSAppRunner_PartialConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSAppRunner: &AWSAppRunnerConfig{
			CPU:    "2 vCPU",
			Memory: "4 GB",
			// Every other field intentionally left at zero values.
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSAppRunner, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "2 vCPU", vals["cpu"])
	require.Equal(t, "4 GB", vals["memory"])

	for _, k := range []string{
		"service_name", "image_repository_url", "image_repository_type", "port",
		"env_vars", "min_size", "max_size", "max_concurrency",
		"is_publicly_accessible", "auto_deployments_enabled",
		"health_check_protocol", "health_check_path",
		"enable_vpc_connector", "vpc_id", "subnet_ids",
		"custom_domain_name", "enable_www_subdomain",
	} {
		_, has := vals[k]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left it zero — module default must win",
			k)
	}
}

// TestMapper_AWSAppRunner_EmptyStringsIgnored pins the trimspace gates.
// Whitespace-only string fields (e.g. an unset form field that arrived
// as "   ") must be treated as not-set so the preset defaults kick in
// instead of being overridden with garbage. Sibling pattern of
// TestMapper_AWSSageMaker_EmptyStringsIgnored.
func TestMapper_AWSAppRunner_EmptyStringsIgnored(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		AWSAppRunner: &AWSAppRunnerConfig{
			ServiceName:         "   ",
			ImageRepositoryURL:  "  ",
			ImageRepositoryType: "",
			CPU:                 "  ",
			Memory:              "   ",
			HealthCheckProtocol: "",
			HealthCheckPath:     " ",
			VPCID:               "   ",
			SubnetIDs:           []string{"", "  ", "subnet-real"},
			CustomDomainName:    "  ",
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSAppRunner, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	// subnet_ids: whitespace/empty entries dropped, real one kept.
	require.Equal(t, []any{"subnet-real"}, vals["subnet_ids"])

	for _, k := range []string{
		"service_name", "image_repository_url", "image_repository_type",
		"cpu", "memory", "health_check_protocol", "health_check_path",
		"vpc_id", "custom_domain_name",
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

func TestComposeStack_AWSAppRunner_Forward(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSAppRunner},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, `module "aws_apprunner"`,
		"composed root must declare module aws_apprunner when KeyAWSAppRunner is selected")
	// AWS modules use the legacy `modules/<x>` ModulePath (not `aws/<x>`)
	// — kept for backwards compatibility with the in-account deployer
	// service that already resolves that path.
	require.Contains(t, rootStr, `"./modules/apprunner"`,
		"module source path must resolve to modules/apprunner per ModulePath")

	// KeyAWSAppRunner → KeyAWSVPC implicit dep: aws_vpc must also appear.
	require.Contains(t, rootStr, `module "aws_vpc"`,
		"composer must auto-add KeyAWSVPC when KeyAWSAppRunner is selected (ImplicitDependencies)")

	// Wiring assertion: aws_apprunner's vpc_id argument must literally
	// reference module.aws_vpc.vpc_id. Without this, the previous check
	// only pins that aws_vpc is emitted — it doesn't pin that the value
	// actually flows into aws_apprunner.
	require.Contains(t, rootStr, `module.aws_vpc.vpc_id`,
		"DefaultWiring must thread module.aws_vpc.vpc_id into the aws_apprunner module block")
	// Symmetric subnet wiring — DefaultWiring threads private subnets on
	// non-public VPCs. The default Components shape (no AWSVPC string set)
	// is treated as non-public by isPublicVPC, so the wiring code falls
	// into the private-subnet branch.
	require.Contains(t, rootStr, `module.aws_vpc.private_subnet_ids`,
		"DefaultWiring must thread module.aws_vpc.private_subnet_ids into aws_apprunner.subnet_ids (symmetric with the vpc_id wiring above)")

	// Confirm the tfvars file landed and carries the always-required
	// project / region mappings.
	tfvars, ok := out["/aws_apprunner.auto.tfvars"]
	require.True(t, ok, "expected aws_apprunner.auto.tfvars")
	tfvarsStr := string(tfvars)
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_project", `"test"`,
		"composer must emit aws_apprunner_project tfvar from the always-required project mapping")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_region", `"us-east-1"`,
		"composer must emit aws_apprunner_region tfvar from the always-required region mapping")
}

func TestComposeStack_AWSAppRunner_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	enableVPC := true

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSAppRunner},
		Comps:        &Components{Cloud: "AWS"},
		Cfg: &Config{
			Region: "us-east-1",
			AWSAppRunner: &AWSAppRunnerConfig{
				ServiceName:         "api",
				ImageRepositoryURL:  "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest",
				ImageRepositoryType: "ECR",
				CPU:                 "2 vCPU",
				Memory:              "4 GB",
				CustomDomainName:    "api.example.com",
				EnableVPCConnector:  &enableVPC,
			},
		},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	tfvars, ok := out["/aws_apprunner.auto.tfvars"]
	require.True(t, ok)
	tfvarsStr := string(tfvars)

	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_service_name", `"api"`,
		"caller-supplied ServiceName must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_image_repository_url",
		`"123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest"`,
		"caller-supplied ImageRepositoryURL must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_image_repository_type", `"ECR"`,
		"caller-supplied ImageRepositoryType must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_cpu", `"2 vCPU"`,
		"caller-supplied CPU must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_memory", `"4 GB"`,
		"caller-supplied Memory must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_custom_domain_name", `"api.example.com"`,
		"caller-supplied CustomDomainName must flow through to the namespaced tfvar")
	requireTfvarAssignment(t, tfvarsStr, "aws_apprunner_enable_vpc_connector", `true`,
		"caller-supplied EnableVPCConnector must flow through to the namespaced tfvar")

	// Partial-config contract: the unset Port field must NOT appear in
	// the tfvars (leaving Port nil means the preset's variables.tf
	// default of 8080 wins). The bare-substring check is brittle because
	// the HCL pretty-printer column-aligns assignments — a substring
	// "aws_apprunner_port " (trailing-space) would also match
	// "aws_apprunner_port      = " padding, defeating the assertion.
	// requireNoTfvarAssignment uses the same anchored regex shape as
	// requireTfvarAssignment so the check is robust to padding AND to
	// future keys like aws_apprunner_port_range.
	requireNoTfvarAssignment(t, tfvarsStr, "aws_apprunner_port",
		"unset Port must NOT be emitted in tfvars (the preset's default 8080 must win)")
}

// -----------------------------------------------------------------------------
// Coherence / IAM permission coverage
// -----------------------------------------------------------------------------

// TestComponentSelected_AWSAppRunner pins the coherence.go entry —
// without it ComponentSelected returns false for KeyAWSAppRunner and the
// orphan-strip pass silently clears cfg.AWSAppRunner even when
// comps.AWSAppRunner = &true.
func TestComponentSelected_AWSAppRunner(t *testing.T) {
	t.Parallel()

	tr := true
	c := &Components{AWSAppRunner: &tr}
	require.True(t, ComponentSelected(c, KeyAWSAppRunner),
		"ComponentSelected must return true when comps.AWSAppRunner=&true")

	fa := false
	c2 := &Components{AWSAppRunner: &fa}
	require.False(t, ComponentSelected(c2, KeyAWSAppRunner),
		"ComponentSelected must return false when comps.AWSAppRunner=&false (explicit deselect)")

	c3 := &Components{}
	require.False(t, ComponentSelected(c3, KeyAWSAppRunner),
		"ComponentSelected must return false when comps.AWSAppRunner is nil")
}

// TestAWSIAMPermissions_AppRunnerCovered pins the iam_actions.go entry.
// Without it RequiredAWSIAMActions silently omits the App Runner / IAM /
// VPC / security-group permissions a real deploy needs — surfacing as a
// 403 at apply time instead of at SimulatePrincipalPolicy pre-deploy
// check (ui-core #192).
func TestAWSIAMPermissions_AppRunnerCovered(t *testing.T) {
	t.Parallel()

	actions, ok := AWSIAMActions[KeyAWSAppRunner]
	require.True(t, ok, "AWSIAMActions must have an entry for KeyAWSAppRunner")
	require.NotEmpty(t, actions, "AWSIAMActions[KeyAWSAppRunner] must list at least one action — service create + autoscaling-config create + IAM role create all require explicit perms beyond the always-required set")

	required := RequiredAWSIAMActions([]ComponentKey{KeyAWSAppRunner})
	require.Contains(t, required, "apprunner:CreateService",
		"App Runner service create permission must be in the required set")
	require.Contains(t, required, "apprunner:CreateAutoScalingConfiguration",
		"App Runner autoscaling-config create permission must be in the required set")
	require.Contains(t, required, "apprunner:CreateVpcConnector",
		"App Runner VPC connector create permission must be in the required set (covers the enable_vpc_connector path)")
	require.Contains(t, required, "ec2:CreateSecurityGroup",
		"EC2 CreateSecurityGroup permission must be in the required set (covers the connector's matching SG)")
	require.Contains(t, required, "iam:PassRole",
		"PassRole permission must be in the required set (the App Runner control plane needs to assume both the access role and the instance role we create)")
}
