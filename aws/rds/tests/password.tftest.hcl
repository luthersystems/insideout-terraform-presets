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

  # --- Forbidden-char assertions: direct regression for #100.
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

  # --- Generator-config assertions: lock the surrounding knobs that
  # determine which character pool random_password samples from. Without
  # these, a future edit that (for example) disables `special` or drops
  # `override_special` entirely would silently fall back to the provider's
  # default special-char set — which contains '@' — and re-open #100.
  assert {
    condition     = random_password.db.special == true
    error_message = "random_password.db.special must stay true — flipping it makes the override_special guard this test relies on vacuous and silently weakens the password."
  }

  assert {
    condition     = random_password.db.min_special >= 1
    error_message = "random_password.db.min_special must be >= 1 — without it random_password may emit a password with zero special chars, failing most RDS password-complexity policies."
  }

  assert {
    condition     = random_password.db.length >= 16
    error_message = "random_password.db.length must be >= 16 — shorter passwords weaken entropy meaningfully and risk RDS engine-level minimum-length rejections on some configurations."
  }

  # --- Pool content: at least one RDS-safe non-alphanumeric character
  # must be in override_special so min_special=1 can be satisfied without
  # random_password falling back to its default set (which contains '@').
  # This is stricter than the previous length>=1 check: "abc" would satisfy
  # length>=1 but contains no characters eligible as "special" for the
  # min_special constraint, forcing the default-set fallback at apply time.
  assert {
    condition     = length(regexall("[^A-Za-z0-9/@\" ]", random_password.db.override_special)) >= 1
    error_message = "override_special must contain at least one non-alphanumeric character that is not in RDS's forbidden set {/, @, \", space}. An all-alphanumeric override_special triggers the default-set fallback, which contains '@'."
  }
}
