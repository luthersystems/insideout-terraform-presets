mock_provider "aws" {}

# Regression for #205. AWS rejects the (allocated_storage,
# max_allocated_storage) combination at apply time with
# `InvalidParameterCombination: Max storage size must be greater than
# storage size` whenever max_allocated_storage > 0 AND
# max_allocated_storage <= allocated_storage. The module's primary
# aws_db_instance carries a lifecycle.precondition that surfaces this
# misconfig at plan time so callers that bypass the composer's auto-derive
# still fail cleanly before any AWS state is provisioned.
#
# Note on expect_failures: it matches *any* check failure on
# resource.aws_db_instance.primary — preconditions, postconditions, or
# input-variable validation routed to the resource. Today the primary has
# exactly one precondition (this one) and no postcondition, and the inputs
# below pass their per-variable validation blocks (allocated_storage >= 20
# and max_allocated_storage >= 0 always hold). So a failure here pins the
# precondition specifically. If a future change adds another check on the
# primary, prefer routing the new rule through a named `check {}` block to
# preserve this test's specificity.

variables {
  project     = "rds-storage-test"
  region      = "us-east-1"
  environment = "test"
  vpc_id      = "vpc-12345"
  subnet_ids  = ["subnet-aaa", "subnet-bbb"]
}

# Happy path: max_allocated_storage strictly greater than allocated_storage.
run "max_greater_than_allocated_passes" {
  command = plan

  variables {
    allocated_storage     = 200
    max_allocated_storage = 1000
  }
}

# Happy path: max_allocated_storage = 0 disables autoscaling. The
# precondition's first disjunct allows this regardless of allocated_storage.
run "max_zero_disables_autoscaling_passes" {
  command = plan

  variables {
    allocated_storage     = 200
    max_allocated_storage = 0
  }
}

# Failure path: the issue #205 prod repro — allocated_storage = 2000
# (storageSize: "2TB") with the module default max_allocated_storage = 1000
# must fail at plan via the precondition, not at apply.
run "max_at_module_default_fails_when_allocated_2tb" {
  command = plan

  variables {
    allocated_storage     = 2000
    max_allocated_storage = 1000
  }

  expect_failures = [
    resource.aws_db_instance.primary,
  ]
}

# Failure path: equal values are also rejected by AWS — guard against the
# off-by-one where someone might think "max == allocated" is fine.
run "max_equal_to_allocated_fails" {
  command = plan

  variables {
    allocated_storage     = 500
    max_allocated_storage = 500
  }

  expect_failures = [
    resource.aws_db_instance.primary,
  ]
}
