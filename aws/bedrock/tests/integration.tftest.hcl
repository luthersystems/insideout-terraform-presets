# Integration tests for the bedrock module — APPLIES AGAINST A REAL AWS ACCOUNT.
#
# Run with valid AWS credentials in the target account:
#
#   cd aws/bedrock
#   terraform init
#   AWS_REGION=us-east-1 terraform test -filter=tests/integration.tftest.hcl
#
# What it does:
#   1. Stands up a real AOSS serverless collection (5–10 min)
#   2. bedrock_apply: applies bedrock with logging OFF, asserts on the
#      rendered guardrail attributes, decodes and inspects the AOSS
#      data-access policy
#   3. bedrock_with_invocation_logging: re-applies the same module with
#      enable_invocation_logging = true, asserts the CloudWatch log group,
#      IAM role + policy, and account-level configuration come up
#   4. Tears EVERYTHING down at the end (terraform test always destroys)
#
# Bedrock Knowledge Base / vector index creation is intentionally not
# exercised — see the comment in main.tf for why those are application-
# layer concerns.
#
# CAUTION: aws_bedrock_model_invocation_logging_configuration is an
# account+region SINGLETON. Run 3 will overwrite any pre-existing
# configuration in the target account+region for the duration of the test
# (and delete it on teardown). Only run this against a sandbox / test
# account where wiping invocation logging is acceptable.
#
# Resource names embed a UTC timestamp suffix (MMDDhhmmss) so two
# back-to-back runs don't collide on the AOSS collection name. If a prior
# run was killed mid-apply, manual cleanup may be required:
#
#   aws opensearchserverless list-collections --query 'collectionSummaries[?starts_with(name, `iotbed-`)]'
#   aws opensearchserverless delete-collection --id <id>
#   aws opensearchserverless list-access-policies --type data --query 'accessPolicySummaries[?starts_with(name, `iotbed-`)]'
#   aws opensearchserverless delete-access-policy --type data --name <name>
#   aws opensearchserverless list-security-policies --type encryption --query 'securityPolicySummaries[?starts_with(name, `iotbed-`)]'
#   aws opensearchserverless list-security-policies --type network --query 'securityPolicySummaries[?starts_with(name, `iotbed-`)]'

provider "aws" {
  region = var.region
}

variables {
  # Suffix the project name with a UTC MMDDhhmmss timestamp so concurrent
  # or back-to-back runs don't collide on AOSS resource names. Computed
  # once at file-load time and shared across every run in this file, so
  # setup_aoss_collection and bedrock_apply see the same project string.
  # Risk: two runs in the exact same second collide. Acceptable for a
  # human-driven integration test.
  project     = "iotbed-${formatdate("MMDDhhmmss", timestamp())}"
  region      = "us-east-1"
  environment = "test"

  # The bedrock module requires both, but neither needs to actually exist
  # for this test — the IAM policy references them as ARN strings only and
  # is never evaluated against the real principals during apply.
  s3_bucket_arn             = "arn:aws:s3:::iotbed-fixture-not-real"
  opensearch_collection_arn = "arn:aws:aoss:us-east-1:000000000000:collection/placeholder"
}

# --- Setup: real AOSS collection ------------------------------------------
#
# Uses the sibling opensearch preset. allow_public_access = true so the
# collection can be reached without provisioning a VPC endpoint, which keeps
# the test self-contained.
run "setup_aoss_collection" {
  command = apply

  # Path is resolved relative to the module under test (aws/bedrock), not
  # this file's location, so we go up one level into aws/ and across.
  module {
    source = "../opensearch"
  }

  variables {
    project             = var.project
    environment         = var.environment
    region              = var.region
    deployment_type     = "serverless"
    allow_public_access = true
  }

  assert {
    condition     = output.collection_arn != null && output.collection_name != null
    error_message = "AOSS setup must yield both an ARN and a collection name."
  }
}

# --- Main: bedrock module against the real collection (logging OFF) -------

