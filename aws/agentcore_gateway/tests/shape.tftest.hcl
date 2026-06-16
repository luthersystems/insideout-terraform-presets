# Plan-only unit tests for the agentcore_gateway module.
#
# No AWS credentials required: the AWS provider is mocked so
# data.aws_caller_identity.current and data.aws_region.current resolve at plan
# time. The aws_bedrockagentcore_* family is mocked too so attributes the
# Lambda target / permission reference (gateway_id, gateway_arn) resolve.
#
# These run in CI (filename has no "integration"). Run locally with:
#
#   cd aws/agentcore_gateway
#   terraform init
#   terraform test -filter=tests/shape.tftest.hcl
#
# Assertions pin SHAPE, not exact provider attributes — the AgentCore family's
# schema churns across provider minors (see main.tf maturity caveat), so these
# check resource presence, count-gating, names, and wiring rather than fragile
# deep attribute paths.
#
# TODO(#763): a live-apply integration.tftest.hcl is deliberately deferred —
# AgentCore live-apply needs special account access and a live-apply test
# against a churning provider family would be MORE fragile than this shape
# suite, not less. Track separately; do not treat its absence as folklore.

mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "111111111111"
      arn        = "arn:aws:iam::111111111111:user/test"
      user_id    = "AIDACKCEVSQ6C2EXAMPLE"
    }
  }
  mock_data "aws_region" {
    defaults = {
      region = "us-east-1"
    }
  }
  # The Lambda target and invoke permission reference the gateway id/arn; give
  # the gateway resource id/arn-shaped mocks so those attributes resolve.
  mock_resource "aws_bedrockagentcore_gateway" {
    defaults = {
      gateway_id  = "gw-ABCDEFGHIJ"
      gateway_arn = "arn:aws:bedrock-agentcore:us-east-1:111111111111:gateway/gw-ABCDEFGHIJ"
      gateway_url = "https://gw-ABCDEFGHIJ.gateway.bedrock-agentcore.us-east-1.amazonaws.com/mcp"
    }
  }
  mock_resource "aws_iam_role" {
    defaults = {
      arn = "arn:aws:iam::111111111111:role/iotest-gw-agentcore-gw-role"
    }
  }
}

variables {
  project     = "iotest-gw"
  region      = "us-east-1"
  environment = "test"
}

# --- Default shape (gateway only, no Lambda target) --------------------------

