# Plan-only unit tests for the bedrock module.
#
# No AWS credentials required: the AWS provider is mocked so
# data.aws_caller_identity.current resolves to a fixed account ID at plan
# time. Run with:
#
#   terraform test -filter=tests/unit.tftest.hcl

# Mocking the entire AWS provider replaces it for every run in this file
# and gives data.aws_caller_identity.current a predictable account_id.
# Resources never make real API calls under mock_provider, which is why
# command = plan is enough to exercise the module's authoring logic.
mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "111111111111"
      arn        = "arn:aws:iam::111111111111:user/test"
      user_id    = "AIDACKCEVSQ6C2EXAMPLE"
    }
  }
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

  assert {
    condition     = aws_iam_role.bedrock_kb.name == "iotest-br-bedrock-role"
    error_message = "Bedrock KB IAM role must always be created with the project-prefixed name."
  }

  assert {
    condition     = aws_iam_role_policy.bedrock_kb.name == "iotest-br-bedrock-policy"
    error_message = "Bedrock KB IAM policy must always be created with the project-prefixed name."
  }

  # Trust policy must include the SourceAccount confused-deputy guard,
  # scoped to the mocked test account.
  assert {
    condition     = jsondecode(aws_iam_role.bedrock_kb.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111111111111"
    error_message = "bedrock_kb trust policy must include aws:SourceAccount scoped to the current account."
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

  # Default guardrail shape: 5 universal categories + PROMPT_ATTACK = 6 filters,
  # 7 default PII entities at ANONYMIZE, no topics, no words.
  assert {
    condition     = length(aws_bedrock_guardrail.this[0].content_policy_config[0].filters_config) == 6
    error_message = "Default guardrail must have 5 universal content filters + PROMPT_ATTACK = 6."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].sensitive_information_policy_config[0].pii_entities_config) == 7
    error_message = "Default guardrail must render all 7 default PII entities."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].topic_policy_config) == 0
    error_message = "Default guardrail must NOT include a topic_policy_config block (none configured)."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].word_policy_config) == 0
    error_message = "Default guardrail must NOT include a word_policy_config block (none configured)."
  }

  assert {
    condition     = aws_bedrock_guardrail.this[0].name == "iotest-br-guardrail"
    error_message = "Guardrail name must be {project}-guardrail."
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

# --- Output null-gating (cheap to assert in unit, expensive in integration) ---

run "outputs_null_when_features_off" {
  command = plan

  variables {
    enable_guardrail          = false
    enable_invocation_logging = false
  }

  assert {
    condition     = output.guardrail_id == null && output.guardrail_arn == null && output.guardrail_version == null
    error_message = "All guardrail outputs must be null when enable_guardrail is false."
  }

  assert {
    condition     = output.invocation_log_group_name == null && output.invocation_log_group_arn == null && output.invocation_logging_role_arn == null
    error_message = "All invocation-logging outputs must be null when enable_invocation_logging is false."
  }

  assert {
    condition     = output.aoss_data_access_policy_name == null
    error_message = "AOSS data-access policy output must be null when opensearch_collection_name is unset."
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
    condition     = aws_cloudwatch_log_group.invocations[0].name == "/aws/bedrock/iotest-br-invocations"
    error_message = "Log group name must follow the /aws/bedrock/{project}-invocations convention."
  }

  assert {
    condition     = length(aws_iam_role.invocation_logging) == 1
    error_message = "Logging IAM role must be created."
  }

  # Confused-deputy guard on the new role too.
  assert {
    condition     = jsondecode(aws_iam_role.invocation_logging[0].assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111111111111"
    error_message = "invocation_logging trust policy must include aws:SourceAccount scoped to the current account."
  }

  assert {
    condition     = length(aws_iam_role_policy.invocation_logging) == 1
    error_message = "Logging IAM role policy must be created."
  }

  assert {
    condition     = length(aws_bedrock_model_invocation_logging_configuration.this) == 1
    error_message = "Account-level logging configuration must be created."
  }

  # The configuration object must reflect every flag toggled above.
  assert {
    condition     = aws_bedrock_model_invocation_logging_configuration.this[0].logging_config[0].text_data_delivery_enabled == false
    error_message = "log_text_data=false must propagate to the logging configuration."
  }

  assert {
    condition     = aws_bedrock_model_invocation_logging_configuration.this[0].logging_config[0].image_data_delivery_enabled == true
    error_message = "log_image_data=true must propagate to the logging configuration."
  }

  assert {
    condition     = aws_bedrock_model_invocation_logging_configuration.this[0].logging_config[0].embedding_data_delivery_enabled == true
    error_message = "log_embedding_data=true must propagate to the logging configuration."
  }
}

run "aoss_access_policy_wired_with_extra_principals" {
  # apply (not plan) because the rendered policy embeds aws_iam_role.bedrock_kb.arn,
  # which is "(known after apply)" at plan time. mock_provider populates a mock
  # ARN at apply time and never calls AWS, so this stays credential-free.
  command = apply

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

  # Decode the JSON and assert on its inner shape — the policy body is
  # where regressions hide. We confirm: principal list contains both the
  # bedrock role (computed at plan time as a placeholder, so we only check
  # length) and the additional principal we passed in; both rule blocks
  # are present; the index rule includes aoss:CreateIndex.
  assert {
    condition     = length(jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)) == 1
    error_message = "AOSS access policy must contain exactly one policy element."
  }

  assert {
    condition = contains(
      jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Principal,
      "arn:aws:iam::111111111111:role/terraform-runner",
    )
    error_message = "AOSS access policy principal list must include the additional principal we passed in."
  }

  assert {
    condition     = length(jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Principal) == 2
    error_message = "AOSS access policy principal list must contain exactly 2 entries: bedrock role + 1 additional."
  }

  assert {
    condition     = length(jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Rules) == 2
    error_message = "AOSS access policy must contain both a collection rule and an index rule."
  }

  assert {
    condition = contains(
      jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Rules[1].Permission,
      "aoss:CreateIndex",
    )
    error_message = "Index rule must grant aoss:CreateIndex (required for the application to author the vector index)."
  }

  assert {
    condition     = jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Rules[0].Resource[0] == "collection/iotest-br-search"
    error_message = "Collection rule resource must reference the wired collection name."
  }
}

run "aoss_access_policy_with_no_extra_principals" {
  # apply (not plan) — see aoss_access_policy_wired_with_extra_principals.
  command = apply

  variables {
    opensearch_collection_name = "iotest-br-search"
    # aoss_additional_principal_arns left at default []
  }

  # Empty additional-principals concat path — bedrock role only.
  assert {
    condition     = length(jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Principal) == 1
    error_message = "AOSS principal list must contain exactly the bedrock role when no additional principals are passed."
  }
}

# --- Guardrail variants ----------------------------------------------------

run "guardrail_content_strength_none_drops_block" {
  command = plan

  variables {
    guardrail_content_filter_strength = "NONE"
  }

  # The conditional in main.tf drops content_policy_config entirely when
  # strength is NONE. Asserting the block disappears proves the conditional
  # works (and would catch a regression that always-on'd the block).
  assert {
    condition     = length(aws_bedrock_guardrail.this[0].content_policy_config) == 0
    error_message = "content_policy_config block must be dropped when content strength is NONE."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].sensitive_information_policy_config) == 1
    error_message = "PII policy must remain even when content policy is NONE."
  }
}

