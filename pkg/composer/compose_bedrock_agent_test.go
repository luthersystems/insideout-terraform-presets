package composer

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wiresAttr reports whether composed HCL wires attr to the given RHS
// expression, tolerant of the composer's `=`-alignment whitespace.
func wiresAttr(hcl, attr, rhs string) bool {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(attr) + `\s*=\s*` + regexp.QuoteMeta(rhs) + `\s*$`)
	return re.MatchString(hcl)
}

// TestComposeStack_BedrockAgent_ImplicitLambda verifies the #762 acceptance
// criterion: selecting aws_bedrock_agent alone drags in aws/lambda (the
// action-group executor is a HARD ImplicitDependency) and DefaultWiring feeds
// the agent's action_group_lambda_arn from module.aws_lambda.function_arn.
func TestComposeStack_BedrockAgent_ImplicitLambda(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSBedrockAgent},
		Comps:        &Components{AWSBedrockAgent: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])

	// Lambda must be implicitly composed (action-group executor).
	require.Contains(t, mainTF, `module "aws_lambda"`,
		"aws_bedrock_agent must implicitly pull in the aws/lambda module (action-group executor)")
	require.Contains(t, mainTF, `module "aws_bedrock_agent"`,
		"the agent module itself must be composed")

	// The agent's action_group_lambda_arn must be wired from the lambda module.
	require.True(t, wiresAttr(mainTF, "action_group_lambda_arn", "module.aws_lambda.function_arn"),
		"DefaultWiring must wire the agent's action_group_lambda_arn from module.aws_lambda.function_arn")

	// A bedrock-agent-only stack (no aws_bedrock) must NOT wire a KB id.
	require.NotContains(t, mainTF, "knowledge_base_id",
		"a bedrock-agent stack without aws_bedrock must not wire knowledge_base_id")

	// No fabricated preview ARN may leak into the composed stack.
	require.NotContains(t, mainTF, "composerpreview",
		"no fabricated preview ARN may leak into a composed stack's main.tf")
}

// TestComposeStack_BedrockAgent_WiresKnowledgeBase verifies the #762 "agent
// that does RAG" criterion: when aws_bedrock (KB, #757) is selected alongside
// aws_bedrock_agent, the composed stack wires the agent's knowledge_base_id
// from the bedrock module's knowledge_base_id output — no stub, no fabricated
// id. This is the KB-association composition described in the ticket.
func TestComposeStack_BedrockAgent_WiresKnowledgeBase(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSBedrockAgent,
			KeyAWSBedrock,
		},
		Comps: &Components{
			AWSBedrockAgent: ptrBool(true),
			AWSBedrock:      ptrBool(true),
		},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])

	// The agent's knowledge_base_id must be wired from the bedrock KB output.
	require.True(t, wiresAttr(mainTF, "knowledge_base_id", "module.aws_bedrock.knowledge_base_id"),
		"composing aws_bedrock with aws_bedrock_agent must wire the agent's knowledge_base_id from module.aws_bedrock.knowledge_base_id (KB association, #757 outputs)")

	// Lambda is still implicitly present (the executor).
	require.Contains(t, mainTF, `module "aws_lambda"`,
		"aws_bedrock_agent must still implicitly pull in aws/lambda alongside the KB wiring")

	// aws_bedrock must compose BEFORE aws_bedrock_agent so its knowledge_base_id
	// output exists when the agent module references it (ComposeOrder contract).
	bedrockPos := strings.Index(mainTF, `module "aws_bedrock"`)
	agentPos := strings.Index(mainTF, `module "aws_bedrock_agent"`)
	require.NotEqual(t, -1, bedrockPos)
	require.NotEqual(t, -1, agentPos)
	require.Less(t, bedrockPos, agentPos,
		"aws_bedrock must be declared before aws_bedrock_agent so the KB output is available to wire")
}
