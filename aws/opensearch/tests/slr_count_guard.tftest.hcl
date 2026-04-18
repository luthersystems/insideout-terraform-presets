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
# Each run below forces one of the two data sources to an empty tuple.

run "managed_mode_plans_with_empty_aoss_slr_tuple" {
  command = plan

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "managed"
    vpc_id          = "vpc-12345"
    subnet_ids      = ["subnet-aaa"]
  }

  # In managed mode, data.aws_iam_roles.aoss_slr has count=0 (empty tuple).
  # The aws_iam_service_linked_role.aoss count expression must cope.
  assert {
    condition     = length(aws_iam_service_linked_role.aoss) == 0
    error_message = "Expected no AOSS SLR resource when deployment_type = managed"
  }
}

run "serverless_mode_plans_with_empty_opensearch_slr_tuple" {
  command = plan

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "serverless"
  }

  # In serverless mode, data.aws_iam_roles.opensearch_slr has count=0
  # (empty tuple). The aws_iam_service_linked_role.opensearch count
  # expression must cope.
  assert {
    condition     = length(aws_iam_service_linked_role.opensearch) == 0
    error_message = "Expected no managed-OpenSearch SLR resource when deployment_type = serverless"
  }
}
