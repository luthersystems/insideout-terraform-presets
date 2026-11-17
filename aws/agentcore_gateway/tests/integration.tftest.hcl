# Integration tests for the agentcore_gateway module — APPLIES AGAINST A REAL
# AWS ACCOUNT.
#
# This suite is opt-in. CI never runs it: terraform-validate.yml skips every
# *integration* .tftest.hcl file, because these need live AWS credentials. The
# credential-free tests/shape.tftest.hcl is what CI runs.
#
# Run with valid AWS credentials in the target account:
#
#   cd aws/agentcore_gateway
#   terraform init -backend=false
#   AWS_REGION=us-east-1 terraform test -filter=tests/integration.tftest.hcl
#
# Account access needed: the account must have access to Amazon Bedrock
# AgentCore in the target region (us-east-1 below), plus permission to create
# AgentCore gateways and gateway targets, IAM roles and inline policies,
# Lambda functions and permissions, and Cognito user pools. Without AgentCore
# access the gateway create fails with AccessDenied; that is an account setup
# problem, not a preset bug.
#
# Time and cost: about 5 minutes end to end. Nothing here bills by the hour;
# an idle gateway, a Lambda that is never invoked and an empty user pool cost
# effectively nothing for the length of a run.
#
# Why a live suite at all: the AgentCore provider resources are new and their
# schema changes between provider minors (see main.tf's maturity caveat). The
# shape suite only proves the plan; this suite proves the real API still
# accepts what the preset sends after a provider bump.
#
# What it does:
#   1. setup_issuer: stands up a real Cognito user pool and app client via the
#      sibling ../cognito preset. AgentCore fetches the authorizer's OIDC
#      discovery document on CreateGateway, so the gateway needs a real issuer,
#      not the placeholder default.
#   2. setup_target_lambda: stands up a real Lambda via the sibling ../lambda
#      preset (bundled placeholder.zip, no VPC) to back the gateway's tool.
#   3. agentcore_gateway_apply: applies agentcore_gateway with the issuer and
#      Lambda wired in, asserting the gateway id/arn/url, the gateway role and
#      its confused-deputy trust guard, the role's invoke policy scoped to the
#      Lambda, the Lambda target, and the AgentCore->Lambda invoke permission
#      scoped to this gateway all come up.
#   4. Tears EVERYTHING down at the end (terraform test always destroys).
#
# Resource names embed a UTC timestamp suffix (MMDDhhmmss) under the iotacg-
# prefix so back-to-back or concurrent runs do not collide.
#
# If a prior run was killed mid-apply, manual cleanup may be required:
#
#   aws bedrock-agentcore-control list-gateways --query 'items[?starts_with(name, `iotacg-`)]'
#   aws bedrock-agentcore-control list-gateway-targets --gateway-identifier <id>
#   aws bedrock-agentcore-control delete-gateway-target --gateway-identifier <id> --target-id <target>
#   aws bedrock-agentcore-control delete-gateway --gateway-identifier <id>
#   aws lambda list-functions --query 'Functions[?starts_with(FunctionName, `iotacg-`)]'
#   aws lambda delete-function --function-name <name>
#   aws cognito-idp list-user-pools --max-results 60 --query 'UserPools[?starts_with(Name, `iotacg-`)]'
#   aws cognito-idp delete-user-pool --user-pool-id <id>
#   aws iam list-roles --query 'Roles[?starts_with(RoleName, `iotacg-`)]'
#   # delete the inline policies, then:
#   aws iam delete-role --role-name <name>

provider "aws" {
  region = var.region
}

variables {
  # Suffix the project name with a UTC MMDDhhmmss timestamp so runs do not
  # collide. Computed once at file-load time and shared by every run in this
  # file, so the setup runs and the main run see the same project string.
  # Two runs in the exact same second would collide; acceptable for a
  # human-driven test.
  project     = "iotacg-${formatdate("MMDDhhmmss", timestamp())}"
  region      = "us-east-1"
  environment = "test"
}

# --- Setup: real OIDC issuer (Cognito) ---------------------------------------
#
# A user pool with one app client. No hosted UI domain: the gateway only needs
# the pool's OIDC discovery document, which Cognito serves without a domain.
run "setup_issuer" {
  command = apply

  module {
    source = "../cognito"
  }

  variables {
    project       = var.project
    environment   = var.environment
    region        = var.region
    create_domain = false
  }

  assert {
    condition     = length(output.user_pool_id) > 0 && length(output.user_pool_client_id) > 0
    error_message = "Issuer setup must yield a user pool id and an app client id."
  }
}

# --- Setup: real target Lambda -----------------------------------------------
run "setup_target_lambda" {
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
    error_message = "Target Lambda setup must yield a function ARN."
  }
}

# --- Main: agentcore_gateway against the real issuer and Lambda --------------
run "agentcore_gateway_apply" {
  command = apply

  variables {
    jwt_discovery_url   = "https://cognito-idp.${var.region}.amazonaws.com/${run.setup_issuer.user_pool_id}/.well-known/openid-configuration"
    jwt_allowed_clients = [run.setup_issuer.user_pool_client_id]

    target_lambda_arn = run.setup_target_lambda.function_arn
  }

  # --- Gateway up with id, arn and MCP endpoint ---
  assert {
    condition     = aws_bedrockagentcore_gateway.this.name == "${var.project}-gateway"
    error_message = "Gateway name must default to {project}-gateway."
  }

  assert {
    condition     = length(output.gateway_id) > 0
    error_message = "gateway_id output must be populated after a live apply."
  }

  assert {
    condition     = can(regex("^arn:aws[a-zA-Z-]*:bedrock-agentcore:", output.gateway_arn))
    error_message = "gateway_arn output must be an AgentCore gateway ARN after a live apply."
  }

  assert {
    condition     = can(regex("^https://", output.gateway_url))
    error_message = "gateway_url output must be the live https MCP endpoint."
  }

  # --- Gateway role + confused-deputy trust guard ---
  assert {
    condition     = output.gateway_role_arn == aws_iam_role.gateway.arn
    error_message = "gateway_role_arn output must be the gateway role's ARN."
  }

  assert {
    condition     = jsondecode(aws_iam_role.gateway.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == data.aws_caller_identity.current.account_id
    error_message = "Gateway trust policy must scope aws:SourceAccount to the deploying account."
  }

  assert {
    condition     = jsondecode(aws_iam_role_policy.gateway_invoke[0].policy).Statement[0].Resource[0] == run.setup_target_lambda.function_arn
    error_message = "Gateway invoke policy must be scoped to the live target Lambda only."
  }

  # --- Lambda target bound to the gateway ---
  assert {
    condition     = length(aws_bedrockagentcore_gateway_target.lambda) == 1
    error_message = "Lambda target must be created when target_lambda_arn is wired in."
  }

  assert {
    condition     = output.gateway_target_id != null && length(output.gateway_target_id) > 0
    error_message = "gateway_target_id output must be populated after a live target apply."
  }

  # --- AgentCore -> Lambda invoke permission, scoped to this gateway ---
  assert {
    condition     = aws_lambda_permission.gateway_invoke[0].principal == "bedrock-agentcore.amazonaws.com"
    error_message = "Lambda invoke permission must grant bedrock-agentcore.amazonaws.com."
  }

  assert {
    condition     = aws_lambda_permission.gateway_invoke[0].source_arn == aws_bedrockagentcore_gateway.this.gateway_arn
    error_message = "Lambda invoke permission must be scoped to this gateway's ARN (only this gateway may invoke)."
  }
}