run "guardrail_pii_off_drops_block" {
  command = plan

  variables {
    guardrail_pii_action = "NONE"
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].sensitive_information_policy_config) == 0
    error_message = "sensitive_information_policy_config block must be dropped when PII action is NONE."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].content_policy_config) == 1
    error_message = "Content policy must remain even when PII is NONE."
  }
}

run "guardrail_pii_blocked_with_custom_entities" {
  command = plan

  variables {
    guardrail_pii_action   = "BLOCK"
    guardrail_pii_entities = ["EMAIL", "PHONE"]
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].sensitive_information_policy_config[0].pii_entities_config) == 2
    error_message = "PII entities config must render exactly the two entries we passed."
  }

  # Verify the action propagates from the variable into the rendered block.
  assert {
    condition = alltrue([
      for e in aws_bedrock_guardrail.this[0].sensitive_information_policy_config[0].pii_entities_config : e.action == "BLOCK"
    ])
    error_message = "All rendered PII entries must take the BLOCK action when guardrail_pii_action=BLOCK."
  }
}

run "guardrail_topics_and_words" {
  command = plan

  variables {
    guardrail_denied_topics = [
      {
        name       = "Investment Advice"
        definition = "Specific recommendations to buy or sell securities."
        examples   = ["Should I buy AAPL?", "What stocks should I pick?"]
      },
      {
        name       = "Medical Diagnosis"
        definition = "Clinical diagnosis of patient conditions."
        examples   = ["Do I have cancer?"]
      },
      {
        name       = "Legal Advice"
        definition = "Specific legal recommendations for the user's situation."
      },
    ]
    guardrail_blocked_words = ["competitorcorp", "internalcodename"]
  }

  # Multi-topic iteration must render all three; tests the dynamic for_each.
  assert {
    condition     = length(aws_bedrock_guardrail.this[0].topic_policy_config[0].topics_config) == 3
    error_message = "topic_policy_config must contain all 3 denied topics."
  }

  assert {
    condition = alltrue([
      for t in aws_bedrock_guardrail.this[0].topic_policy_config[0].topics_config : t.type == "DENY"
    ])
    error_message = "All denied topics must have type=DENY."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].word_policy_config[0].words_config) == 2
    error_message = "word_policy_config must contain both blocked words."
  }
}

