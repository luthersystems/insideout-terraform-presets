package composer

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file closes the composed-stack test gap for the AWS AI catalog
// (tracking #755): the per-component composer tests prove each AI module
// composes and wires correctly in isolation, but nothing exercised a
// SINGLE composed bundle holding the whole 7-layer AWS AI stack through
// terraform end-to-end. The three tests below form a ladder:
//
//   1. TestComposeAWSAIStack_Compose        — pure compose-from-IR + wiring
//                                             asserts (hermetic, always runs)
//   2. TestComposeAWSAIStack_InitValidate   — terraform init + validate of the
//                                             composed bundle (hermetic, no
//                                             creds; skips w/o terraform/-short)
//   3. TestComposeAWSAIStack_ApplyDestroy   — real apply→confirm→destroy against
//                                             a live AWS account (opt-in via
//                                             AI_STACK_APPLY=1; proven in cust3)
//
// The stack composes the full Phase-1/2 AWS AI surface:
//
//   aws_vpc (Private) ─▶ aws_lambda ─▶ aws_bedrock_agent (action-group executor)
//                            └────────▶ aws_agentcore_gateway (MCP lambda target)
//   aws_s3 ─▶ aws_bedrock (Knowledge Base on S3 Vectors + Guardrails)
//   aws_bedrock(KB) ─▶ aws_bedrock_agent (knowledge_base_association)
//   aws_s3 ─▶ aws_kendra (S3 data source)
//   aws_sagemaker (real-time inference endpoint)
//   aws_opensearch (Serverless VECTORSEARCH collection)

// defaultSageMakerImage is the AWS HuggingFace PyTorch inference DLC proven
// green against a real account in aws/sagemaker/tests/integration.tftest.hcl
// (#761). It hub-pulls a tiny model at boot, so no model_data_url is needed.
// Override with AI_STACK_SAGEMAKER_IMAGE for a different region/image.
const defaultSageMakerImage = "763104351884.dkr.ecr.us-east-1.amazonaws.com/" +
	"huggingface-pytorch-inference:2.6.0-transformers4.51.3-cpu-py312-ubuntu22.04-v2.1"

// aiStackKeys is the full Phase-1/2 AWS AI component selection. vpc/lambda/s3
// are listed explicitly (rather than relying on implicit-dep injection) so the
// selection reads as the real stack a caller would request.
var aiStackKeys = []ComponentKey{
	KeyAWSVPC,
	KeyAWSLambda,
	KeyAWSS3,
	KeyAWSBedrock,
	KeyAWSBedrockAgent,
	KeyAWSAgentCoreGateway,
	KeyAWSKendra,
	KeyAWSSageMaker,
	KeyAWSOpenSearch,
}

// aiStackComps selects every AI component plus its hard deps.
func aiStackComps() *Components {
	return &Components{
		AWSVPC:              "Private",
		AWSLambda:           ptrBool(true),
		AWSS3:               ptrBool(true),
		AWSBedrock:          ptrBool(true),
		AWSBedrockAgent:     ptrBool(true),
		AWSAgentCoreGateway: ptrBool(true),
		AWSKendra:           ptrBool(true),
		AWSSageMaker:        ptrBool(true),
		AWSOpenSearch:       ptrBool(true),
	}
}

