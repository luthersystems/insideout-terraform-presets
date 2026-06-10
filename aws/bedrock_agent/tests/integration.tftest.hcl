# Integration tests for the bedrock_agent module — APPLIES AGAINST A REAL AWS ACCOUNT.
#
# Run with valid AWS credentials in the target account:
#
#   cd aws/bedrock_agent
#   terraform init -backend=false
#   AWS_REGION=us-east-1 terraform test -filter=tests/integration.tftest.hcl
#
# What it does:
#   1. setup_action_lambda: stands up a real Lambda via the sibling ../lambda
#      preset (bundled placeholder.zip, no VPC) to back the agent's action
#      group. Yields a concrete function_arn for the executor.
#   2. bedrock_agent_apply: applies bedrock_agent with the action-group Lambda
#      wired in, asserting the agent id/arn, the live alias id, the action
#      group + its Lambda executor, the Bedrock->Lambda invoke permission, and
#      the agent role + trust-policy confused-deputy guard all come up. This is
#      the live-apply proof that the PREPARED-before-alias ordering holds: the
#      action group writes the tool into the DRAFT and re-prepares, and the
#      alias (depends_on the action group) only ever binds the PREPARED version.
#      If the ordering were wrong, the alias create would fail against a
#      NOT_PREPARED agent and this apply would error.
#   3. Tears EVERYTHING down at the end (terraform test always destroys).
#
# No Knowledge Base run here on purpose — the KB suite (aws/bedrock) is the
# RAG live-apply proof and is exercised separately. This test stays
# self-contained and fast: agent + action group + alias only.
#
# Model: anthropic.claude-3-haiku-20240307-v1:0 — the cheapest authorized
# Anthropic text model in this account/region (verified with
# `aws bedrock get-foundation-model-availability`). If agent creation ever
# fails with a model-access / on-demand-throughput error, switch
# foundation_model to the cross-region inference profile form
# "us.anthropic.claude-3-haiku-20240307-v1:0" (the preset role already grants
# the inference-profile ARN namespace).
#
# Resource names embed a UTC timestamp suffix (MMDDhhmmss) under the iotagt-
# prefix so this never collides with the concurrent aws/bedrock KB
# integration run (iotbed- prefix) or with a prior iotagt- run. This test does
# NOT touch the aws_bedrock_model_invocation_logging_configuration singleton.
#
# If a prior run was killed mid-apply, manual cleanup may be required:
#
#   aws bedrock-agent list-agents --query 'agentSummaries[?starts_with(agentName, `iotagt-`)]'
#   aws bedrock-agent delete-agent --agent-id <id> --skip-resource-in-use-check
#   aws lambda list-functions --query 'Functions[?starts_with(FunctionName, `iotagt-`)]'
#   aws lambda delete-function --function-name <name>
#   aws iam list-roles --query 'Roles[?starts_with(RoleName, `iotagt-`)]'
#   # detach/delete inline + managed policies, then:
#   aws iam delete-role --role-name <name>

provider "aws" {
  region = var.region
}

variables {
  # Suffix the project name with a UTC MMDDhhmmss timestamp so concurrent or
  # back-to-back runs don't collide on agent / Lambda / role names. Computed
  # once at file-load time and shared across every run in this file, so
  # setup_action_lambda and bedrock_agent_apply see the same project string.
  # Risk: two runs in the exact same second collide. Acceptable for a
  # human-driven integration test.
  project     = "iotagt-${formatdate("MMDDhhmmss", timestamp())}"
  region      = "us-east-1"
  environment = "test"

  # Cheapest authorized Anthropic text model in this account/region. See the
  # header note for the inference-profile fallback if model access changes.
  foundation_model = "anthropic.claude-3-haiku-20240307-v1:0"
}

# --- Setup: real action-group Lambda --------------------------------------
#
# Uses the sibling lambda preset (bundled placeholder.zip, no VPC) so the
# action group has a concrete executor ARN. Path is resolved relative to the
# module under test (aws/bedrock_agent), so we go up one level and across.
run "setup_action_lambda" {
  command = apply

  module {
    source = "../lambda"
  }

  variables {
    project     = var.project
    environment = var.environment
    region      = var.region
  }

  assert {
    condition     = output.function_arn != null && length(output.function_arn) > 0
    error_message = "Action-group Lambda setup must yield a function ARN."
  }
}

