package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKMSKeyARN = "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"

// TestMapper_S3KMSKeyARN pins the opt-in contract of #909 for aws/s3: an
// empty field leaves kms_key_arn out (SSE-S3, the preset default, so live
// readers keep access); a set field is emitted.
func TestMapper_S3KMSKeyARN(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyAWSS3, nil, &Config{AWSS3: &AWSS3Config{}}, "", "")
	require.NoError(t, err)
	assert.NotContains(t, vals, "kms_key_arn")

	vals, err = m.BuildModuleValues(KeyAWSS3, nil, &Config{AWSS3: &AWSS3Config{KMSKeyARN: " " + testKMSKeyARN + " "}}, "", "")
	require.NoError(t, err)
	assert.Equal(t, testKMSKeyARN, vals["kms_key_arn"])
}

// TestMapper_SageMakerWorkspaceKMSKeyARN is the same contract for the
// SageMaker workspace bucket.
func TestMapper_SageMakerWorkspaceKMSKeyARN(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyAWSSageMaker, nil, &Config{AWSSageMaker: &AWSSageMakerConfig{}}, "", "")
	require.NoError(t, err)
	assert.NotContains(t, vals, "workspace_kms_key_arn")

	vals, err = m.BuildModuleValues(KeyAWSSageMaker, nil, &Config{AWSSageMaker: &AWSSageMakerConfig{WorkspaceKMSKeyARN: testKMSKeyARN}}, "", "")
	require.NoError(t, err)
	assert.Equal(t, testKMSKeyARN, vals["workspace_kms_key_arn"])
}

// TestValidate_KMSKeyARNs pins that Validate uses each preset's rule:
// aws/s3 takes a key or alias ARN; the SageMaker workspace key must be a
// key ARN, because its IAM grant matches the key ARN.
func TestValidate_KMSKeyARNs(t *testing.T) {
	t.Parallel()

	alias := "arn:aws:kms:us-east-1:123456789012:alias/data"
	for _, tc := range []struct {
		field string
		cfg   *Config
		ok    bool
	}{
		{"aws_s3.kmsKeyArn", &Config{AWSS3: &AWSS3Config{KMSKeyARN: testKMSKeyARN}}, true},
		{"aws_s3.kmsKeyArn", &Config{AWSS3: &AWSS3Config{KMSKeyARN: alias}}, true},
		{"aws_s3.kmsKeyArn", &Config{AWSS3: &AWSS3Config{KMSKeyARN: "not-an-arn"}}, false},
		{"aws_sagemaker.workspaceKmsKeyArn", &Config{AWSSageMaker: &AWSSageMakerConfig{WorkspaceKMSKeyARN: testKMSKeyARN}}, true},
		{"aws_sagemaker.workspaceKmsKeyArn", &Config{AWSSageMaker: &AWSSageMakerConfig{WorkspaceKMSKeyARN: alias}}, false},
	} {
		_, bad := issuesByField(Validate(nil, tc.cfg))[tc.field]
		assert.Equal(t, !tc.ok, bad, "%s: %+v", tc.field, tc.cfg)
	}
}

// TestComposeStack_S3KMSKeyReachesReaders pins the end-to-end path of #909:
// the key reaches aws/s3's tfvars, and the readers the composer knows
// (aws/bedrock, aws/kendra) get it from module.aws_s3.kms_key_arn for
// their kms:Decrypt grant.
func TestComposeStack_S3KMSKeyReachesReaders(t *testing.T) {
	t.Parallel()

	out, err := newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSS3, KeyAWSBedrock, KeyAWSKendra},
		Comps: &Components{
			Cloud:      "AWS",
			AWSS3:      ptrBool(true),
			AWSBedrock: ptrBool(true),
			AWSKendra:  ptrBool(true),
		},
		Cfg:     &Config{AWSS3: &AWSS3Config{KMSKeyARN: testKMSKeyARN}},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	requireTfvarAssignment(t, string(out["/aws_s3.auto.tfvars"]), "aws_s3_kms_key_arn", `"`+testKMSKeyARN+`"`,
		"the opted-in key must reach aws/s3")

	mainTF := string(out["/main.tf"])
	for _, mod := range []string{"aws_bedrock", "aws_kendra"} {
		block := moduleBlock(t, mainTF, mod)
		assert.True(t, wiresAttr(block, "s3_kms_key_arn", "module.aws_s3.kms_key_arn"),
			"%s must get the bucket's key for its kms:Decrypt grant", mod)
	}

	// Without the opt-in the bucket stays SSE-S3: no kms_key_arn tfvar.
	out, err = newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSS3},
		Comps:        &Components{Cloud: "AWS", AWSS3: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	requireNoTfvarAssignment(t, string(out["/aws_s3.auto.tfvars"]), "aws_s3_kms_key_arn",
		"a bucket that did not opt in must keep SSE-S3")
}

// moduleBlock returns the text of one top-level module block of a composed
// main.tf, so an assertion about a wired argument names its module.
func moduleBlock(t *testing.T, mainTF, name string) string {
	t.Helper()
	start := strings.Index(mainTF, `module "`+name+`" {`)
	require.GreaterOrEqual(t, start, 0, "module %q not in main.tf", name)
	rest := mainTF[start+1:]
	if end := strings.Index(rest, "\nmodule \""); end >= 0 {
		return mainTF[start : start+1+end]
	}
	return mainTF[start:]
}