// aiStackConfig builds the apply-realistic Config for the AI stack. It is built
// via JSON unmarshal because several AI sub-configs are anonymous struct fields
// on Config that can't be set with an inline composite literal. modelImage is
// the SageMaker serving image (env-overridable); the rest pin the cheapest
// real-deployable options (KB on S3 Vectors, Kendra DEVELOPER_EDITION, AOSS
// serverless) and force_destroy so the teardown is clean.
func aiStackConfig(t *testing.T, modelImage string) *Config {
	t.Helper()

	// The agentcore gateway's JWT authorizer needs an OIDC discovery URL. The
	// preset default is a syntactically-valid example.com placeholder that
	// satisfies compose/validate but that AWS may reject at CREATE. Keep that
	// faithful default for the hermetic tests; let the live apply override it
	// with a reachable issuer via AI_STACK_JWT_DISCOVERY_URL.
	jwtURL := "https://example.com/.well-known/openid-configuration"
	if v := strings.TrimSpace(os.Getenv("AI_STACK_JWT_DISCOVERY_URL")); v != "" {
		jwtURL = v
	}

	// Foundation / base model IDs. The committed defaults keep the hermetic
	// tests self-describing (they never invoke a model). For a live apply the
	// IDs must be ACTIVE + access-granted in the target account/region —
	// legacy Claude IDs are now invoke-blocked and newer ones are
	// inference-profile-only (us.* prefix) — so the live run overrides them via
	// AI_STACK_FOUNDATION_MODEL / AI_STACK_BEDROCK_MODEL_ID.
	foundationModel := "anthropic.claude-3-5-sonnet-20240620-v1:0"
	if v := strings.TrimSpace(os.Getenv("AI_STACK_FOUNDATION_MODEL")); v != "" {
		foundationModel = v
	}
	bedrockModelID := "anthropic.claude-3-5-sonnet-20240620-v1:0"
	if v := strings.TrimSpace(os.Getenv("AI_STACK_BEDROCK_MODEL_ID")); v != "" {
		bedrockModelID = v
	}

	configJSON := `{
		"region": "us-east-1",
		"aws_bedrock": {
			"enableKnowledgeBase": true,
			"vectorStore": "s3vectors",
			"modelId": ` + mustJSONString(bedrockModelID) + `,
			"embeddingModelId": "amazon.titan-embed-text-v2:0"
		},
		"aws_bedrock_agent": {
			"foundationModel": ` + mustJSONString(foundationModel) + `,
			"agentName": "ioaistack-agent"
		},
		"aws_agentcore_gateway": {
			"protocolType": "MCP",
			"jwtDiscoveryUrl": ` + mustJSONString(jwtURL) + `,
			"jwtAllowedAudience": ["ioaistack-agentcore"]
		},
		"aws_kendra": {
			"edition": "DEVELOPER_EDITION"
		},
		"aws_opensearch": {
			"deploymentType": "serverless"
		},
		"aws_sagemaker": {
			"enableInference": true,
			"endpointInstanceType": "ml.m5.large",
			"modelEnvironment": {
				"HF_MODEL_ID": "sshleifer/tiny-distilbert-base-cased-distilled-squad",
				"HF_TASK": "question-answering"
			}
		}
	}`

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(configJSON), &cfg),
		"aiStackConfig: unmarshal config JSON")
	require.NotNil(t, cfg.AWSSageMaker, "aiStackConfig: aws_sagemaker config must parse")
	cfg.AWSSageMaker.ModelImage = modelImage
	return &cfg
}

// mustJSONString JSON-encodes s (with surrounding quotes) for safe embedding
// in a JSON literal.
func mustJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// composeAIStack composes the full AWS AI stack from the IR and returns the
// bundle. Shared by all three tests so they exercise identical composition.
func composeAIStack(t *testing.T, modelImage string) Files {
	t.Helper()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: aiStackKeys,
		Comps:        aiStackComps(),
		Cfg:          aiStackConfig(t, modelImage),
		Project:      "ioaistack",
		Region:       "us-east-1",
	})
	require.NoError(t, err, "ComposeStack(full AWS AI stack) must succeed")
	return out
}

