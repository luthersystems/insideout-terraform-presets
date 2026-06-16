terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "bra"
  resource       = "bra"
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  agent_name        = var.agent_name == null ? "${var.project}-agent" : var.agent_name
  action_group_name = var.action_group_name == null ? "actions" : var.action_group_name

  # The action group / Lambda invoke permission only exist when a backing
  # Lambda is wired in. A KB-only or chat-only agent leaves these off.
  #
  # The enable_* toggles are the plan-time-known gates. Composed stacks wire
  # action_group_lambda_arn / knowledge_base_id from other modules' outputs,
  # whose values are unknown at plan, so `var.x != null` is itself unknown and
  # Terraform rejects it as a count argument. The composer sets the toggles
  # explicitly (enable_knowledge_base_association from the bedrock module's
  # plan-time-known knowledge_base_enabled output); standalone callers leave
  # them null and fall back to auto-detecting from the (literal) inputs.
  has_action_group = var.enable_action_group != null ? var.enable_action_group : (var.action_group_lambda_arn != null)
  # The KB association only exists when a Knowledge Base id is wired in.
  has_knowledge_base = var.enable_knowledge_base_association != null ? var.enable_knowledge_base_association : (var.knowledge_base_id != null)
}

# --- Agent resource IAM role --------------------------------------------------
#
# Bedrock assumes this role to run the agent: invoke the foundation model and,
# when a Knowledge Base is associated, retrieve from it. Always created — it is
# a required input to the agent resource below and keeps the preset producing
# infrastructure even in its minimal (chat-only) shape.
resource "aws_iam_role" "agent" {
  name = "${var.project}-bedrock-agent-role"

  # Scope the bedrock.amazonaws.com service trust to this account
  # (aws:SourceAccount) to close the cross-account confused-deputy hole, and to
  # the agent ARN namespace (aws:SourceArn) so only Bedrock agents in this
  # account+region can assume the role. Mirrors the aws/bedrock KB role.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "bedrock.amazonaws.com"
        }
        Condition = {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
          ArnLike = {
            "aws:SourceArn" = "arn:aws:bedrock:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:agent/*"
          }
        }
      }
    ]
  })

  tags = merge(module.name.tags, var.tags)

  # The inline policy is attached via aws_iam_role_policy below; the provider
  # re-reads it onto the role on refresh and drift-check flags it. Ignore here.
  # (managed_policy_arns is deprecated in AWS provider 6.x and unused here.)
  lifecycle {
    ignore_changes = [inline_policy]
  }
}

# The agent role policy: InvokeModel on the agent's foundation model is always
# present (the whole point of the role and keeps the Statement list non-empty,
# which aws_iam_role_policy requires). bedrock:Retrieve is appended only when a
# Knowledge Base is associated — it has no purpose for a chat-only agent.
locals {
  agent_invoke_statement = {
    Sid    = "InvokeFoundationModel"
    Effect = "Allow"
    Action = [
      "bedrock:InvokeModel",
      "bedrock:InvokeModelWithResponseStream",
    ]
    # The agent may resolve its model through an inference profile, which fans
    # out to the underlying foundation models — grant both the model ARN and
    # the inference-profile ARN namespace so either resolution path works.
    Resource = [
      "arn:aws:bedrock:${data.aws_region.current.region}::foundation-model/*",
      "arn:aws:bedrock:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:inference-profile/*",
    ]
  }

  agent_retrieve_statements = local.has_knowledge_base ? [
    {
      Sid    = "RetrieveFromKnowledgeBase"
      Effect = "Allow"
      Action = [
        "bedrock:Retrieve",
        "bedrock:RetrieveAndGenerate",
      ]
      Resource = [
        "arn:aws:bedrock:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:knowledge-base/${var.knowledge_base_id}",
      ]
    }
  ] : []
}

resource "aws_iam_role_policy" "agent" {
  name = "${var.project}-bedrock-agent-policy"
  role = aws_iam_role.agent.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [local.agent_invoke_statement],
      local.agent_retrieve_statements,
    )
  })
}

# --- Agent --------------------------------------------------------------------
#
# prepare_agent = true builds the DRAFT version into a PREPARED state, so a
# chat-only agent (no action group / KB) is immediately invokable via its
# alias. The agent CANNOT depend_on its own action group / KB association — they
# reference the agent (agent_id), so that would be a cycle. Instead, the action
# group and KB association each re-run prepare after they write into the DRAFT
# (prepare_agent = true on those resources), and the alias depends_on all of
# them so it only ever binds a PREPARED version that already includes the tools.
resource "aws_bedrockagent_agent" "this" {
  agent_name                  = local.agent_name
  agent_resource_role_arn     = aws_iam_role.agent.arn
  foundation_model            = var.foundation_model
  instruction                 = var.instruction
  idle_session_ttl_in_seconds = var.idle_session_ttl_in_seconds
  prepare_agent               = true

  # A prepared agent that still has a live alias / action group bound can hit
  # the same ConflictException as the action group on delete. Skip the in-use
  # check so destroy tears the agent down cleanly alongside its dependents.
  skip_resource_in_use_check = true

  tags = merge(module.name.tags, var.tags)

  depends_on = [aws_iam_role_policy.agent]
}

