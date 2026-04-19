# Plan-only unit tests for the bedrock module.
#
# No AWS credentials required: every run uses command = plan, the AWS
# provider is configured with skip_credentials_validation = true, and the
# module has no data sources that would call AWS at plan time.
#
# Run with: terraform test -filter=tests/unit.tftest.hcl

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "fake"
  secret_key                  = "fake"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
}

variables {
  project                   = "iotest-br"
  region                    = "us-east-1"
  environment               = "test"
  s3_bucket_arn             = "arn:aws:s3:::iotest-br-bucket"
  opensearch_collection_arn = "arn:aws:aoss:us-east-1:111111111111:collection/abc123def456"
}

# --- Default shape ---------------------------------------------------------

run "defaults" {
  command = plan

  # The bedrock_kb role is a non-counted resource so length() doesn't apply;
  # asserting the name was rendered proves it's in the plan.
  assert {
    condition     = aws_iam_role.bedrock_kb.name == "iotest-br-bedrock-role"
    error_message = "Bedrock KB IAM role must always be created with the project-prefixed name."
  }

  assert {
    condition     = aws_iam_role_policy.bedrock_kb.name == "iotest-br-bedrock-policy"
    error_message = "Bedrock KB IAM policy must always be created with the project-prefixed name."
  }

  assert {
    condition     = length(aws_opensearchserverless_access_policy.bedrock) == 0
    error_message = "AOSS data-access policy must NOT be created when opensearch_collection_name is null (default)."
  }

  assert {
    condition     = length(aws_cloudwatch_log_group.invocations) == 0
    error_message = "Invocation log group must NOT be created when enable_invocation_logging is false (default)."
  }

  assert {
    condition     = length(aws_iam_role.invocation_logging) == 0
    error_message = "Invocation logging role must NOT be created by default."
  }

  assert {
    condition     = length(aws_bedrock_model_invocation_logging_configuration.this) == 0
    error_message = "Invocation logging configuration must NOT be created by default."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this) == 1
    error_message = "Guardrail is enabled by default and must be planned."
  }
}

# --- Feature toggles -------------------------------------------------------

run "everything_off" {
  command = plan

  variables {
    enable_guardrail          = false
    enable_invocation_logging = false
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this) == 0
    error_message = "Guardrail must be skipped when enable_guardrail is false."
  }

  assert {
    condition     = length(aws_cloudwatch_log_group.invocations) == 0
    error_message = "Log group must be skipped when invocation logging is off."
  }

  assert {
    condition     = length(aws_bedrock_model_invocation_logging_configuration.this) == 0
    error_message = "Invocation logging config must be skipped when invocation logging is off."
  }
}

run "invocation_logging_enabled" {
  command = plan

  variables {
    enable_invocation_logging     = true
    invocation_log_retention_days = 7
    log_text_data                 = false
    log_image_data                = true
    log_embedding_data            = true
  }

  assert {
    condition     = length(aws_cloudwatch_log_group.invocations) == 1
    error_message = "Log group must be created when invocation logging enabled."
  }

  assert {
    condition     = aws_cloudwatch_log_group.invocations[0].retention_in_days == 7
    error_message = "Log group retention must reflect invocation_log_retention_days."
  }

  assert {
    condition     = length(aws_iam_role.invocation_logging) == 1
    error_message = "Logging IAM role must be created."
  }

  assert {
    condition     = length(aws_iam_role_policy.invocation_logging) == 1
    error_message = "Logging IAM role policy must be created."
  }

  assert {
    condition     = length(aws_bedrock_model_invocation_logging_configuration.this) == 1
    error_message = "Account-level logging configuration must be created."
  }
}

run "aoss_access_policy_wired" {
  command = plan

  variables {
    opensearch_collection_name     = "iotest-br-search"
    aoss_additional_principal_arns = ["arn:aws:iam::111111111111:role/terraform-runner"]
  }

  assert {
    condition     = length(aws_opensearchserverless_access_policy.bedrock) == 1
    error_message = "AOSS access policy must be created when opensearch_collection_name is set."
  }

  # Pattern is "${project}-br-data" — with project=iotest-br this renders
  # iotest-br-br-data. The doubled "br" is fine because the variable is
  # already shaped to be a logical project identifier; we just assert the
  # full name to lock the convention in.
  assert {
    condition     = aws_opensearchserverless_access_policy.bedrock[0].name == "iotest-br-br-data"
    error_message = "AOSS access policy name must follow the {project}-br-data pattern."
  }

  assert {
    condition     = aws_opensearchserverless_access_policy.bedrock[0].type == "data"
    error_message = "AOSS access policy must be of type 'data'."
  }
}

# --- Guardrail variants ----------------------------------------------------

run "guardrail_content_strength_none" {
  command = plan

  variables {
    guardrail_content_filter_strength = "NONE"
  }

  # Content-policy block is dropped; the guardrail itself is still planned
  # because PII / topic / word policies remain available.
  assert {
    condition     = length(aws_bedrock_guardrail.this) == 1
    error_message = "Guardrail must still be planned when content strength is NONE."
  }
}

run "guardrail_pii_blocked" {
  command = plan

  variables {
    guardrail_pii_action   = "BLOCK"
    guardrail_pii_entities = ["EMAIL", "PHONE"]
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this) == 1
    error_message = "Guardrail must be planned with custom PII configuration."
  }
}

run "guardrail_pii_off" {
  command = plan

  variables {
    guardrail_pii_action = "NONE"
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this) == 1
    error_message = "Guardrail must still be planned when PII action is NONE."
  }
}

run "guardrail_topics_and_words" {
  command = plan

  variables {
    guardrail_denied_topics = [{
      name       = "Investment Advice"
      definition = "Specific recommendations to buy or sell securities."
      examples   = ["Should I buy AAPL?", "What stocks should I pick?"]
    }]
    guardrail_blocked_words = ["competitorcorp", "internalcodename"]
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this) == 1
    error_message = "Guardrail must be planned with topic + word policies."
  }
}

# --- Validation rules ------------------------------------------------------

run "rejects_bad_collection_arn" {
  command = plan

  variables {
    opensearch_collection_arn = "not-a-real-arn"
  }

  expect_failures = [var.opensearch_collection_arn]
}

run "rejects_session_principal_arn" {
  command = plan

  variables {
    aoss_additional_principal_arns = ["arn:aws:sts::111111111111:assumed-role/MyRole/session"]
  }

  expect_failures = [var.aoss_additional_principal_arns]
}

run "rejects_invalid_strength" {
  command = plan

  variables {
    guardrail_content_filter_strength = "EXTREME"
  }

  expect_failures = [var.guardrail_content_filter_strength]
}

run "rejects_invalid_pii_action" {
  command = plan

  variables {
    guardrail_pii_action = "REDACT"
  }

  expect_failures = [var.guardrail_pii_action]
}

run "rejects_bad_retention_days" {
  command = plan

  variables {
    enable_invocation_logging     = true
    invocation_log_retention_days = 42
  }

  expect_failures = [var.invocation_log_retention_days]
}

run "rejects_oversize_collection_name" {
  command = plan

  variables {
    opensearch_collection_name = "this-name-is-far-too-long-for-aoss-to-accept-it"
  }

  expect_failures = [var.opensearch_collection_name]
}

run "rejects_oversize_input_message" {
  command = plan

  variables {
    # 501 characters — one over the Bedrock guardrail limit
    guardrail_blocked_input_messaging = "a${join("", [for i in range(500) : "b"])}"
  }

  expect_failures = [var.guardrail_blocked_input_messaging]
}