run "guardrail_with_kms_key" {
  command = plan

  variables {
    guardrail_kms_key_arn = "arn:aws:kms:us-east-1:111111111111:key/abcd1234-ab12-cd34-ef56-abcdef123456"
  }

  assert {
    condition     = aws_bedrock_guardrail.this[0].kms_key_arn == "arn:aws:kms:us-east-1:111111111111:key/abcd1234-ab12-cd34-ef56-abcdef123456"
    error_message = "kms_key_arn must propagate to the guardrail resource for customer-managed encryption."
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

run "rejects_unknown_pii_entity" {
  command = plan

  variables {
    guardrail_pii_entities = ["EMAIL", "EMAILS"] # second one is a typo
  }

  expect_failures = [var.guardrail_pii_entities]
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

run "rejects_oversize_output_message" {
  command = plan

  variables {
    guardrail_blocked_outputs_messaging = "a${join("", [for i in range(500) : "b"])}"
  }

  expect_failures = [var.guardrail_blocked_outputs_messaging]
}

run "accepts_max_length_input_message" {
  command = plan

  variables {
    # Exactly 500 characters — the upper bound. Anchors the validation
    # boundary on the accepting side so a regression that drops the
    # validation lower can't go undetected.
    guardrail_blocked_input_messaging = join("", [for i in range(500) : "a"])
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this) == 1
    error_message = "Exactly-500-char message must be accepted (boundary case)."
  }
}

run "rejects_empty_environment" {
  command = plan

  variables {
    environment = "  "
  }

  expect_failures = [var.environment]
}

run "rejects_oversize_project" {
  command = plan

  variables {
    # 25 chars, one over the bedrock module's project limit (24).
    project = "abcdefghij-klmnopqrstuvw1"
  }

  expect_failures = [var.project]
}

run "rejects_bad_kms_arn" {
  command = plan

  variables {
    guardrail_kms_key_arn = "not-a-kms-arn"
  }

  expect_failures = [var.guardrail_kms_key_arn]
}
