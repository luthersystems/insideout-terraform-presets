# aws/agentcore_gateway — Bedrock AgentCore MCP/tool gateway (#763, part of #755)
#
# ── MATURITY CAVEAT (ADVANCED component, maturity-flagged) ────────────────────
# Bedrock AgentCore is a NEW AWS service surface. The hashicorp/aws provider's
# aws_bedrockagentcore_* resource family landed ~v6.18 and its schema CHURNS
# across minor releases (attributes added/renamed/reshaped). This preset is
# pinned to the shape of the family in the provider version this repo resolves
# (aws = 6.45.0, see schemas/providers.tf) and is deliberately conservative:
# it provisions ONLY the gateway + a single Lambda target + the IAM role the
# gateway assumes to invoke that target. AgentCore *runtime* (which requires a
# customer-supplied ECR image), memory, and credential-provider resources are
# OUT OF SCOPE for this component — gateway only, per #763. There is no GCP
# analog; #756 documents the AWS-only asymmetry. Treat this as an advanced
# card: expect to re-pin attribute shapes when the provider is bumped.
#
# What this builds:
#   • aws_bedrockagentcore_gateway        — the MCP gateway endpoint, with
#       inbound auth (a custom JWT authorizer, e.g. Cognito/Auth0/OIDC) and the
#       MCP protocol configuration. Always created.
#   • aws_iam_role (gateway)              — the role the gateway assumes to
#       invoke its targets (e.g. the Lambda). Always created (the gateway
#       requires role_arn), so this preset always emits infrastructure even in
#       its minimal shape.
#   • aws_bedrockagentcore_gateway_target — a Lambda target turning the wired
#       Lambda into an agent-callable tool. Created only when a Lambda ARN is
#       wired in (count-gated); in a composed stack DefaultWiring supplies it
#       from module.aws_lambda.function_arn (KeyAWSLambda is a HARD implicit
#       dependency of this component).

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
  subcomponent   = "acgw"
  resource       = "acgw"
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}
data "aws_partition" "current" {}

locals {
  gateway_name = var.gateway_name == null ? "${var.project}-gateway" : var.gateway_name

  # The Lambda target (and the gateway's permission to invoke it) only exist
  # when a backing Lambda ARN is wired in. A gateway with OpenAPI/REST targets
  # supplied out-of-band, or one stood up before its tools, leaves this off.
  has_lambda_target = var.target_lambda_arn != null
}

# --- Gateway IAM role ---------------------------------------------------------
#
# The role AgentCore assumes to invoke this gateway's targets. The gateway
# requires role_arn, so this is always created and keeps the preset producing
# infrastructure even in its minimal (no-target) shape. Trust is scoped to the
# bedrock-agentcore service principal, and the confused-deputy holes are closed
# by pinning aws:SourceAccount to this account and aws:SourceArn to this
# account+region's gateway ARN namespace (mirrors aws/bedrock_agent's role).
resource "aws_iam_role" "gateway" {
  name = "${var.project}-agentcore-gw-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "bedrock-agentcore.amazonaws.com"
        }
        Condition = {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
          ArnLike = {
            # Derive the partition from data.aws_partition so the SourceArn
            # condition matches in GovCloud (aws-us-gov) / China (aws-cn), not
            # just commercial aws — target_lambda_arn already admits those
            # partitions, so the trust guard must too or the gateway role is
            # unassumable there.
            "aws:SourceArn" = "arn:${data.aws_partition.current.partition}:bedrock-agentcore:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:gateway/*"
          }
        }
      }
    ]
  })

  tags = merge(module.name.tags, var.tags)

  # aws_iam_role_policy below writes an inline policy onto the role; the
  # provider re-reads it onto the role on refresh and drift-check flags it.
  # Ignore here (mirrors aws/bedrock_agent's role).
  lifecycle {
    ignore_changes = [inline_policy]
  }
}

# The gateway role's target-invocation policy. lambda:InvokeFunction on the
# wired target Lambda is the only grant; it exists only when a Lambda target is
# present (aws_iam_role_policy requires a non-empty Statement list, so a
# gateway with no Lambda target carries no inline policy). Scoped to the wired
# Lambda ARN — the gateway can invoke ONLY its own target, not any function.
resource "aws_iam_role_policy" "gateway_invoke" {
  count = local.has_lambda_target ? 1 : 0

  name = "${var.project}-agentcore-gw-invoke"
  role = aws_iam_role.gateway.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "InvokeTargetLambda"
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = [var.target_lambda_arn]
      }
    ]
  })
}

