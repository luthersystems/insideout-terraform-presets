package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestComposeStack_AgentCoreGateway_ImplicitLambda verifies the #763 acceptance
// criterion: selecting aws_agentcore_gateway alone drags in aws/lambda (the
// gateway's Lambda tool target is a HARD ImplicitDependency) and DefaultWiring
// feeds the gateway's target_lambda_arn from module.aws_lambda.function_arn.
func TestComposeStack_AgentCoreGateway_ImplicitLambda(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSAgentCoreGateway},
		Comps:        &Components{AWSAgentCoreGateway: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])

	// Lambda must be implicitly composed (the gateway's tool target).
	require.Contains(t, mainTF, `module "aws_lambda"`,
		"aws_agentcore_gateway must implicitly pull in the aws/lambda module (Lambda tool target)")
	require.Contains(t, mainTF, `module "aws_agentcore_gateway"`,
		"the gateway module itself must be composed")

	// The gateway's target_lambda_arn must be wired from the lambda module.
	require.True(t, wiresAttr(mainTF, "target_lambda_arn", "module.aws_lambda.function_arn"),
		"DefaultWiring must wire the gateway's target_lambda_arn from module.aws_lambda.function_arn")

	// No fabricated preview ARN may leak into the composed stack.
	require.NotContains(t, mainTF, "composerpreview",
		"no fabricated preview ARN may leak into a composed stack's main.tf")
}

// TestComposeStack_AgentCoreGateway_ComposesAfterLambda verifies the
// ComposeOrder contract: aws_lambda (the target producer) must be declared
// BEFORE aws_agentcore_gateway (the consumer) so the function_arn output exists
// when the gateway module references it.
func TestComposeStack_AgentCoreGateway_ComposesAfterLambda(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSAgentCoreGateway, KeyAWSLambda},
		Comps: &Components{
			AWSAgentCoreGateway: ptrBool(true),
			AWSLambda:           ptrBool(true),
		},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])

	lambdaPos := strings.Index(mainTF, `module "aws_lambda"`)
	gatewayPos := strings.Index(mainTF, `module "aws_agentcore_gateway"`)
	require.NotEqual(t, -1, lambdaPos)
	require.NotEqual(t, -1, gatewayPos)
	require.Less(t, lambdaPos, gatewayPos,
		"aws_lambda must be declared before aws_agentcore_gateway so the function_arn output is available to wire")
}
