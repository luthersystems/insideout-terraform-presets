# Integration tests for the bedrock module — APPLIES AGAINST A REAL AWS ACCOUNT.
#
# Run with valid AWS credentials in the target account:
#
#   cd aws/bedrock
#   AWS_REGION=us-east-1 terraform test -filter=tests/integration.tftest.hcl
#
# What it does:
#   1. Stands up a real AOSS serverless collection (5–10 min)
#   2. Applies the bedrock module against it: AOSS data-access policy +
#      guardrail
#   3. Asserts the resources came up cleanly and have the expected outputs
#   4. Tears EVERYTHING down at the end (terraform test always destroys)
#
# What it deliberately does NOT exercise:
#   - enable_invocation_logging — that resource is an account+region
#     singleton; flipping it on here would clobber any existing config in the
#     target account. Test it manually in a throwaway account if needed.
#   - Bedrock Knowledge Base / vector index creation — see the comment in
#     main.tf for why those are application-layer concerns.
#
# Resource names use the project = "iotbed" prefix, so collisions with
# real workloads are unlikely. If you re-run while a previous run is still
# tearing down, AOSS will reject the duplicate collection name — wait for
# the prior teardown (~3 min) before retrying.

provider "aws" {
  region = var.region
}

variables {
  project     = "iotbed"
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

# --- Main: bedrock module against the real collection ---------------------

run "bedrock_apply" {
  command = apply

  variables {
    opensearch_collection_arn  = run.setup_aoss_collection.collection_arn
    opensearch_collection_name = run.setup_aoss_collection.collection_name

    enable_guardrail          = true
    enable_invocation_logging = false # account-level singleton, opt-in only

    # Exercise the topic + word + content + PII paths in one apply so a
    # single integration cycle covers every dynamic block in the guardrail.
    guardrail_pii_action = "ANONYMIZE"
    guardrail_denied_topics = [{
      name       = "Investment Advice"
      definition = "Specific recommendations to buy or sell securities."
      examples   = ["Should I buy AAPL?"]
    }]
    guardrail_blocked_words = ["competitorcorp"]
  }

  assert {
    condition     = output.role_arn != null && length(output.role_arn) > 0
    error_message = "Bedrock IAM role ARN must be populated after apply."
  }

  assert {
    condition     = output.aoss_data_access_policy_name != null
    error_message = "AOSS data-access policy must be created when opensearch_collection_name is wired."
  }

  assert {
    condition     = output.guardrail_id != null && length(output.guardrail_id) > 0
    error_message = "Guardrail ID must be populated after apply — this is the field the application passes to InvokeModel."
  }

  assert {
    condition     = output.guardrail_arn != null
    error_message = "Guardrail ARN must be populated after apply."
  }

  assert {
    condition     = output.guardrail_version != null
    error_message = "Guardrail version must be populated (DRAFT initially)."
  }

  assert {
    condition     = output.invocation_log_group_name == null
    error_message = "Invocation log group must remain null when enable_invocation_logging is false."
  }
}