# --- Gateway ------------------------------------------------------------------
#
# The MCP gateway endpoint. authorizer_type = CUSTOM_JWT + the custom_jwt
# authorizer block configures INBOUND auth: callers (agents/MCP clients) must
# present a JWT from the configured OIDC issuer (Cognito user-pool, Auth0, etc.)
# whose discovery_url is supplied. protocol_type = MCP with the mcp protocol
# block makes this an MCP tool gateway. The gateway URL (computed) is the
# endpoint agents connect to.
resource "aws_bedrockagentcore_gateway" "this" {
  name            = local.gateway_name
  role_arn        = aws_iam_role.gateway.arn
  protocol_type   = var.protocol_type
  authorizer_type = "CUSTOM_JWT"
  description     = "Insideout AgentCore MCP/tool gateway for ${var.project}."

  authorizer_configuration {
    custom_jwt_authorizer {
      discovery_url = var.jwt_discovery_url

      # Audience / client allowlists are optional on the provider; emit them
      # only when the caller supplied a non-empty list so an unconfigured
      # gateway does not pin an empty allowlist.
      allowed_audience = length(var.jwt_allowed_audience) > 0 ? var.jwt_allowed_audience : null
      allowed_clients  = length(var.jwt_allowed_clients) > 0 ? var.jwt_allowed_clients : null
    }
  }

  protocol_configuration {
    mcp {
      instructions       = var.mcp_instructions
      search_type        = var.mcp_search_type
      supported_versions = length(var.mcp_supported_versions) > 0 ? var.mcp_supported_versions : null
    }
  }

  tags = merge(module.name.tags, var.tags)

  depends_on = [aws_iam_role.gateway]
}

# --- Lambda target ------------------------------------------------------------
#
# Turns the wired Lambda into an MCP tool exposed by the gateway. Uses the
# gateway's own IAM role for credentials (gateway_iam_role {} — the empty block
# selects "use the gateway's execution role" rather than an API-key/OAuth
# credential provider). The tool's input schema is declared inline so no
# external OpenAPI payload is required. Created only when a Lambda ARN is wired
# in; in a composed stack DefaultWiring supplies target_lambda_arn from
# module.aws_lambda.function_arn (KeyAWSLambda is a HARD implicit dependency).
resource "aws_bedrockagentcore_gateway_target" "lambda" {
  count = local.has_lambda_target ? 1 : 0

  gateway_identifier = aws_bedrockagentcore_gateway.this.gateway_id
  name               = "${var.project}-lambda-tool"
  description        = "Lambda-backed MCP tool for ${local.gateway_name}."

  credential_provider_configuration {
    # Empty block: the target is invoked with the gateway's execution role
    # (aws_iam_role.gateway), which carries lambda:InvokeFunction on the
    # wired Lambda via aws_iam_role_policy.gateway_invoke above.
    gateway_iam_role {}
  }

  target_configuration {
    mcp {
      lambda {
        lambda_arn = var.target_lambda_arn

        tool_schema {
          inline_payload {
            name        = "invoke"
            description = "Invoke the backing Lambda to perform an action on behalf of the agent."

            input_schema {
              type        = "object"
              description = "Free-form tool input forwarded to the Lambda."

              property {
                name        = "input"
                type        = "string"
                description = "The input payload forwarded to the Lambda."
                required    = false
              }
            }
          }
        }
      }
    }
  }

  # The gateway role's invoke policy must exist before the target binds the
  # Lambda, so the gateway can actually call the function once the target is
  # live. Safe when the policy is absent (count=0) — depends_on on an empty
  # resource list is a no-op (but here both are count-gated identically).
  depends_on = [aws_iam_role_policy.gateway_invoke]
}

# Allow the AgentCore gateway service to invoke the target Lambda. Scoped to
# the gateway ARN so only THIS gateway (not any AgentCore caller) may invoke
# the function. Created alongside the Lambda target.
resource "aws_lambda_permission" "gateway_invoke" {
  count = local.has_lambda_target ? 1 : 0

  statement_id  = "AllowAgentCoreGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = var.target_lambda_arn
  principal     = "bedrock-agentcore.amazonaws.com"
  source_arn    = aws_bedrockagentcore_gateway.this.gateway_arn
}
