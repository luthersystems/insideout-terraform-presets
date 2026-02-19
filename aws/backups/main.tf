terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9.1"
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
  subcomponent   = "bak"
  resource       = "bak"
}

# -----------------------------------------------------------------------------
# IAM role for AWS Backup service (with a short sleep to dodge IAM propagation)
# -----------------------------------------------------------------------------
data "aws_iam_policy_document" "backup_assume" {
  statement {
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["backup.amazonaws.com"]
    }
    actions = ["sts:AssumeRole"]
  }
}

resource "aws_iam_role" "backup" {
  # Backup rule names limited to 50 chars — use var.project throughout
  name               = "${var.project}-backup-role"
  assume_role_policy = data.aws_iam_policy_document.backup_assume.json
  tags               = merge(module.name.tags, var.tags)
}

# Give IAM a moment to propagate the new role before attaching managed policies
resource "time_sleep" "after_role_create" {
  depends_on      = [aws_iam_role.backup]
  create_duration = "20s"
}

resource "aws_iam_role_policy_attachment" "backup_service" {
  role       = aws_iam_role.backup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForBackup"
  depends_on = [time_sleep.after_role_create]
}

resource "aws_iam_role_policy_attachment" "restore" {
  role       = aws_iam_role.backup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForRestores"
  depends_on = [time_sleep.after_role_create]
}

