package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestComposeStack_Kendra_StandaloneNoImplicitDeps verifies the #760 design:
// Kendra has NO hard ImplicitDependency. Selecting aws_kendra alone composes a
// bare index with no other module dragged in — the S3 data source is additive,
// not required (mirrors the aws/bedrock reasoning).
func TestComposeStack_Kendra_StandaloneNoImplicitDeps(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSKendra},
		Comps:        &Components{AWSKendra: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])

	require.Contains(t, mainTF, `module "aws_kendra"`,
		"the kendra module itself must be composed")
	// No S3 wire when aws_s3 is not selected — the data source stays off.
	require.False(t, wiresAttr(mainTF, "s3_bucket_name", "module.aws_s3.bucket_name"),
		"a standalone Kendra index must NOT wire an S3 bucket when aws_s3 is not selected")
	// No other module should be implicitly pulled in (Kendra has no hard deps).
	require.NotContains(t, mainTF, `module "aws_s3"`,
		"aws_kendra alone must NOT implicitly pull in aws/s3 (the S3 source is additive, not a hard dep)")
	require.NotContains(t, mainTF, "composerpreview",
		"no fabricated preview ARN may leak into a composed stack's main.tf")
}

// TestComposeStack_Kendra_WithS3_WiresDataSource verifies the #760 acceptance
// criterion: aws_kendra + aws_s3 emits the index plus an S3 data source wired
// to the stack bucket (s3_bucket_name / s3_bucket_arn from the aws_s3 module).
func TestComposeStack_Kendra_WithS3_WiresDataSource(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSKendra, KeyAWSS3},
		Comps: &Components{
			AWSKendra: ptrBool(true),
			AWSS3:     ptrBool(true),
		},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])

	require.Contains(t, mainTF, `module "aws_s3"`,
		"aws_s3 must be composed alongside aws_kendra")
	require.Contains(t, mainTF, `module "aws_kendra"`,
		"the kendra module must be composed")

	// Both S3 wires must flow from the aws_s3 module.
	require.True(t, wiresAttr(mainTF, "s3_bucket_name", "module.aws_s3.bucket_name"),
		"DefaultWiring must wire the Kendra data source's s3_bucket_name from module.aws_s3.bucket_name")
	require.True(t, wiresAttr(mainTF, "s3_bucket_arn", "module.aws_s3.bucket_arn"),
		"DefaultWiring must wire the Kendra data source's s3_bucket_arn from module.aws_s3.bucket_arn")

	// S3 (the producer) must be declared before kendra (the consumer) so the
	// bucket_name / bucket_arn outputs exist when kendra references them.
	s3Pos := strings.Index(mainTF, `module "aws_s3"`)
	kendraPos := strings.Index(mainTF, `module "aws_kendra"`)
	require.NotEqual(t, -1, s3Pos)
	require.NotEqual(t, -1, kendraPos)
	require.Less(t, s3Pos, kendraPos,
		"aws_s3 must be declared before aws_kendra so the bucket outputs are available to wire")
}

// TestComposeStack_Kendra_ConfigFlows verifies a composed deploy can set the
// index edition / name / user-context policy through Config — the end-to-end
// proof that Config.AWSKendra reaches the emitted module's auto.tfvars and that
// main.tf wires the module input from the mapped root variable.
func TestComposeStack_Kendra_ConfigFlows(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	cfg.AWSKendra = &struct {
		Edition           string `json:"edition,omitempty"`
		IndexName         string `json:"indexName,omitempty"`
		UserContextPolicy string `json:"userContextPolicy,omitempty"`
	}{
		Edition:           "ENTERPRISE_EDITION",
		IndexName:         "support-search",
		UserContextPolicy: "USER_TOKEN",
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSKendra},
		Comps:        &Components{AWSKendra: ptrBool(true)},
		Cfg:          cfg,
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	tfvars := string(out["/aws_kendra.auto.tfvars"])
	require.NotEmpty(t, tfvars, "the kendra component must emit an auto.tfvars file")

	require.Contains(t, tfvars, "ENTERPRISE_EDITION",
		"a composed deploy must be able to supply a non-default edition via Config")
	require.Contains(t, tfvars, "support-search",
		"index_name must flow from Config into the composed stack")
	require.Contains(t, tfvars, "USER_TOKEN",
		"user_context_policy must flow from Config into the composed stack")

	// main.tf wires the kendra module's edition from the mapped root variable
	// (proving the plumbing, independent of the literal value).
	mainTF := string(out["/main.tf"])
	require.True(t, wiresAttr(mainTF, "edition", "var.aws_kendra_edition"),
		"the kendra module's edition must be wired from the mapped root variable")
}
