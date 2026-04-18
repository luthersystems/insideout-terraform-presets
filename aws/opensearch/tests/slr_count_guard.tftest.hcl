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
}