// TestComposeAWSAIStack_Compose proves the IR→stack path: the full AI selection
// composes into one bundle with every module present and the headline
// cross-module wiring resolved. Hermetic; runs in CI.
func TestComposeAWSAIStack_Compose(t *testing.T) {
	t.Parallel()

	out := composeAIStack(t, defaultSageMakerImage)
	mainTF := string(out["/main.tf"])
	require.NotEmpty(t, mainTF, "composed bundle must contain /main.tf")

	// Independent assertions use assert (not require) so a regression that
	// drops several modules or wires at once surfaces all of them in one run —
	// the multi-wire breakage this test exists to catch.

	// Every AI-stack module must be present.
	for _, mod := range []string{
		"aws_vpc", "aws_lambda", "aws_s3", "aws_bedrock", "aws_bedrock_agent",
		"aws_agentcore_gateway", "aws_kendra", "aws_sagemaker", "aws_opensearch",
	} {
		assert.Contains(t, mainTF, `module "`+mod+`"`,
			"composed AI stack must declare module %q", mod)
	}

	// Headline wiring: the agent's action-group executor is the lambda.
	assert.True(t, wiresAttr(mainTF, "action_group_lambda_arn", "module.aws_lambda.function_arn"),
		"agent action_group_lambda_arn must wire from module.aws_lambda.function_arn")

	// Headline wiring: the agent does RAG against the bedrock KB.
	assert.True(t, wiresAttr(mainTF, "knowledge_base_id", "module.aws_bedrock.knowledge_base_id"),
		"agent knowledge_base_id must wire from module.aws_bedrock.knowledge_base_id")

	// Compose ordering: bedrock (KB output) must precede the agent that
	// references it. Require both present first so a missing module (Index ==
	// -1) can't make the ordering check pass spuriously.
	bedrockPos := strings.Index(mainTF, `module "aws_bedrock"`)
	agentPos := strings.Index(mainTF, `module "aws_bedrock_agent"`)
	require.NotEqual(t, -1, bedrockPos, "aws_bedrock module must be present for the ordering check")
	require.NotEqual(t, -1, agentPos, "aws_bedrock_agent module must be present for the ordering check")
	assert.Less(t, bedrockPos, agentPos,
		"aws_bedrock must compose before aws_bedrock_agent so its KB output is available")

	// Plan-time-known count gates. Each AI module gates its optional resources
	// (S3 data source, Lambda target, action group, KB association) on a
	// boolean. The wired inputs they used to gate on (s3_bucket_name,
	// target_lambda_arn, action_group_lambda_arn, knowledge_base_id) are
	// COMPUTED module outputs, so `<input> != null` is unknown at plan and
	// Terraform rejects it as a count argument ("Invalid count argument"). The
	// composer must therefore emit an explicit plan-time-known enable_* flag at
	// each wire. Missing any of these reintroduces the apply-time break that the
	// per-component tests and `terraform validate` cannot see — only a composed
	// `terraform plan` does. These string asserts are the hermetic stand-in for
	// that plan (which needs live credentials).
	assert.True(t, wiresAttr(mainTF, "enable_action_group", "true"),
		"agent action group must be gated on a plan-time-known enable_action_group=true, not on the computed action_group_lambda_arn")
	assert.True(t, wiresAttr(mainTF, "enable_lambda_target", "true"),
		"agentcore lambda target must be gated on a plan-time-known enable_lambda_target=true, not on the computed target_lambda_arn")
	assert.True(t, wiresAttr(mainTF, "enable_s3_data_source", "true"),
		"kendra S3 data source must be gated on a plan-time-known enable_s3_data_source=true, not on the computed s3_bucket_name")
	assert.True(t, wiresAttr(mainTF, "enable_knowledge_base_association", "module.aws_bedrock.knowledge_base_enabled"),
		"agent KB association must be gated on the bedrock module's plan-time-known knowledge_base_enabled output, not on the computed knowledge_base_id")

	// No fabricated preview values may leak into a composed stack.
	assert.NotContains(t, mainTF, "composerpreview",
		"no fabricated preview value may leak into the composed AI stack")
}

// TestComposeAWSAIStack_InitValidate writes the composed bundle to a tempdir and
// runs `terraform init -backend=false` + `terraform validate` against the real
// provider schema. This is the composed-bundle validity gate the per-component
// tests don't provide. Hermetic — needs NO AWS credentials (validate resolves
// the graph and types without contacting AWS). Skipped under -short or when the
// terraform binary is absent so the core suite stays fast/hermetic.
func TestComposeAWSAIStack_InitValidate(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips terraform init+validate of the composed AI stack")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform binary not on PATH: %v", err)
	}

	out := composeAIStack(t, defaultSageMakerImage)
	dir := t.TempDir()
	writeOutputs(t, out, dir)

	initCmd := exec.Command(tfBin, "init", "-backend=false", "-input=false", "-no-color")
	initCmd.Dir = dir
	if o, err := initCmd.CombinedOutput(); err != nil {
		// init reaches the registry; tolerate offline/network flakiness in
		// local dev but fail hard in CI where the network is available.
		if os.Getenv("CI") == "true" {
			require.NoError(t, err, "terraform init must succeed in CI:\n%s", o)
		}
		t.Skipf("terraform init unavailable (network/registry) in local dev: %v\n%s", err, o)
	}

	validateCmd := exec.Command(tfBin, "validate", "-no-color")
	validateCmd.Dir = dir
	o, err := validateCmd.CombinedOutput()
	require.NoError(t, err,
		"terraform validate must pass on the composed full AWS AI stack:\n%s", o)
}

