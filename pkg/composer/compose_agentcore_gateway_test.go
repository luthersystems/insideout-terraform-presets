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

// TestComposeStack_AgentCoreGateway_JWTConfigFlows verifies a composed deploy
// can point the gateway's JWT authorizer at a REAL OIDC issuer through Config
// — the preset's jwt_discovery_url default is a placeholder, so without a
// composer surface a composed gateway would ship unauthenticatable. This is
// the end-to-end proof that Config.AWSAgentCoreGateway.JwtDiscoveryURL (and the
// allowed-audience/clients allowlists) reach the emitted module block.
func TestComposeStack_AgentCoreGateway_JWTConfigFlows(t *testing.T) {
	t.Parallel()

	discovery := "https://auth.example.com/.well-known/openid-configuration"

	cfg := &Config{}
	cfg.AWSAgentCoreGateway = &struct {
		GatewayName        string   `json:"gatewayName,omitempty"`
		ProtocolType       string   `json:"protocolType,omitempty"`
		JwtDiscoveryURL    string   `json:"jwtDiscoveryUrl,omitempty"`
		JwtAllowedAudience []string `json:"jwtAllowedAudience,omitempty"`
		JwtAllowedClients  []string `json:"jwtAllowedClients,omitempty"`
	}{
		JwtDiscoveryURL:    discovery,
		JwtAllowedAudience: []string{"insideout-agents"},
		JwtAllowedClients:  []string{"client-abc"},
	}

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSAgentCoreGateway},
		Comps:        &Components{AWSAgentCoreGateway: ptrBool(true)},
		Cfg:          cfg,
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	// The composer maps config values into a per-component auto.tfvars file
	// (main.tf references them as root variables), so the literal JWT config
	// lands there — assert the end-to-end flow against the tfvars output.
	tfvars := string(out["/aws_agentcore_gateway.auto.tfvars"])
	require.NotEmpty(t, tfvars, "the gateway component must emit an auto.tfvars file")

	// The real discovery URL must reach the composed stack — NOT the preset's
	// example.com placeholder default.
	require.Contains(t, tfvars, discovery,
		"a composed deploy must be able to supply a real jwt_discovery_url via Config")
	require.NotContains(t, tfvars, "https://example.com/.well-known/openid-configuration",
		"the placeholder example.com discovery URL must not leak into a configured composed stack")

	// The allowlists must reach the stack too.
	require.Contains(t, tfvars, "insideout-agents",
		"jwt_allowed_audience must flow from Config into the composed stack")
	require.Contains(t, tfvars, "client-abc",
		"jwt_allowed_clients must flow from Config into the composed stack")

	// main.tf wires the gateway module's jwt_discovery_url from the mapped root
	// variable (proving the plumbing, independent of the literal value).
	mainTF := string(out["/main.tf"])
	require.True(t, wiresAttr(mainTF, "jwt_discovery_url", "var.aws_agentcore_gateway_jwt_discovery_url"),
		"the gateway module's jwt_discovery_url must be wired from the mapped root variable")
}
