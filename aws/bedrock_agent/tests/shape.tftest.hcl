# Plan-only unit tests for the bedrock_agent module.
#
# No AWS credentials required: the AWS provider is mocked so
# data.aws_caller_identity.current and data.aws_region.current resolve at plan
# time. Run with:
#
#   cd aws/bedrock_agent
#   terraform init
#   terraform test -filter=tests/shape.tftest.hcl

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
  # The action-group Lambda invoke permission and the agent role reference the
  # agent ARN; give the agent resource an ARN-shaped mock so attributes that
  # parse as ARNs resolve at apply time.
  mock_resource "aws_bedrockagent_agent" {
    defaults = {
      agent_arn = "arn:aws:bedrock:us-east-1:111111111111:agent/ABCDEFGHIJ"
      agent_id  = "ABCDEFGHIJ"
    }
  }
  mock_resource "aws_iam_role" {
    defaults = {
      arn = "arn:aws:iam::111111111111:role/iotest-bra-bedrock-agent-role"
    }
  }
}

variables {
  project     = "iotest-bra"
  region      = "us-east-1"
  environment = "test"
}

# --- Default shape (chat-only agent) -----------------------------------------

run "defaults" {
  command = plan

  # Agent role + policy are always created.
  assert {
    condition     = aws_iam_role.agent.name == "iotest-bra-bedrock-agent-role"
    error_message = "Agent resource IAM role must always be created with the project-prefixed name."
  }

  assert {
    condition     = aws_iam_role_policy.agent.name == "iotest-bra-bedrock-agent-policy"
    error_message = "Agent IAM policy must always be created."
  }

  # Trust policy carries the confused-deputy guards.
  assert {
    condition     = jsondecode(aws_iam_role.agent.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111111111111"
    error_message = "Agent trust policy must scope aws:SourceAccount to the current account."
  }

  assert {
    condition     = can(jsondecode(aws_iam_role.agent.assume_role_policy).Statement[0].Condition.ArnLike["aws:SourceArn"])
    error_message = "Agent trust policy must include an aws:SourceArn ArnLike condition scoping trust to this account's agents."
  }

  # The agent prepares itself so the alias never binds a NOT_PREPARED agent.
  assert {
    condition     = aws_bedrockagent_agent.this.prepare_agent == true
    error_message = "Agent must set prepare_agent = true so its alias binds a PREPARED version."
  }

  assert {
    condition     = aws_bedrockagent_agent.this.agent_name == "iotest-bra-agent"
    error_message = "Agent name must default to {project}-agent."
  }

  # Default chat-only agent: no action group, no Lambda permission, no KB
  # association, no Retrieve statement on the policy.
  assert {
    condition     = length(aws_bedrockagent_agent_action_group.this) == 0
    error_message = "Action group must NOT be created when action_group_lambda_arn is null (default)."
  }

  assert {
    condition     = length(aws_lambda_permission.agent_invoke) == 0
    error_message = "Lambda invoke permission must NOT be created when no action-group Lambda is wired."
  }

  assert {
    condition     = length(aws_bedrockagent_agent_knowledge_base_association.this) == 0
    error_message = "KB association must NOT be created when knowledge_base_id is null (default)."
  }

  assert {
    condition     = length(jsondecode(aws_iam_role_policy.agent.policy).Statement) == 1
    error_message = "Chat-only agent policy must contain only the InvokeModel statement (no Retrieve)."
  }

  # The alias is always created — the stable invokable handle.
  assert {
    condition     = aws_bedrockagent_agent_alias.this.agent_alias_name == "iotest-bra-live"
    error_message = "A live alias must always be created."
  }
}

# --- With action group (Lambda executor wired in) ----------------------------

run "with_action_group" {
  command = plan

  variables {
    action_group_lambda_arn = "arn:aws:lambda:us-east-1:111111111111:function:iotest-bra-fn"
  }

  assert {
    condition     = length(aws_bedrockagent_agent_action_group.this) == 1
    error_message = "Action group must be created when action_group_lambda_arn is supplied."
  }

  assert {
    condition     = aws_bedrockagent_agent_action_group.this[0].action_group_executor[0].lambda == "arn:aws:lambda:us-east-1:111111111111:function:iotest-bra-fn"
    error_message = "Action group executor must point at the wired Lambda ARN."
  }

  assert {
    condition     = aws_bedrockagent_agent_action_group.this[0].agent_version == "DRAFT"
    error_message = "Action group must attach to the DRAFT version."
  }

  assert {
    condition     = aws_bedrockagent_agent_action_group.this[0].prepare_agent == true
    error_message = "Action group must re-prepare the agent so the alias picks up the tools."
  }

  assert {
    condition     = length(aws_lambda_permission.agent_invoke) == 1
    error_message = "Lambda invoke permission must be created alongside the action group."
  }

  assert {
    condition     = aws_lambda_permission.agent_invoke[0].principal == "bedrock.amazonaws.com"
    error_message = "Lambda invoke permission must grant bedrock.amazonaws.com."
  }
}

# --- With Knowledge Base association (RAG agent) -----------------------------

run "with_knowledge_base" {
  command = plan

  variables {
    knowledge_base_id = "EMDPPAYPZI"
  }

  assert {
    condition     = length(aws_bedrockagent_agent_knowledge_base_association.this) == 1
    error_message = "KB association must be created when knowledge_base_id is supplied."
  }

  assert {
    condition     = aws_bedrockagent_agent_knowledge_base_association.this[0].knowledge_base_id == "EMDPPAYPZI"
    error_message = "KB association must reference the supplied knowledge_base_id."
  }

  assert {
    condition     = aws_bedrockagent_agent_knowledge_base_association.this[0].knowledge_base_state == "ENABLED"
    error_message = "KB association must be ENABLED so the agent retrieves from it."
  }

  # The agent role must now also carry the Retrieve statement.
  assert {
    condition     = length(jsondecode(aws_iam_role_policy.agent.policy).Statement) == 2
    error_message = "RAG agent policy must contain both InvokeModel and Retrieve statements."
  }

  assert {
    condition     = jsondecode(aws_iam_role_policy.agent.policy).Statement[1].Sid == "RetrieveFromKnowledgeBase"
    error_message = "Second policy statement must be the KB Retrieve grant."
  }
}

# --- Full RAG agent (action group + KB) --------------------------------------

run "action_group_and_knowledge_base" {
  command = plan

  variables {
    action_group_lambda_arn = "arn:aws:lambda:us-east-1:111111111111:function:iotest-bra-fn"
    knowledge_base_id       = "EMDPPAYPZI"
  }

  assert {
    condition     = length(aws_bedrockagent_agent_action_group.this) == 1 && length(aws_bedrockagent_agent_knowledge_base_association.this) == 1
    error_message = "Both action group and KB association must compose together for a tool-using RAG agent."
  }
}

# --- Validation failures -----------------------------------------------------

run "empty_instruction_rejected" {
  command = plan

  variables {
    instruction = ""
  }

  expect_failures = [
    var.instruction,
  ]
}

run "empty_foundation_model_rejected" {
  command = plan

  variables {
    foundation_model = "  "
  }

  expect_failures = [
    var.foundation_model,
  ]
}

run "bad_lambda_arn_rejected" {
  command = plan

  variables {
    action_group_lambda_arn = "not-an-arn"
  }

  expect_failures = [
    var.action_group_lambda_arn,
  ]
}