// TestComposeAWSAIStack_ApplyDestroy is the live end-to-end gate: compose →
// init → plan → apply → confirm resources exist → destroy. It stands up REAL,
// billable AWS resources and is therefore opt-in: set AI_STACK_APPLY=1 with
// valid credentials for the target account on PATH.
//
//	AI_STACK_APPLY=1 AWS_REGION=us-east-1 \
//	  go test -v -timeout 90m -run TestComposeAWSAIStack_ApplyDestroy ./pkg/composer/
//
// Account prerequisites (cannot be set via Terraform):
//   - Bedrock model access enabled in the region for the Anthropic Claude
//     family (agent foundation model + bedrock base model) AND Amazon Titan
//     Text Embeddings V2 (the Knowledge Base embedding model).
//   - SageMaker servable image reachable from the account (default is the
//     public AWS HuggingFace DLC; override with AI_STACK_SAGEMAKER_IMAGE).
//
// The bundle is written under .tmp/ (gitignored), NOT t.TempDir(), so its
// state file survives an interrupted run and the stack can be torn down
// out-of-band. Destroy is registered via t.Cleanup so it runs even when an
// assertion fails mid-apply.
func TestComposeAWSAIStack_ApplyDestroy(t *testing.T) {
	if os.Getenv("AI_STACK_APPLY") != "1" {
		t.Skip("set AI_STACK_APPLY=1 (with live AWS creds) to run the real apply/destroy gate")
	}
	tfBin, err := exec.LookPath("terraform")
	require.NoError(t, err, "terraform binary required for the apply gate")

	modelImage := defaultSageMakerImage
	if v := strings.TrimSpace(os.Getenv("AI_STACK_SAGEMAKER_IMAGE")); v != "" {
		modelImage = v
	}

	out := composeAIStack(t, modelImage)

	// Stable, gitignored working dir so state persists across an interrupt.
	repoRoot, err := repoRootDir()
	require.NoError(t, err)
	dir := filepath.Join(repoRoot, ".tmp", "ai-stack-apply")

	// Refuse to clobber a dir that still holds non-empty state: that would be
	// a prior run that was interrupted (signal kills the t.Cleanup destroy)
	// leaving real, billable resources tracked only by this state file.
	// Deleting it strands them with no way to `terraform destroy`. Surface it
	// so the operator tears the old stack down first.
	if st, serr := os.Stat(filepath.Join(dir, "terraform.tfstate")); serr == nil && st.Size() > 0 {
		t.Fatalf("refusing to overwrite %s: a non-empty terraform.tfstate from a prior run exists — "+
			"`terraform destroy` it (or remove the dir) before re-running so live resources are not orphaned", dir)
	}
	require.NoError(t, os.RemoveAll(dir), "clean prior apply dir")
	writeOutputs(t, out, dir)
	t.Logf("composed AI stack written to %s", dir)

	// run streams terraform output to stdout in real time (so the multi-minute
	// AOSS / SageMaker-endpoint / Kendra-index waits aren't a silent black box
	// under `go test -v`) while also capturing it for assertions.
	run := func(name string, args ...string) ([]byte, error) {
		t.Logf("→ terraform %s", name)
		var buf bytes.Buffer
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		cmd.Stdout = io.MultiWriter(&buf, os.Stdout)
		cmd.Stderr = io.MultiWriter(&buf, os.Stderr)
		err := cmd.Run()
		return buf.Bytes(), err
	}

	// applyReached gates the cleanup's failure severity: only an apply that was
	// actually started can leave resources behind. If init/plan failed first,
	// nothing was created, so a destroy error there is informational noise
	// (e.g. providers never installed) rather than an orphan alarm.
	applyReached := false

	// Always attempt teardown, even if apply fails partway.
	t.Cleanup(func() {
		o, derr := run("destroy", "destroy", "-auto-approve", "-input=false", "-no-color")
		switch {
		case derr != nil && applyReached:
			t.Errorf("terraform destroy FAILED — resources may be orphaned in %s; destroy manually:\n%s", dir, o)
		case derr != nil:
			t.Logf("terraform destroy errored but apply never ran (nothing to orphan):\n%s", o)
		}
	})

	o, err := run("init", "init", "-input=false", "-no-color")
	require.NoError(t, err, "terraform init must succeed:\n%s", o)

	o, err = run("plan", "plan", "-input=false", "-no-color")
	require.NoError(t, err, "terraform plan must succeed:\n%s", o)

	applyReached = true
	o, err = run("apply", "apply", "-auto-approve", "-input=false", "-no-color")
	require.NoError(t, err, "terraform apply must stand up the full AI stack:\n%s", o)

	// Confirm resources for every one of the nine modules actually exist in
	// state — a partial apply that AWS accepted for only some modules would
	// otherwise slip through.
	o, err = run("state-list", "state", "list")
	require.NoError(t, err, "terraform state list must succeed after apply:\n%s", o)
	state := string(o)
	for _, want := range []string{
		"module.aws_vpc.",
		"module.aws_lambda.",
		"module.aws_s3.",
		"module.aws_bedrock.",
		"module.aws_bedrock_agent.",
		"module.aws_agentcore_gateway.",
		"module.aws_kendra.",
		"module.aws_sagemaker.",
		"module.aws_opensearch.",
	} {
		assert.Contains(t, state, want,
			"applied state must contain %s", want)
	}
}

// repoRootDir walks up from the test's working directory to the module root
// (the dir containing go.mod) so the apply gate writes to a stable repo-local
// .tmp/ regardless of where `go test` is invoked.
func repoRootDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return os.Getwd()
		}
		dir = parent
	}
}