# --- Main: bedrock_agent against the real Lambda --------------------------
#
# The live-apply proof for the PREPARED-before-alias ordering. With the
# action-group Lambda wired in, the apply must build: the agent (prepares its
# DRAFT), the action group (writes the tool into the DRAFT and re-prepares),
# the Bedrock->Lambda invoke permission, and the alias (binds the PREPARED
# version that now includes the tool). A wrong ordering — alias created before
# the agent reaches PREPARED — fails the alias create here.
run "bedrock_agent_apply" {
  command = apply

  variables {
    action_group_lambda_arn = run.setup_action_lambda.function_arn
  }

  # --- Agent up with id + arn ---
  assert {
    condition     = aws_bedrockagent_agent.this.agent_name == "${var.project}-agent"
    error_message = "Agent name must default to {project}-agent."
  }

  assert {
    condition     = length(aws_bedrockagent_agent.this.agent_id) > 0
    error_message = "Live agent must report a non-empty agent_id after apply."
  }

  assert {
    condition     = can(regex("^arn:aws:bedrock:", aws_bedrockagent_agent.this.agent_arn))
    error_message = "Live agent must report a Bedrock agent ARN after apply."
  }

  # The agent must reach PREPARED so the alias can bind it. The resource has no
  # agent_status attribute, but prepared_at is the computed timestamp Bedrock
  # only sets once the DRAFT has successfully prepared — a non-null value is the
  # proof the prepare build succeeded. This is the headline of the live test
  # (mocks can't reach this state).
  assert {
    condition     = aws_bedrockagent_agent.this.prepare_agent == true
    error_message = "Agent must set prepare_agent = true so its DRAFT reaches PREPARED."
  }

  assert {
    condition     = aws_bedrockagent_agent.this.prepared_at != null && length(aws_bedrockagent_agent.this.prepared_at) > 0
    error_message = "Live agent must report a non-null prepared_at after apply (the prepare_agent build must succeed)."
  }

  # --- Agent role + confused-deputy trust guard ---
  assert {
    condition     = aws_iam_role.agent.name == "${var.project}-bedrock-agent-role"
    error_message = "Agent resource IAM role must be named {project}-bedrock-agent-role."
  }

  assert {
    condition     = jsondecode(aws_iam_role.agent.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == data.aws_caller_identity.current.account_id
    error_message = "Agent trust policy must scope aws:SourceAccount to the deploying account."
  }

  # --- Action group attached to the DRAFT, pointed at the live Lambda ---
  assert {
    condition     = length(aws_bedrockagent_agent_action_group.this) == 1
    error_message = "Action group must be created when action_group_lambda_arn is wired in."
  }

  assert {
    condition     = aws_bedrockagent_agent_action_group.this[0].agent_version == "DRAFT"
    error_message = "Action group must attach to the DRAFT version."
  }

  assert {
    condition     = aws_bedrockagent_agent_action_group.this[0].action_group_executor[0].lambda == run.setup_action_lambda.function_arn
    error_message = "Action group executor must point at the live setup Lambda ARN."
  }

  assert {
    condition     = length(output.action_group_id) > 0
    error_message = "action_group_id output must be populated after a live action-group apply."
  }

  # --- Bedrock -> Lambda invoke permission, scoped to this agent ---
  assert {
    condition     = length(aws_lambda_permission.agent_invoke) == 1
    error_message = "Lambda invoke permission must be created alongside the action group."
  }

  assert {
    condition     = aws_lambda_permission.agent_invoke[0].principal == "bedrock.amazonaws.com"
    error_message = "Lambda invoke permission must grant bedrock.amazonaws.com."
  }

  assert {
    condition     = aws_lambda_permission.agent_invoke[0].source_arn == aws_bedrockagent_agent.this.agent_arn
    error_message = "Lambda invoke permission must be scoped to this agent's ARN (only this agent may invoke)."
  }

  # --- Live alias bound to the PREPARED version ---
  # No KB association in this test, so the alias depends only on the agent +
  # action group. Its creation succeeding is the proof the ordering held.
  assert {
    condition     = aws_bedrockagent_agent_alias.this.agent_alias_name == "${var.project}-live"
    error_message = "A live alias must be created with the {project}-live name."
  }

  assert {
    condition     = length(output.agent_alias_id) > 0
    error_message = "agent_alias_id output must be populated — the alias bound a PREPARED agent version."
  }
}