# --- Knowledge Base association -----------------------------------------------
#
# Associates an existing Bedrock Knowledge Base with the agent's DRAFT version
# so the agent can retrieve from it (RAG). Only created when a KB id is wired
# in. This resource has no prepare_agent flag of its own; the action group's
# prepare (below, ordered after this via depends_on) re-prepares the agent so
# the alias picks up the KB. In the composed product aws/lambda is a HARD
# implicit dependency of aws_bedrock_agent, so an action group is always
# present to drive that re-prepare. (Standalone caveat: a KB-only agent with no
# action_group_lambda_arn relies on the agent's initial prepare, which runs
# before this association is written — the alias picks up the KB on the next
# apply. Supply an action group, or re-apply once, to fold the KB in
# immediately.)
resource "aws_bedrockagent_agent_knowledge_base_association" "this" {
  count = local.has_knowledge_base ? 1 : 0

  agent_id             = aws_bedrockagent_agent.this.agent_id
  knowledge_base_id    = var.knowledge_base_id
  knowledge_base_state = "ENABLED"
  description          = var.knowledge_base_instruction
}

# --- Action group -------------------------------------------------------------
#
# Attaches a Lambda executor to the agent's DRAFT version. function_schema
# declares the callable tool inline (no external OpenAPI payload / S3 object
# required). prepare_agent = true re-prepares the agent after the action group
# is written into the DRAFT, so the alias (which depends_on this resource) only
# ever binds a PREPARED version that already includes the tools. depends_on the
# KB association so this prepare is the LAST DRAFT mutation in the apply — the
# prepared snapshot then reflects BOTH the action group and the KB regardless of
# the order Terraform applies the two independent resources.
resource "aws_bedrockagent_agent_action_group" "this" {
  count = local.has_action_group ? 1 : 0

  agent_id          = aws_bedrockagent_agent.this.agent_id
  agent_version     = "DRAFT"
  action_group_name = local.action_group_name
  description       = "Lambda-backed tools for ${local.agent_name}."
  prepare_agent     = true

  # An ENABLED action group attached to a prepared agent cannot be deleted —
  # Bedrock returns ConflictException ("ActionGroup ... cannot be deleted when
  # it is Enabled") on DeleteAgentActionGroup. Skip the in-use check so destroy
  # (and any replace) removes it cleanly without a manual disable step.
  skip_resource_in_use_check = true

  action_group_executor {
    lambda = var.action_group_lambda_arn
  }

  function_schema {
    member_functions {
      functions {
        name        = "invoke"
        description = "Invoke the backing Lambda to perform an action on behalf of the user."
        parameters {
          map_block_key = "input"
          type          = "string"
          description   = "Free-form input forwarded to the Lambda."
          required      = false
        }
      }
    }
  }

  # Order the action group's prepare after the KB association so the prepared
  # snapshot includes the KB. Safe when the KB association is absent (count=0):
  # depends_on on an empty resource list is a no-op.
  depends_on = [aws_bedrockagent_agent_knowledge_base_association.this]
}

# Allow the Bedrock agent service to invoke the action-group Lambda. Scoped to
# the agent ARN so only this agent (not any Bedrock caller) may invoke it.
resource "aws_lambda_permission" "agent_invoke" {
  count = local.has_action_group ? 1 : 0

  statement_id  = "AllowBedrockAgentInvoke"
  action        = "lambda:InvokeFunction"
  function_name = var.action_group_lambda_arn
  principal     = "bedrock.amazonaws.com"
  source_arn    = aws_bedrockagent_agent.this.agent_arn
}

# --- Alias --------------------------------------------------------------------
#
# A stable, invokable handle for the agent. depends_on the agent (which always
# prepares itself) plus the action group + KB association guarantees the alias
# NEVER binds a NOT_PREPARED agent and is created only AFTER every DRAFT
# mutation + the re-prepare that follows them — the documented failure mode this
# preset exists to encapsulate.
resource "aws_bedrockagent_agent_alias" "this" {
  agent_id         = aws_bedrockagent_agent.this.agent_id
  agent_alias_name = "${var.project}-live"
  description      = "Live alias for ${local.agent_name}, always bound to the latest PREPARED version."

  tags = merge(module.name.tags, var.tags)

  depends_on = [
    aws_bedrockagent_agent.this,
    aws_bedrockagent_agent_action_group.this,
    aws_bedrockagent_agent_knowledge_base_association.this,
  ]
}
