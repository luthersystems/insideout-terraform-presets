mock_provider "aws" {}

# Regression for #957. RDS makes /aws/rds/instance/<id>/<export> itself
# when log export is on. Terraform did not own that group, so a destroy
# left it, with its data, in the account. The preset now makes each group
# before the instance, so a destroy deletes it.

variables {
  project                = "rdslogs"
  environment            = "test"
  region                 = "us-west-2"
  vpc_id                 = "vpc-12345"
  subnet_ids             = ["subnet-aaa", "subnet-bbb"]
  enable_cloudwatch_logs = true
  read_replica_count     = 1
}

run "fresh_stack_owns_every_group" {
  command = plan

  assert {
    condition = toset(keys(aws_cloudwatch_log_group.rds)) == toset([
      "/aws/rds/instance/${aws_db_instance.primary.identifier}/postgresql",
      "/aws/rds/instance/${aws_db_instance.primary.identifier}/upgrade",
      "/aws/rds/instance/${aws_db_instance.replica[0].identifier}/postgresql",
      "/aws/rds/instance/${aws_db_instance.replica[0].identifier}/upgrade",
    ])
    error_message = "the preset must own one log group for each instance and each log export."
  }
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.rds : g.retention_in_days == 30])
    error_message = "the log groups must expire (default 30 days)."
  }
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.rds : g.tags["Project"] == "rdslogs"])
    error_message = "the log groups must have the Project tag, which the inspector needs."
  }
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.rds : g.tags["insideout:log-group-owner"] == "terraform"])
    error_message = "the log groups must have the owner tag, so the next plan keeps them."
  }
}

run "logs_off_makes_no_group" {
  command = plan

  variables {
    enable_cloudwatch_logs = false
  }

  assert {
    condition     = length(aws_cloudwatch_log_group.rds) == 0
    error_message = "no log group when log export is off."
  }
}

# A stack deployed before #957: RDS made the group, without tags. Making
# it again fails with ResourceAlreadyExistsException, so the preset must
# leave it alone.
run "group_made_by_rds_is_left_alone" {
  command = plan

  # The override names this group; if the identifier changes, the
  # override would match nothing and the test would pass for no reason.
  assert {
    condition     = aws_db_instance.primary.identifier == "rdslogs-or-test-luthersystems-insideout-rds-rds0"
    error_message = "the override above must name the primary instance's group."
  }

  override_data {
    target = data.aws_cloudwatch_log_groups.rds_existing
    values = {
      log_group_names = ["/aws/rds/instance/rdslogs-or-test-luthersystems-insideout-rds-rds0/postgresql"]
    }
  }
  override_data {
    target = data.aws_cloudwatch_log_group.rds_existing
    values = {
      tags = {}
    }
  }

  assert {
    condition     = !contains(keys(aws_cloudwatch_log_group.rds), "/aws/rds/instance/${aws_db_instance.primary.identifier}/postgresql")
    error_message = "a group that RDS made must not be made again."
  }
  assert {
    condition     = contains(keys(aws_cloudwatch_log_group.rds), "/aws/rds/instance/${aws_db_instance.primary.identifier}/upgrade")
    error_message = "a group that does not exist must still be made."
  }
}

# The plan after the first apply: the group exists and has the owner tag.
# The preset must keep it, or the plan would delete it.
run "group_made_by_terraform_is_kept" {
  command = plan

  # The override names this group; if the identifier changes, the
  # override would match nothing and the test would pass for no reason.
  assert {
    condition     = aws_db_instance.primary.identifier == "rdslogs-or-test-luthersystems-insideout-rds-rds0"
    error_message = "the override above must name the primary instance's group."
  }

  override_data {
    target = data.aws_cloudwatch_log_groups.rds_existing
    values = {
      log_group_names = ["/aws/rds/instance/rdslogs-or-test-luthersystems-insideout-rds-rds0/postgresql"]
    }
  }
  override_data {
    target = data.aws_cloudwatch_log_group.rds_existing
    values = {
      tags = { "insideout:log-group-owner" = "terraform" }
    }
  }

  assert {
    condition     = contains(keys(aws_cloudwatch_log_group.rds), "/aws/rds/instance/${aws_db_instance.primary.identifier}/postgresql")
    error_message = "a group that Terraform made must stay in the plan."
  }
}
