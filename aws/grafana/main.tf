# Amazon Managed Grafana preset (#936).
#
# A Grafana workspace that reads CloudWatch metrics and logs of this
# account. The preset provisions:
#
#   - A workspace role (`aws_iam_role.grafana`, CUSTOMER_MANAGED) that
#     the Grafana service assumes, with an inline read-only policy for
#     CloudWatch metrics and alarms, CloudWatch Logs queries and EC2
#     descriptions (for dimension lookups).
#   - The workspace (`aws_grafana_workspace.main`), CloudWatch data
#     source, current account only.
#   - Login:
#       - AWS_SSO (default): IAM Identity Center. Identity Center MUST be
#         on in the account (or its organization) before the deploy, or
#         the workspace create fails. Users in `admin_sso_user_ids` and
#         groups in `admin_sso_group_ids` get the ADMIN role
#         (`aws_grafana_role_association.admin`).
#       - SAML: `aws_grafana_workspace_saml_configuration.main` from
#         `saml_idp_metadata_url` and the role assertion values.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "grafana"
  resource       = "grafana"
}

data "aws_caller_identity" "current" {}

locals {
  tags = merge(module.name.tags, var.tags)

  use_sso  = contains(var.authentication_providers, "AWS_SSO")
  use_saml = contains(var.authentication_providers, "SAML")

  sso_admins = local.use_sso && (length(var.admin_sso_user_ids) + length(var.admin_sso_group_ids)) > 0
}

# -----------------------------------------------------------------------------
# Workspace role.
# -----------------------------------------------------------------------------

resource "aws_iam_role" "grafana" {
  name = "${var.project}-grafana"
  path = try(var.iam_scope.path, "/service-role/")

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "grafana.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = {
        StringEquals = { "aws:SourceAccount" = data.aws_caller_identity.current.account_id }
      }
    }]
  })

  permissions_boundary = try(var.iam_scope.permissions_boundary, null)
  tags                 = local.tags
}

# The read actions of the AWS managed AmazonGrafanaCloudWatchAccess
# policy, without tag:GetResources (tag-based resource filters), which
# the scoped workload boundary does not allow.
resource "aws_iam_role_policy" "cloudwatch" {
  name = "${var.project}-grafana-cloudwatch"
  role = aws_iam_role.grafana.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "CloudWatchRead"
        Effect = "Allow"
        Action = [
          "cloudwatch:DescribeAlarmHistory",
          "cloudwatch:DescribeAlarms",
          "cloudwatch:DescribeAlarmsForMetric",
          "cloudwatch:GetInsightRuleReport",
          "cloudwatch:GetMetricData",
          "cloudwatch:ListMetrics",
        ]
        Resource = "*"
      },
      {
        Sid    = "LogsRead"
        Effect = "Allow"
        Action = [
          "logs:DescribeLogGroups",
          "logs:GetLogEvents",
          "logs:GetLogGroupFields",
          "logs:GetQueryResults",
          "logs:StartQuery",
          "logs:StopQuery",
        ]
        Resource = "*"
      },
      {
        Sid      = "EC2Describe"
        Effect   = "Allow"
        Action   = ["ec2:DescribeInstances", "ec2:DescribeRegions", "ec2:DescribeTags"]
        Resource = "*"
      },
    ]
  })
}

# -----------------------------------------------------------------------------
# Workspace.
# -----------------------------------------------------------------------------

resource "aws_grafana_workspace" "main" {
  name                     = "${var.project}-grafana"
  description              = "Grafana for ${var.project}"
  account_access_type      = "CURRENT_ACCOUNT"
  authentication_providers = var.authentication_providers
  permission_type          = "CUSTOMER_MANAGED"
  role_arn                 = aws_iam_role.grafana.arn
  data_sources             = ["CLOUDWATCH"]
  grafana_version          = var.grafana_version

  tags = local.tags

  depends_on = [aws_iam_role_policy.cloudwatch]

  lifecycle {
    precondition {
      condition     = local.use_saml ? var.saml_idp_metadata_url != "" && length(var.saml_editor_role_values) > 0 : true
      error_message = "SAML login needs saml_idp_metadata_url and at least one saml_editor_role_values entry."
    }
  }
}

resource "aws_grafana_role_association" "admin" {
  count = local.sso_admins ? 1 : 0

  workspace_id = aws_grafana_workspace.main.id
  role         = "ADMIN"
  user_ids     = var.admin_sso_user_ids
  group_ids    = var.admin_sso_group_ids
}

resource "aws_grafana_workspace_saml_configuration" "main" {
  count = local.use_saml ? 1 : 0

  workspace_id       = aws_grafana_workspace.main.id
  idp_metadata_url   = var.saml_idp_metadata_url
  role_assertion     = var.saml_role_assertion
  admin_role_values  = var.saml_admin_role_values
  editor_role_values = var.saml_editor_role_values
}
