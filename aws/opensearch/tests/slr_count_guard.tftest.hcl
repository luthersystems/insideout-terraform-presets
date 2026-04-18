mock_provider "aws" {}

# Regression for #70. Both service-linked-role data sources are themselves
# conditional (count = 0 on the "off" deployment_type), producing an empty
# tuple. Indexing with [0] inside the aws_iam_service_linked_role.count
# expression previously failed at plan time:
#
#   Error: Invalid index — data.aws_iam_roles.opensearch_slr is empty tuple
#
# `&&` short-circuit does not prevent Terraform from analysing the [0] access,
# so the guard must be written with try().
#
# Each run pins one direction of the hazard and also exercises the positive
# "create when names is empty" branch by overriding the non-empty probe —
# otherwise a mutation that replaced the try() with a constant would slip past.

run "managed_mode_creates_slr_and_tolerates_empty_aoss_tuple" {
  command = plan

  # Force the managed-SLR probe to report "role absent" so the try() guard's
  # creation branch runs — asserting count == 1 pins that branch.
  override_data {
    target = data.aws_iam_roles.opensearch_slr[0]
    values = {
      names = []
    }
  }

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "managed"
    vpc_id          = "vpc-12345"
    subnet_ids      = ["subnet-aaa"]
  }

  assert {
    condition     = length(aws_iam_service_linked_role.opensearch) == 1
    error_message = "Expected the managed-OpenSearch SLR to be created when the probe returns no matching role"
  }

  # data.aws_iam_roles.aoss_slr has count = 0 here — empty tuple.
  assert {
    condition     = length(aws_iam_service_linked_role.aoss) == 0
    error_message = "Expected no AOSS SLR resource when deployment_type = managed"
  }

  # Encryption policy is AOSS-only. Locks the is_serverless guard at
  # main.tf so a mutation to `count = 1` can't slip through.
  assert {
    condition     = length(aws_opensearchserverless_security_policy.encryption) == 0
    error_message = "AOSS encryption policy must not exist when deployment_type = managed"
  }
}

run "serverless_mode_creates_aoss_slr_and_tolerates_empty_opensearch_tuple" {
  command = plan

  # Mirror of the managed run: force the AOSS probe to report "role absent"
  # so the try() creation branch is exercised.
  override_data {
    target = data.aws_iam_roles.aoss_slr[0]
    values = {
      names = []
    }
  }

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "serverless"
  }

  assert {
    condition     = length(aws_iam_service_linked_role.aoss) == 1
    error_message = "Expected the AOSS SLR to be created when the probe returns no matching role"
  }

  # data.aws_iam_roles.opensearch_slr has count = 0 here — empty tuple.
  assert {
    condition     = length(aws_iam_service_linked_role.opensearch) == 0
    error_message = "Expected no managed-OpenSearch SLR resource when deployment_type = serverless"
  }

  # Regression for #75. merge() unified the bool arm with the string arm
  # of the kms_key_arn ternary, which serialized AWSOwnedKey as the string
  # "true" and made AOSS reject the policy with "string found, boolean
  # expected". Equality against `true` fails on both the string form and
  # on a missing field, so it locks the JSON type.
  assert {
    condition     = jsondecode(aws_opensearchserverless_security_policy.encryption[0].policy).AWSOwnedKey == true
    error_message = "AOSS encryption policy must serialize AWSOwnedKey as a JSON boolean, not a string"
  }

  assert {
    condition     = !can(jsondecode(aws_opensearchserverless_security_policy.encryption[0].policy).KmsARN)
    error_message = "AOSS encryption policy must omit KmsARN when using the AWS-owned key"
  }
}

run "serverless_mode_with_customer_kms_uses_kms_arn_field" {
  command = plan

  override_data {
    target = data.aws_iam_roles.aoss_slr[0]
    values = {
      names = ["AWSServiceRoleForAmazonOpenSearchServerless"]
    }
  }

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "serverless"
    kms_key_arn     = "arn:aws:kms:us-east-1:123456789012:key/abcd1234-ab12-cd34-ef56-abcdef123456"
  }

  assert {
    condition     = jsondecode(aws_opensearchserverless_security_policy.encryption[0].policy).KmsARN == "arn:aws:kms:us-east-1:123456789012:key/abcd1234-ab12-cd34-ef56-abcdef123456"
    error_message = "AOSS encryption policy must emit the customer KmsARN as a string when provided"
  }

  assert {
    condition     = !can(jsondecode(aws_opensearchserverless_security_policy.encryption[0].policy).AWSOwnedKey)
    error_message = "AOSS encryption policy must omit AWSOwnedKey when a customer KMS ARN is set"
  }
}