run "bedrock_apply" {
  command = apply

  variables {
    opensearch_collection_arn  = run.setup_aoss_collection.collection_arn
    opensearch_collection_name = run.setup_aoss_collection.collection_name

    enable_guardrail          = true
    enable_invocation_logging = false # exercised in the next run

    # Drive every dynamic block in the guardrail so a single apply covers
    # content + PII + topic + word policies end-to-end.
    guardrail_pii_action = "ANONYMIZE"
    guardrail_denied_topics = [{
      name       = "Investment Advice"
      definition = "Specific recommendations to buy or sell securities."
      examples   = ["Should I buy AAPL?"]
    }]
    guardrail_blocked_words = ["competitorcorp"]
  }

  # --- IAM role basics ---
  assert {
    condition     = aws_iam_role.bedrock_kb.name == "${var.project}-bedrock-role"
    error_message = "Bedrock KB role must be named {project}-bedrock-role."
  }

  # Confused-deputy guard must be present in the trust policy of the live
  # role — covers both that the data source resolved AND that the policy
  # JSON includes the SourceAccount condition.
  assert {
    condition     = jsondecode(aws_iam_role.bedrock_kb.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == data.aws_caller_identity.current.account_id
    error_message = "Bedrock KB role trust policy must include aws:SourceAccount scoped to the deploying account."
  }

  # --- AOSS data-access policy ---
  # Decode the live policy JSON and assert on its contents. Catches
  # regressions where the rule shape changes, the bedrock role drops out
  # of the principal list, or aoss:CreateIndex disappears (which is what
  # the application uses to author the vector index).
  assert {
    condition     = aws_opensearchserverless_access_policy.bedrock[0].name == "${var.project}-br-data"
    error_message = "AOSS access policy must follow the {project}-br-data naming convention."
  }

  assert {
    condition = contains(
      jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Principal,
      aws_iam_role.bedrock_kb.arn,
    )
    error_message = "AOSS access policy must include the bedrock role in its principal list."
  }

  assert {
    condition     = length(jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Rules) == 2
    error_message = "AOSS access policy must include both a collection rule and an index rule."
  }

  assert {
    condition = contains(
      jsondecode(aws_opensearchserverless_access_policy.bedrock[0].policy)[0].Rules[1].Permission,
      "aoss:CreateIndex",
    )
    error_message = "AOSS index rule must grant aoss:CreateIndex (the application uses this to author the vector index)."
  }

  # --- Guardrail shape ---
  # Verify the live guardrail rendered every dynamic block we configured
  # above. Asserting on counts catches regressions where a dynamic block
  # silently disappears or iterates the wrong number of times.
  assert {
    condition     = aws_bedrock_guardrail.this[0].name == "${var.project}-guardrail"
    error_message = "Guardrail name must be {project}-guardrail."
  }

  assert {
    condition     = aws_bedrock_guardrail.this[0].version == "DRAFT"
    error_message = "Newly-created guardrail must report version=DRAFT (we don't publish numbered versions in this module)."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].content_policy_config[0].filters_config) == 6
    error_message = "Guardrail must have 5 universal content filters + PROMPT_ATTACK = 6."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].sensitive_information_policy_config[0].pii_entities_config) == 7
    error_message = "Guardrail must render all 7 default PII entities."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].topic_policy_config[0].topics_config) == 1
    error_message = "Guardrail must include the 1 denied topic we configured."
  }

  assert {
    condition     = length(aws_bedrock_guardrail.this[0].word_policy_config[0].words_config) == 1
    error_message = "Guardrail must include the 1 blocked word we configured."
  }

  # --- Outputs ---
  assert {
    condition     = output.guardrail_id != null && length(output.guardrail_id) > 0
    error_message = "Guardrail ID must be populated after apply (this is the field the application passes to InvokeModel)."
  }
}

# --- Invocation logging: re-apply with logging enabled --------------------
#
# This run modifies the bedrock module's state in place to add the logging
# resources. Asserts the CloudWatch log group, IAM role, and account-level
# configuration come up; the existing guardrail and AOSS policy from the
# prior run remain in place. Teardown removes everything.
#
# Worth running because the invocation-logging path is the entire reason
# Bedrock has any observability at all — it's the singleton that pipes
# every InvokeModel call to CloudWatch Logs.
run "bedrock_with_invocation_logging" {
  command = apply

  variables {
    opensearch_collection_arn  = run.setup_aoss_collection.collection_arn
    opensearch_collection_name = run.setup_aoss_collection.collection_name

    enable_guardrail              = true
    enable_invocation_logging     = true
    invocation_log_retention_days = 7
    log_text_data                 = true
    log_image_data                = false
    log_embedding_data            = false

    # Keep guardrail config identical to the prior run so the diff is
    # purely the new logging resources.
    guardrail_pii_action = "ANONYMIZE"
    guardrail_denied_topics = [{
      name       = "Investment Advice"
      definition = "Specific recommendations to buy or sell securities."
      examples   = ["Should I buy AAPL?"]
    }]
    guardrail_blocked_words = ["competitorcorp"]
  }

  assert {
    condition     = aws_cloudwatch_log_group.invocations[0].name == "/aws/bedrock/${var.project}-invocations"
    error_message = "Log group must follow the /aws/bedrock/{project}-invocations naming convention."
  }

  assert {
    condition     = aws_cloudwatch_log_group.invocations[0].retention_in_days == 7
    error_message = "Log group retention must reflect the 7-day setting we passed."
  }

  assert {
    condition     = aws_iam_role.invocation_logging[0].name == "${var.project}-bedrock-logging-role"
    error_message = "Logging IAM role must follow the naming convention."
  }

  # The invocation logging trust policy must also include the confused-
  # deputy guard (mirrors the bedrock_kb assertion in the prior run).
  assert {
    condition     = jsondecode(aws_iam_role.invocation_logging[0].assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == data.aws_caller_identity.current.account_id
    error_message = "Invocation logging role trust policy must include aws:SourceAccount scoped to the deploying account."
  }

  assert {
    condition     = aws_bedrock_model_invocation_logging_configuration.this[0].logging_config[0].text_data_delivery_enabled == true
    error_message = "log_text_data=true must propagate to the live invocation logging configuration."
  }

  assert {
    condition     = aws_bedrock_model_invocation_logging_configuration.this[0].logging_config[0].image_data_delivery_enabled == false
    error_message = "log_image_data=false must propagate to the live invocation logging configuration."
  }

  assert {
    condition     = output.invocation_log_group_name != null && length(output.invocation_log_group_name) > 0
    error_message = "Invocation log group output must be populated after enabling logging."
  }

  assert {
    condition     = output.invocation_logging_role_arn != null
    error_message = "Invocation logging role ARN output must be populated."
  }
}
