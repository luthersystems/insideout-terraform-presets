# Regression for #957. The amazon-cloudwatch-observability addon (installed
# by aws/eks_nodegroup) makes /aws/containerinsights/<cluster>/* itself.
# Terraform did not own those groups, so a destroy left them, with their
# data, in the account. The cluster preset now makes them, because only it
# knows the cluster name at plan time.

mock_provider "aws" {
  # The IAM documents must be JSON, or the IAM resources refuse them.
  mock_data "aws_iam_policy_document" {
    defaults = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }
  mock_data "aws_partition" {
    defaults = {
      partition  = "aws"
      dns_suffix = "amazonaws.com"
    }
  }
  mock_data "aws_iam_session_context" {
    defaults = {
      issuer_arn = "arn:aws:iam::123456789012:role/deployer"
    }
  }
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
      arn        = "arn:aws:sts::123456789012:assumed-role/deployer/session"
    }
  }
}
mock_provider "tls" {}
mock_provider "time" {}
mock_provider "cloudinit" {}
mock_provider "null" {}

variables {
  project            = "ekslogs"
  environment        = "test"
  region             = "us-west-2"
  vpc_id             = "vpc-12345"
  private_subnet_ids = ["subnet-aaa", "subnet-bbb"]
}

run "fresh_stack_owns_every_group" {
  command = plan

  assert {
    condition = toset(keys(aws_cloudwatch_log_group.container_insights)) == toset([
      for t in ["application", "dataplane", "host", "performance"] : "/aws/containerinsights/${output.cluster_name}/${t}"
    ])
    error_message = "the preset must own the four Container Insights log groups."
  }
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.container_insights : g.retention_in_days == 30])
    error_message = "the log groups must expire (default 30 days)."
  }
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.container_insights : g.tags["Project"] == "ekslogs"])
    error_message = "the log groups must have the Project tag, which the inspector needs."
  }
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.container_insights : g.tags["insideout:log-group-owner"] == "terraform"])
    error_message = "the log groups must have the owner tag, so the next plan keeps them."
  }
}

run "off_makes_no_group" {
  command = plan

  variables {
    container_insights_log_groups = false
  }

  assert {
    condition     = length(aws_cloudwatch_log_group.container_insights) == 0
    error_message = "no log group when container_insights_log_groups is false."
  }
}

# A stack deployed before #957: the addon made the groups, without tags.
# Making one again fails with ResourceAlreadyExistsException, so the
# preset must leave it alone.
run "group_made_by_addon_is_left_alone" {
  command = plan

  override_data {
    target = data.aws_cloudwatch_log_groups.container_insights_existing
    values = {
      log_group_names = ["/aws/containerinsights/ekslogs-or-test-luthers-eks0/performance"]
    }
  }
  override_data {
    target = data.aws_cloudwatch_log_group.container_insights_existing
    values = {
      tags = {}
    }
  }

  # The override names this group; if the name changes, the override would
  # match nothing and the test would pass for no reason.
  assert {
    condition     = output.cluster_name == "ekslogs-or-test-luthers-eks0"
    error_message = "the override above must name this cluster's group."
  }
  assert {
    condition     = !contains(keys(aws_cloudwatch_log_group.container_insights), "/aws/containerinsights/${output.cluster_name}/performance")
    error_message = "a group that the addon made must not be made again."
  }
  assert {
    condition     = length(aws_cloudwatch_log_group.container_insights) == 3
    error_message = "the groups that do not exist must still be made."
  }
}

# The plan after the first apply: the group exists and has the owner tag.
# The preset must keep it, or the plan would delete it.
run "group_made_by_terraform_is_kept" {
  command = plan

  override_data {
    target = data.aws_cloudwatch_log_groups.container_insights_existing
    values = {
      log_group_names = ["/aws/containerinsights/ekslogs-or-test-luthers-eks0/performance"]
    }
  }
  override_data {
    target = data.aws_cloudwatch_log_group.container_insights_existing
    values = {
      tags = { "insideout:log-group-owner" = "terraform" }
    }
  }

  assert {
    condition     = output.cluster_name == "ekslogs-or-test-luthers-eks0"
    error_message = "the override above must name this cluster's group."
  }
  assert {
    condition     = length(aws_cloudwatch_log_group.container_insights) == 4
    error_message = "a group that Terraform made must stay in the plan."
  }
}
