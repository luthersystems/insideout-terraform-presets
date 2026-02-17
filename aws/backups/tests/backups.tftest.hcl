mock_provider "aws" {}
mock_provider "time" {}

# Verify that enabling a single service plans successfully.
run "backups_single_service_enabled" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.backup_assume
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    enable_rds  = true
  }

  assert {
    condition     = aws_backup_plan.this.name == "test-plan"
    error_message = "Expected backup plan name to be 'test-plan' but got '${aws_backup_plan.this.name}'"
  }
}

# Verify that enabling all services plans successfully.
run "backups_all_services_enabled" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.backup_assume
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    enable_ec2_ebs  = true
    enable_rds      = true
    enable_dynamodb = true
    enable_s3       = true
  }

  assert {
    condition     = aws_backup_plan.this.name == "test-plan"
    error_message = "Expected backup plan name to be 'test-plan' but got '${aws_backup_plan.this.name}'"
  }
}

# Verify that zero services enabled fails the precondition at plan time.
run "backups_no_services_fails_precondition" {
  command         = plan
  expect_failures = [aws_backup_plan.this]

  override_data {
    target = data.aws_iam_policy_document.backup_assume
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
  }
}