run "defaults" {
  command = plan

  # The CUSTOM_JWT authorizer requires at least one non-empty allowlist — AWS
  # CreateGateway rejects an authorizer with neither, enforced by the gateway
  # precondition in main.tf. So even the "defaults" shape must pin one. Audience
  # is set here; clients is left unset so the null-emission security assertion
  # below still exercises the unset path.
  variables {
    jwt_allowed_audience = ["insideout-agents"]
  }

  # Gateway role is always created (the gateway requires role_arn).
  assert {
    condition     = aws_iam_role.gateway.name == "iotest-gw-agentcore-gw-role"
    error_message = "Gateway IAM role must always be created with the project-prefixed name."
  }

  # Trust policy carries the confused-deputy guards.
  assert {
    condition     = jsondecode(aws_iam_role.gateway.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111111111111"
    error_message = "Gateway trust policy must scope aws:SourceAccount to the current account."
  }

  assert {
    condition     = jsondecode(aws_iam_role.gateway.assume_role_policy).Statement[0].Principal.Service == "bedrock-agentcore.amazonaws.com"
    error_message = "Gateway trust policy must trust the bedrock-agentcore service principal."
  }

  assert {
    condition     = can(jsondecode(aws_iam_role.gateway.assume_role_policy).Statement[0].Condition.ArnLike["aws:SourceArn"])
    error_message = "Gateway trust policy must include an aws:SourceArn ArnLike condition scoping trust to this account's gateways."
  }

  # The gateway itself is always created.
  assert {
    condition     = aws_bedrockagentcore_gateway.this.name == "iotest-gw-gateway"
    error_message = "Gateway name must default to {project}-gateway."
  }

  assert {
    condition     = aws_bedrockagentcore_gateway.this.protocol_type == "MCP"
    error_message = "Gateway must default to the MCP protocol."
  }

  assert {
    condition     = aws_bedrockagentcore_gateway.this.authorizer_type == "CUSTOM_JWT"
    error_message = "Gateway must use the CUSTOM_JWT inbound authorizer."
  }

  # JWT authorizer carries the discovery URL (inbound auth surface).
  assert {
    condition     = aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].discovery_url == "https://example.com/.well-known/openid-configuration"
    error_message = "Gateway JWT authorizer must carry the OIDC discovery URL."
  }

  # MCP protocol block is present.
  assert {
    condition     = length(aws_bedrockagentcore_gateway.this.protocol_configuration[0].mcp) == 1
    error_message = "Gateway must configure the MCP protocol block."
  }

  # Default MCP instructions are surfaced to clients.
  assert {
    condition     = aws_bedrockagentcore_gateway.this.protocol_configuration[0].mcp[0].instructions == "Tools exposed by the Insideout AgentCore gateway."
    error_message = "Gateway MCP block must carry the default instructions."
  }

  # The set allowlist (audience) flows through to the authorizer.
  assert {
    condition     = contains(aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].allowed_audience, "insideout-agents")
    error_message = "Gateway JWT authorizer must carry the supplied allowed_audience."
  }

  # SECURITY: an unset JWT allowlist must be null (not []) so the gateway does
  # not pin an empty allowlist — the preset promises this (main.tf header on
  # the conditional). An empty-list allowlist on a JWT authorizer is ambiguous
  # (allow-none vs allow-all depending on provider); pinning null is the safe,
  # documented shape. Asserted on the unset clients allowlist (audience is set
  # above to satisfy the gateway precondition).
  assert {
    condition     = aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].allowed_clients == null
    error_message = "Unset jwt_allowed_clients must emit null, not an empty allowlist."
  }

  # Default (no target_lambda_arn): no Lambda target, no invoke policy, no
  # Lambda permission.
  assert {
    condition     = length(aws_bedrockagentcore_gateway_target.lambda) == 0
    error_message = "Lambda target must NOT be created when target_lambda_arn is null (default)."
  }

  assert {
    condition     = length(aws_iam_role_policy.gateway_invoke) == 0
    error_message = "Gateway invoke policy must NOT be created when no Lambda target is wired."
  }

  assert {
    condition     = length(aws_lambda_permission.gateway_invoke) == 0
    error_message = "Lambda invoke permission must NOT be created when no Lambda target is wired."
  }
}

# --- With Lambda target (executor wired in) ----------------------------------

run "with_lambda_target" {
  # apply (not plan): the final source_arn assertion compares the Lambda
  # permission's source_arn against aws_bedrockagentcore_gateway.this.gateway_arn,
  # a computed attribute that is "(known after apply)" at plan time — comparing
  # two unknowns raises "Unknown condition value" under command = plan.
  # mock_provider populates the gateway_arn mock default at apply and never calls
  # AWS, so this stays credential-free (mirrors aws/bedrock/tests/unit.tftest.hcl,
  # which uses command = apply for the same computed-ARN-in-condition reason).
  command = apply

  variables {
    target_lambda_arn    = "arn:aws:lambda:us-east-1:111111111111:function:iotest-gw-fn"
    jwt_allowed_audience = ["insideout-agents"]
  }

  assert {
    condition     = length(aws_bedrockagentcore_gateway_target.lambda) == 1
    error_message = "Lambda target must be created when target_lambda_arn is supplied."
  }

  assert {
    condition     = aws_bedrockagentcore_gateway_target.lambda[0].target_configuration[0].mcp[0].lambda[0].lambda_arn == "arn:aws:lambda:us-east-1:111111111111:function:iotest-gw-fn"
    error_message = "Lambda target must point at the wired Lambda ARN."
  }

  # The target uses the gateway's own IAM role for credentials (empty
  # gateway_iam_role block).
  assert {
    condition     = length(aws_bedrockagentcore_gateway_target.lambda[0].credential_provider_configuration[0].gateway_iam_role) == 1
    error_message = "Lambda target must use the gateway's IAM role credential provider."
  }

  # The gateway role's invoke policy is created and scoped to the target Lambda.
  assert {
    condition     = length(aws_iam_role_policy.gateway_invoke) == 1
    error_message = "Gateway invoke policy must be created alongside the Lambda target."
  }

  assert {
    condition     = jsondecode(aws_iam_role_policy.gateway_invoke[0].policy).Statement[0].Resource[0] == "arn:aws:lambda:us-east-1:111111111111:function:iotest-gw-fn"
    error_message = "Gateway invoke policy must scope lambda:InvokeFunction to the wired Lambda ARN."
  }

  # SECURITY (least privilege): the grant must be EXACTLY lambda:InvokeFunction
  # — a widened action (lambda:* / iam:PassRole) would be a privilege
  # escalation. Pin the action, not just the resource.
  assert {
    condition     = jsondecode(aws_iam_role_policy.gateway_invoke[0].policy).Statement[0].Action[0] == "lambda:InvokeFunction" && length(jsondecode(aws_iam_role_policy.gateway_invoke[0].policy).Statement[0].Action) == 1
    error_message = "Gateway invoke policy must grant ONLY lambda:InvokeFunction."
  }

  # The Lambda permission grants the AgentCore gateway service principal.
  assert {
    condition     = length(aws_lambda_permission.gateway_invoke) == 1
    error_message = "Lambda invoke permission must be created alongside the Lambda target."
  }

  assert {
    condition     = aws_lambda_permission.gateway_invoke[0].principal == "bedrock-agentcore.amazonaws.com"
    error_message = "Lambda invoke permission must grant bedrock-agentcore.amazonaws.com."
  }

  # SECURITY: the permission's source_arn must scope invocation to THIS
  # gateway's ARN — without it any AgentCore caller in the account could
  # invoke the function. Removing the scoping must fail this assertion.
  assert {
    condition     = aws_lambda_permission.gateway_invoke[0].source_arn == aws_bedrockagentcore_gateway.this.gateway_arn
    error_message = "Lambda invoke permission must scope source_arn to this gateway's ARN."
  }
}