# -----------------------------------------------------------------------------
# Backup vault (no KMS here; easy to add later if needed)
# -----------------------------------------------------------------------------
resource "aws_backup_vault" "this" {
  name = "${var.project}-vault"
  tags = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# Normalize inputs -> per-service resolved settings
# - We use 'schedule' in the plan (provider expects this), but accept
#   either 'schedule' or 'schedule_expression' from the caller.
# -----------------------------------------------------------------------------
locals {
  any_service_enabled = (
    var.enable_ec2_ebs ||
    var.enable_rds ||
    var.enable_dynamodb ||
    var.enable_s3
  )

  # Defaults, tolerant to missing keys
  base_defaults = {
    schedule_expression          = try(var.default_rule.schedule, try(var.default_rule.schedule_expression, "cron(0 5 ? * * *)"))
    schedule_expression_timezone = try(var.default_rule.schedule_expression_timezone, "Etc/UTC")
    start_window                 = try(var.default_rule.start_window, 60)
    completion_window            = try(var.default_rule.completion_window, 180)
    enable_continuous_backup     = try(var.default_rule.enable_continuous_backup, false)
    retention_days               = try(var.default_rule.retention_days, 90)
    cold_storage_after_days      = try(var.default_rule.cold_storage_after_days, 0)
    recovery_point_tags          = tomap(try(var.default_rule.recovery_point_tags, {}))
  }

  # Helper to build a normalized map for a service rule block
  # (merges defaults with service overrides + selection shape)
  ec2_ebs_norm = merge(local.base_defaults, {
    schedule_expression          = try(var.ec2_ebs_rule.schedule, try(var.ec2_ebs_rule.schedule_expression, local.base_defaults.schedule_expression))
    schedule_expression_timezone = try(var.ec2_ebs_rule.schedule_expression_timezone, local.base_defaults.schedule_expression_timezone)
    start_window                 = try(var.ec2_ebs_rule.start_window, local.base_defaults.start_window)
    completion_window            = try(var.ec2_ebs_rule.completion_window, local.base_defaults.completion_window)
    enable_continuous_backup     = try(var.ec2_ebs_rule.enable_continuous_backup, local.base_defaults.enable_continuous_backup)
    retention_days               = try(var.ec2_ebs_rule.retention_days, local.base_defaults.retention_days)
    cold_storage_after_days      = try(var.ec2_ebs_rule.cold_storage_after_days, local.base_defaults.cold_storage_after_days)
    recovery_point_tags          = tomap(try(var.ec2_ebs_rule.recovery_point_tags, local.base_defaults.recovery_point_tags))
    selection = {
      resource_arns  = try(var.ec2_ebs_rule.selection.resource_arns, [])
      selection_tags = try(var.ec2_ebs_rule.selection.selection_tags, [])
    }
  })

  rds_norm = merge(local.base_defaults, {
    schedule_expression          = try(var.rds_rule.schedule, try(var.rds_rule.schedule_expression, local.base_defaults.schedule_expression))
    schedule_expression_timezone = try(var.rds_rule.schedule_expression_timezone, local.base_defaults.schedule_expression_timezone)
    start_window                 = try(var.rds_rule.start_window, local.base_defaults.start_window)
    completion_window            = try(var.rds_rule.completion_window, local.base_defaults.completion_window)
    enable_continuous_backup     = try(var.rds_rule.enable_continuous_backup, local.base_defaults.enable_continuous_backup)
    retention_days               = try(var.rds_rule.retention_days, local.base_defaults.retention_days)
    cold_storage_after_days      = try(var.rds_rule.cold_storage_after_days, local.base_defaults.cold_storage_after_days)
    recovery_point_tags          = tomap(try(var.rds_rule.recovery_point_tags, local.base_defaults.recovery_point_tags))
    selection = {
      resource_arns  = try(var.rds_rule.selection.resource_arns, [])
      selection_tags = try(var.rds_rule.selection.selection_tags, [])
    }
  })

  dynamodb_norm = merge(local.base_defaults, {
    schedule_expression          = try(var.dynamodb_rule.schedule, try(var.dynamodb_rule.schedule_expression, local.base_defaults.schedule_expression))
    schedule_expression_timezone = try(var.dynamodb_rule.schedule_expression_timezone, local.base_defaults.schedule_expression_timezone)
    start_window                 = try(var.dynamodb_rule.start_window, local.base_defaults.start_window)
    completion_window            = try(var.dynamodb_rule.completion_window, local.base_defaults.completion_window)
    enable_continuous_backup     = try(var.dynamodb_rule.enable_continuous_backup, local.base_defaults.enable_continuous_backup)
    retention_days               = try(var.dynamodb_rule.retention_days, local.base_defaults.retention_days)
    cold_storage_after_days      = try(var.dynamodb_rule.cold_storage_after_days, local.base_defaults.cold_storage_after_days)
    recovery_point_tags          = tomap(try(var.dynamodb_rule.recovery_point_tags, local.base_defaults.recovery_point_tags))
    selection = {
      resource_arns  = try(var.dynamodb_rule.selection.resource_arns, [])
      selection_tags = try(var.dynamodb_rule.selection.selection_tags, [])
    }
  })

  s3_norm = merge(local.base_defaults, {
    schedule_expression          = try(var.s3_rule.schedule, try(var.s3_rule.schedule_expression, local.base_defaults.schedule_expression))
    schedule_expression_timezone = try(var.s3_rule.schedule_expression_timezone, local.base_defaults.schedule_expression_timezone)
    start_window                 = try(var.s3_rule.start_window, local.base_defaults.start_window)
    completion_window            = try(var.s3_rule.completion_window, local.base_defaults.completion_window)
    enable_continuous_backup     = try(var.s3_rule.enable_continuous_backup, local.base_defaults.enable_continuous_backup)
    retention_days               = try(var.s3_rule.retention_days, local.base_defaults.retention_days)
    cold_storage_after_days      = try(var.s3_rule.cold_storage_after_days, local.base_defaults.cold_storage_after_days)
    recovery_point_tags          = tomap(try(var.s3_rule.recovery_point_tags, local.base_defaults.recovery_point_tags))
    selection = {
      resource_arns  = try(var.s3_rule.selection.resource_arns, [])
      selection_tags = try(var.s3_rule.selection.selection_tags, [])
    }
  })
}

# -----------------------------------------------------------------------------
# Backup plan — explicit per-service rule blocks (stable order)
# Only add a rule block when that service is enabled.
# -----------------------------------------------------------------------------
resource "aws_backup_plan" "this" {
  name = "${var.project}-plan"

  # EC2/EBS rule
  dynamic "rule" {
    for_each = var.enable_ec2_ebs ? [1] : []
    content {
      rule_name                    = "${var.project}-ec2Ebs"
      target_vault_name            = aws_backup_vault.this.name
      schedule                     = local.ec2_ebs_norm.schedule_expression
      schedule_expression_timezone = local.ec2_ebs_norm.schedule_expression_timezone
      start_window                 = local.ec2_ebs_norm.start_window
      completion_window            = local.ec2_ebs_norm.completion_window
      enable_continuous_backup     = local.ec2_ebs_norm.enable_continuous_backup
      recovery_point_tags          = local.ec2_ebs_norm.recovery_point_tags

      lifecycle {
        delete_after = local.ec2_ebs_norm.retention_days
        # If cold_storage_after_days == 0, setting null effectively omits it.
        cold_storage_after = local.ec2_ebs_norm.cold_storage_after_days > 0 ? local.ec2_ebs_norm.cold_storage_after_days : null
      }
    }
  }

  # RDS rule
  dynamic "rule" {
    for_each = var.enable_rds ? [1] : []
    content {
      rule_name                    = "${var.project}-rds"
      target_vault_name            = aws_backup_vault.this.name
      schedule                     = local.rds_norm.schedule_expression
      schedule_expression_timezone = local.rds_norm.schedule_expression_timezone
      start_window                 = local.rds_norm.start_window
      completion_window            = local.rds_norm.completion_window
      enable_continuous_backup     = local.rds_norm.enable_continuous_backup
      recovery_point_tags          = local.rds_norm.recovery_point_tags

      lifecycle {
        delete_after       = local.rds_norm.retention_days
        cold_storage_after = local.rds_norm.cold_storage_after_days > 0 ? local.rds_norm.cold_storage_after_days : null
      }
    }
  }

  # DynamoDB rule
  dynamic "rule" {
    for_each = var.enable_dynamodb ? [1] : []
    content {
      rule_name                    = "${var.project}-dynamodb"
      target_vault_name            = aws_backup_vault.this.name
      schedule                     = local.dynamodb_norm.schedule_expression
      schedule_expression_timezone = local.dynamodb_norm.schedule_expression_timezone
      start_window                 = local.dynamodb_norm.start_window
      completion_window            = local.dynamodb_norm.completion_window
      enable_continuous_backup     = local.dynamodb_norm.enable_continuous_backup
      recovery_point_tags          = local.dynamodb_norm.recovery_point_tags

      lifecycle {
        delete_after       = local.dynamodb_norm.retention_days
        cold_storage_after = local.dynamodb_norm.cold_storage_after_days > 0 ? local.dynamodb_norm.cold_storage_after_days : null
      }
    }
  }

  # S3 rule
  dynamic "rule" {
    for_each = var.enable_s3 ? [1] : []
    content {
      rule_name                    = "${var.project}-s3"
      target_vault_name            = aws_backup_vault.this.name
      schedule                     = local.s3_norm.schedule_expression
      schedule_expression_timezone = local.s3_norm.schedule_expression_timezone
      start_window                 = local.s3_norm.start_window
      completion_window            = local.s3_norm.completion_window
      enable_continuous_backup     = local.s3_norm.enable_continuous_backup
      recovery_point_tags          = local.s3_norm.recovery_point_tags

      lifecycle {
        delete_after       = local.s3_norm.retention_days
        cold_storage_after = local.s3_norm.cold_storage_after_days > 0 ? local.s3_norm.cold_storage_after_days : null
      }
    }
  }

  tags = merge(module.name.tags, var.tags)

  lifecycle {
    precondition {
      condition     = local.any_service_enabled
      error_message = "At least one backup service must be enabled (enable_ec2_ebs, enable_rds, enable_dynamodb, or enable_s3)."
    }
  }
}

# -----------------------------------------------------------------------------
# Selections — 1 per enabled service
# -----------------------------------------------------------------------------

# EC2/EBS: usually picked by tag (e.g., backup=true), but accept explicit ARNs too
resource "aws_backup_selection" "ec2_ebs" {
  for_each     = var.enable_ec2_ebs ? toset(["on"]) : toset([])
  name         = "${var.project}-ec2Ebs"
  plan_id      = aws_backup_plan.this.id
  iam_role_arn = aws_iam_role.backup.arn

  # explicit resources list (can be empty)
  resources = local.ec2_ebs_norm.selection.resource_arns

  # allow multiple selection tags
  dynamic "selection_tag" {
    for_each = toset([for t in local.ec2_ebs_norm.selection.selection_tags : jsonencode(t)])
    content {
      key   = jsondecode(selection_tag.value).key
      type  = jsondecode(selection_tag.value).type
      value = jsondecode(selection_tag.value).value
    }
  }
}

# RDS: generally explicit DB instance ARNs
resource "aws_backup_selection" "rds" {
  for_each     = var.enable_rds ? toset(["on"]) : toset([])
  name         = "${var.project}-rds"
  plan_id      = aws_backup_plan.this.id
  iam_role_arn = aws_iam_role.backup.arn

  resources = local.rds_norm.selection.resource_arns

  dynamic "selection_tag" {
    for_each = toset([for t in local.rds_norm.selection.selection_tags : jsonencode(t)])
    content {
      key   = jsondecode(selection_tag.value).key
      type  = jsondecode(selection_tag.value).type
      value = jsondecode(selection_tag.value).value
    }
  }
}

# DynamoDB
resource "aws_backup_selection" "dynamodb" {
  for_each     = var.enable_dynamodb ? toset(["on"]) : toset([])
  name         = "${var.project}-dynamodb"
  plan_id      = aws_backup_plan.this.id
  iam_role_arn = aws_iam_role.backup.arn

  resources = local.dynamodb_norm.selection.resource_arns

  dynamic "selection_tag" {
    for_each = toset([for t in local.dynamodb_norm.selection.selection_tags : jsonencode(t)])
    content {
      key   = jsondecode(selection_tag.value).key
      type  = jsondecode(selection_tag.value).type
      value = jsondecode(selection_tag.value).value
    }
  }
}

# S3
resource "aws_backup_selection" "s3" {
  for_each     = var.enable_s3 ? toset(["on"]) : toset([])
  name         = "${var.project}-s3"
  plan_id      = aws_backup_plan.this.id
  iam_role_arn = aws_iam_role.backup.arn

  resources = local.s3_norm.selection.resource_arns

  dynamic "selection_tag" {
    for_each = toset([for t in local.s3_norm.selection.selection_tags : jsonencode(t)])
    content {
      key   = jsondecode(selection_tag.value).key
      type  = jsondecode(selection_tag.value).type
      value = jsondecode(selection_tag.value).value
    }
  }
}
