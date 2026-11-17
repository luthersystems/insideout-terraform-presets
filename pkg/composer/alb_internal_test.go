package composer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapper_ALBInternal pins the opt-in contract of #910: nil leaves
// internal out (the internet-facing default, since a change replaces the
// ALB), and a set value is emitted.
func TestMapper_ALBInternal(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyAWSALB, nil, &Config{AWSALB: &AWSALBConfig{}}, "", "")
	require.NoError(t, err)
	assert.NotContains(t, vals, "internal")

	for _, v := range []bool{true, false} {
		v := v
		vals, err = m.BuildModuleValues(KeyAWSALB, &Components{Cloud: "AWS"}, &Config{AWSALB: &AWSALBConfig{Internal: &v}}, "", "")
		require.NoError(t, err)
		assert.Equal(t, v, vals["internal"])
	}
}

// TestMapper_ALBInternalRejectsCloudFront pins the compose-time guard:
// aws/cloudfront takes the ALB as a public origin, so an internal ALB
// behind it would answer every request with 502.
func TestMapper_ALBInternalRejectsCloudFront(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}

	internal, external := true, false
	comps := &Components{Cloud: "AWS", AWSALB: ptrBool(true), AWSCloudFront: ptrBool(true)}

	_, err := m.BuildModuleValues(KeyAWSALB, comps, &Config{AWSALB: &AWSALBConfig{Internal: &internal}}, "", "")
	var ve *ValidationError
	require.True(t, errors.As(err, &ve), "want a ValidationError, got %v", err)
	assert.Contains(t, ve.Error(), "CloudFront")

	vals, err := m.BuildModuleValues(KeyAWSALB, comps, &Config{AWSALB: &AWSALBConfig{Internal: &external}}, "", "")
	require.NoError(t, err, "an explicit internet-facing ALB behind CloudFront is fine")
	assert.Equal(t, false, vals["internal"])
}

// TestComposeStack_ALBInternal pins the composed output: the opt-in
// reaches the ALB tfvars, and the ALB gets the VPC's private subnets so an
// internal ALB has no address in a public subnet.
func TestComposeStack_ALBInternal(t *testing.T) {
	t.Parallel()

	internal := true
	out, err := newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC", AWSALB: ptrBool(true)},
		Cfg:          &Config{AWSALB: &AWSALBConfig{Internal: &internal}},
		Project:      "demo",
		Region:       "us-west-2",
	})
	require.NoError(t, err)
	requireTfvarAssignment(t, string(out["/aws_alb.auto.tfvars"]), "aws_alb_internal", "true",
		"the opt-in must reach the ALB")
	assert.True(t, wiresAttr(moduleBlock(t, string(out["/main.tf"]), "aws_alb"), "private_subnet_ids", "module.aws_vpc.private_subnet_ids"),
		"the ALB must get the VPC's private subnets for the internal case")

	out, err = newTestClient().ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC", AWSALB: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-west-2",
	})
	require.NoError(t, err)
	requireNoTfvarAssignment(t, string(out["/aws_alb.auto.tfvars"]), "aws_alb_internal",
		"an ALB that did not opt in must keep the internet-facing default")
}