# --- Custom gateway name + JWT allowlists ------------------------------------

run "custom_name_and_jwt_allowlists" {
  command = plan

  variables {
    gateway_name           = "support-tools"
    jwt_discovery_url      = "https://auth.example.com/.well-known/openid-configuration"
    jwt_allowed_audience   = ["insideout-agents"]
    jwt_allowed_clients    = ["client-abc"]
    mcp_search_type        = "SEMANTIC"
    mcp_supported_versions = ["2025-03-26"]
  }

  assert {
    condition     = aws_bedrockagentcore_gateway.this.name == "support-tools"
    error_message = "Gateway name must honor the explicit gateway_name override."
  }

  assert {
    condition     = aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].discovery_url == "https://auth.example.com/.well-known/openid-configuration"
    error_message = "Gateway must honor the explicit jwt_discovery_url."
  }

  # SECURITY: BOTH inbound-auth allowlists must carry the supplied values.
  # allowed_audience and allowed_clients are emitted by independent
  # conditionals in main.tf, so each must be asserted separately — a dropped
  # allowed_clients line is a confused-deputy-adjacent auth-surface regression.
  assert {
    condition     = contains(aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].allowed_audience, "insideout-agents")
    error_message = "Gateway JWT authorizer must carry the supplied allowed_audience."
  }

  assert {
    condition     = contains(aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].allowed_clients, "client-abc")
    error_message = "Gateway JWT authorizer must carry the supplied allowed_clients."
  }

  # The SEMANTIC search_type and supported_versions reach the MCP block.
  assert {
    condition     = aws_bedrockagentcore_gateway.this.protocol_configuration[0].mcp[0].search_type == "SEMANTIC"
    error_message = "Gateway MCP block must carry the supplied SEMANTIC search_type."
  }

  assert {
    condition     = contains(aws_bedrockagentcore_gateway.this.protocol_configuration[0].mcp[0].supported_versions, "2025-03-26")
    error_message = "Gateway MCP block must carry the supplied supported_versions."
  }
}

# --- GovCloud partition ARN accepted -----------------------------------------
#
# target_lambda_arn's regex (^arn:aws[a-zA-Z-]*:lambda:) deliberately admits
# aws-us-gov / aws-cn partitions. Confirm a GovCloud ARN is ACCEPTED and flows
# through to the invoke policy's Resource — guards against an over-tightening
# mutation (^arn:aws:lambda:) that would silently break GovCloud customers.

run "govcloud_lambda_arn_accepted" {
  command = plan

  variables {
    target_lambda_arn    = "arn:aws-us-gov:lambda:us-gov-west-1:111111111111:function:iotest-gw-fn"
    jwt_allowed_audience = ["insideout-agents"]
  }

  assert {
    condition     = length(aws_bedrockagentcore_gateway_target.lambda) == 1
    error_message = "A GovCloud-partition Lambda ARN must be accepted (the regex admits aws-us-gov)."
  }

  assert {
    condition     = jsondecode(aws_iam_role_policy.gateway_invoke[0].policy).Statement[0].Resource[0] == "arn:aws-us-gov:lambda:us-gov-west-1:111111111111:function:iotest-gw-fn"
    error_message = "GovCloud Lambda ARN must flow through to the invoke policy Resource."
  }
}

