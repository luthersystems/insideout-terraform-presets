mock_provider "aws" {}

# Regression for #100. AWS RDS's CreateDBInstance rejects these four
# characters in MasterUserPassword:
#
#   /   @   "   (space)
#
# random_password only samples from override_special, so leaving any
# forbidden character in that set produces non-deterministic apply-time
# failures (roughly 1 - (allowed/total)^N at min_special >= 1). Lock
# the safe set so a future edit re-introducing any forbidden character
# fails at plan time, not in production.

run "password_override_special_excludes_rds_forbidden_chars" {
  command = plan

  variables {
    project     = "pw-test"
    region      = "us-east-1"
    environment = "test"
    vpc_id      = "vpc-12345"
    subnet_ids  = ["subnet-aaa", "subnet-bbb"]
  }

  assert {
    condition     = !strcontains(random_password.db.override_special, "@")
    error_message = "random_password.db.override_special must not contain '@' — RDS CreateDBInstance rejects it in MasterUserPassword. See issue #100."
  }

  assert {
    condition     = !strcontains(random_password.db.override_special, "/")
    error_message = "random_password.db.override_special must not contain '/' — RDS CreateDBInstance rejects it in MasterUserPassword."
  }

  assert {
    condition     = !strcontains(random_password.db.override_special, "\"")
    error_message = "random_password.db.override_special must not contain '\"' — RDS CreateDBInstance rejects it in MasterUserPassword."
  }

  assert {
    condition     = !strcontains(random_password.db.override_special, " ")
    error_message = "random_password.db.override_special must not contain space — RDS CreateDBInstance rejects it in MasterUserPassword."
  }

  # Defence-in-depth: keep at least one special char in the pool so the
  # min_special = 1 constraint can be satisfied. If someone accidentally
  # empties override_special, random_password silently falls back to the
  # default set which contains '@'.
  assert {
    condition     = length(random_password.db.override_special) >= 1
    error_message = "random_password.db.override_special must be non-empty; empty string falls back to random_password's default set, which contains '@'."
  }
}