# --- Validation failures -----------------------------------------------------

run "bad_protocol_type_rejected" {
  command = plan

  variables {
    protocol_type = "REST"
  }

  expect_failures = [
    var.protocol_type,
  ]
}

run "bad_discovery_url_rejected" {
  command = plan

  variables {
    jwt_discovery_url = "http://not-https.example.com/discovery"
  }

  expect_failures = [
    var.jwt_discovery_url,
  ]
}

run "bad_lambda_arn_rejected" {
  command = plan

  variables {
    target_lambda_arn = "not-an-arn"
    # An allowlist is required to satisfy the gateway precondition: target_lambda_arn
    # is not consumed by aws_bedrockagentcore_gateway.this, so an invalid value does
    # not block that resource from planning (and evaluating its precondition) the way
    # an invalid protocol_type / discovery_url / search_type / gateway_name does. The
    # expected failure here is the target_lambda_arn variable validation, not the
    # precondition.
    jwt_allowed_audience = ["insideout-agents"]
  }

  expect_failures = [
    var.target_lambda_arn,
  ]
}

run "bad_search_type_rejected" {
  command = plan

  variables {
    mcp_search_type = "FUZZY"
  }

  expect_failures = [
    var.mcp_search_type,
  ]
}

run "bad_gateway_name_rejected" {
  command = plan

  variables {
    # Spaces are not permitted by the gateway_name regex.
    gateway_name = "not a valid name"
  }

  expect_failures = [
    var.gateway_name,
  ]
}

# --- Precondition: CUSTOM_JWT requires an allowlist --------------------------
#
# Denial test for the gateway precondition (main.tf): a CUSTOM_JWT authorizer
# with NEITHER allowlist must be rejected at plan, not deferred to AWS
# CreateGateway's ValidationException. Deleting the precondition leaves the rest
# of the suite green, so this is the only run that guards it.
run "jwt_neither_allowlist_rejected" {
  command = plan

  # No jwt_allowed_audience, no jwt_allowed_clients (both default []).
  expect_failures = [
    aws_bedrockagentcore_gateway.this,
  ]
}

# Symmetric to the defaults run (audience set, clients asserted null): set only
# clients and assert the unset audience collapses to null, not []. The two
# allowlist conditionals (main.tf) are independent, so each null branch needs
# its own coverage.
run "clients_only_audience_emits_null" {
  command = plan

  variables {
    jwt_allowed_clients = ["client-abc"]
  }

  assert {
    condition     = aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].allowed_audience == null
    error_message = "Unset jwt_allowed_audience must emit null, not an empty allowlist."
  }

  assert {
    condition     = contains(aws_bedrockagentcore_gateway.this.authorizer_configuration[0].custom_jwt_authorizer[0].allowed_clients, "client-abc")
    error_message = "Gateway JWT authorizer must carry the supplied allowed_clients."
  }
}

# --- Module half of the count-on-computed fix (#807) -------------------------
#
# The composer gates the Lambda target on a plan-time-known enable_lambda_target
# flag, not the computed target_lambda_arn. Prove the preset honors it: with the
# flag false but a non-null ARN supplied, NO target / invoke policy / permission
# may be created — count must follow the bool, not the ARN. A revert to
# `count = var.target_lambda_arn != null` makes this run fail.
run "enable_lambda_target_false_overrides_arn" {
  command = plan

  variables {
    target_lambda_arn    = "arn:aws:lambda:us-east-1:111111111111:function:iotest-gw-fn"
    enable_lambda_target = false
    jwt_allowed_audience = ["insideout-agents"]
  }

  assert {
    condition     = length(aws_bedrockagentcore_gateway_target.lambda) == 0
    error_message = "enable_lambda_target=false must suppress the Lambda target even when target_lambda_arn is set."
  }

  assert {
    condition     = length(aws_iam_role_policy.gateway_invoke) == 0
    error_message = "enable_lambda_target=false must suppress the gateway invoke policy."
  }

  assert {
    condition     = length(aws_lambda_permission.gateway_invoke) == 0
    error_message = "enable_lambda_target=false must suppress the Lambda invoke permission."
  }
}
