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
  subcomponent   = "cwm"
  resource       = "cwm"
}


locals {
  alarm_name_prefix = module.name.name
}

# -----------------------------------------------------------------------------
# Alarm notifications
# -----------------------------------------------------------------------------
resource "aws_sns_topic" "alarms" {
  name = "${module.name.name}-alarms"
  tags = merge(module.name.tags, var.tags)
}

resource "aws_sns_topic_subscription" "emails" {
  for_each  = toset(var.alarm_emails)
  topic_arn = aws_sns_topic.alarms.arn
  protocol  = "email"
  endpoint  = each.value
}

# -----------------------------------------------------------------------------
# EC2 CPU alarms (bastion/VMs)
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_metric_alarm" "ec2_cpu_high" {
  # Use stable keys (indices) so the set of instances is known at plan time
  for_each = { for i in tolist(range(length(var.instance_ids))) : i => true }

  alarm_name                = "${local.alarm_name_prefix}-ec2-cpu-${replace(var.instance_ids[tonumber(each.key)], "/[^a-zA-Z0-9._-]/", "-")}"
  comparison_operator       = "GreaterThanThreshold"
  evaluation_periods        = var.eval_periods
  threshold                 = var.cpu_high_threshold
  metric_name               = "CPUUtilization"
  namespace                 = "AWS/EC2"
  period                    = var.period
  statistic                 = "Average"
  treat_missing_data        = "notBreaching"
  alarm_description         = "High CPU on EC2 instance ${var.instance_ids[tonumber(each.key)]}"
  dimensions                = { InstanceId = var.instance_ids[tonumber(each.key)] }
  alarm_actions             = [aws_sns_topic.alarms.arn]
  ok_actions                = [aws_sns_topic.alarms.arn]
  insufficient_data_actions = []
  tags                      = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# RDS alarms (CPU and FreeStorageSpace)
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_metric_alarm" "rds_cpu_high" {
  # Same index-key trick to avoid unknown keys at plan time
  for_each = { for i in tolist(range(length(var.rds_instance_ids))) : i => true }

  alarm_name                = "${local.alarm_name_prefix}-rds-cpu-${var.rds_instance_ids[tonumber(each.key)]}"
  comparison_operator       = "GreaterThanThreshold"
  evaluation_periods        = var.eval_periods
  threshold                 = var.cpu_high_threshold
  metric_name               = "CPUUtilization"
  namespace                 = "AWS/RDS"
  period                    = var.period
  statistic                 = "Average"
  treat_missing_data        = "notBreaching"
  alarm_description         = "High CPU on RDS instance ${var.rds_instance_ids[tonumber(each.key)]}"
  dimensions                = { DBInstanceIdentifier = var.rds_instance_ids[tonumber(each.key)] }
  alarm_actions             = [aws_sns_topic.alarms.arn]
  ok_actions                = [aws_sns_topic.alarms.arn]
  insufficient_data_actions = []
  tags                      = merge(module.name.tags, var.tags)
}

resource "aws_cloudwatch_metric_alarm" "rds_free_storage_low" {
  for_each = { for i in tolist(range(length(var.rds_instance_ids))) : i => true }

  alarm_name                = "${local.alarm_name_prefix}-rds-freestorage-${var.rds_instance_ids[tonumber(each.key)]}"
  comparison_operator       = "LessThanThreshold"
  evaluation_periods        = var.eval_periods
  threshold                 = var.rds_free_storage_gb_threshold * 1024 * 1024 * 1024
  metric_name               = "FreeStorageSpace"
  namespace                 = "AWS/RDS"
  period                    = var.period
  statistic                 = "Average"
  treat_missing_data        = "notBreaching"
  alarm_description         = "Low free storage on RDS instance ${var.rds_instance_ids[tonumber(each.key)]}"
  dimensions                = { DBInstanceIdentifier = var.rds_instance_ids[tonumber(each.key)] }
  alarm_actions             = [aws_sns_topic.alarms.arn]
  ok_actions                = [aws_sns_topic.alarms.arn]
  insufficient_data_actions = []
  tags                      = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# ElastiCache / Redis CPU alarm
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_metric_alarm" "redis_cpu_high" {
  for_each = { for i in tolist(range(length(var.elasticache_replication_group_ids))) : i => true }

  alarm_name                = "${local.alarm_name_prefix}-redis-cpu-${var.elasticache_replication_group_ids[tonumber(each.key)]}"
  comparison_operator       = "GreaterThanThreshold"
  evaluation_periods        = var.eval_periods
  threshold                 = var.cpu_high_threshold
  metric_name               = "CPUUtilization"
  namespace                 = "AWS/ElastiCache"
  period                    = var.period
  statistic                 = "Average"
  treat_missing_data        = "notBreaching"
  alarm_description         = "High CPU on Redis RG ${var.elasticache_replication_group_ids[tonumber(each.key)]}"
  dimensions                = { CacheClusterId = var.elasticache_replication_group_ids[tonumber(each.key)] } # works per-node
  alarm_actions             = [aws_sns_topic.alarms.arn]
  ok_actions                = [aws_sns_topic.alarms.arn]
  insufficient_data_actions = []
  tags                      = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# SQS backlog alarm (visible messages)
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_metric_alarm" "sqs_backlog" {
  # Use stable keys (indices) to avoid unknown set keys
  for_each = { for i in tolist(range(length(var.sqs_queue_arns))) : i => true }

  alarm_name          = "${local.alarm_name_prefix}-sqs-backlog-${replace(var.sqs_queue_arns[tonumber(each.key)], "/[:]/", "-")}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = var.eval_periods
  threshold           = var.sqs_backlog_threshold
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = var.period
  statistic           = "Average"
  treat_missing_data  = "notBreaching"
  alarm_description   = "SQS backlog high"
  dimensions = {
    QueueName = split(":", var.sqs_queue_arns[tonumber(each.key)])[length(split(":", var.sqs_queue_arns[tonumber(each.key)])) - 1]
  }
  alarm_actions             = [aws_sns_topic.alarms.arn]
  ok_actions                = [aws_sns_topic.alarms.arn]
  insufficient_data_actions = []
  tags                      = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# Dashboard (EC2, RDS, Redis, ALB, MSK)
# -----------------------------------------------------------------------------
locals {
  dash_metrics = concat(
    length(var.instance_ids) > 0 ? [
      {
        "type" : "metric", "x" : 0, "y" : 0, "w" : 24, "h" : 6,
        "properties" : {
          "title" : "EC2 CPU", "region" : var.region, "period" : var.period, "stat" : "Average",
          "metrics" : concat([["AWS/EC2", "CPUUtilization", { "stat" : "Average" }]],
            [for id in var.instance_ids : ["AWS/EC2", "CPUUtilization", "InstanceId", id, { "stat" : "Average" }]]
          )
        }
      }
    ] : [],
    length(var.rds_instance_ids) > 0 ? [
      {
        "type" : "metric", "x" : 0, "y" : 6, "w" : 24, "h" : 6,
        "properties" : {
          "title" : "RDS CPU", "region" : var.region, "period" : var.period, "stat" : "Average",
          "metrics" : concat([["AWS/RDS", "CPUUtilization", { "stat" : "Average" }]],
            [for id in var.rds_instance_ids : ["AWS/RDS", "CPUUtilization", "DBInstanceIdentifier", id, { "stat" : "Average" }]]
          )
        }
      },
      {
        "type" : "metric", "x" : 0, "y" : 12, "w" : 24, "h" : 6,
        "properties" : {
          "title" : "RDS FreeStorage", "region" : var.region, "period" : var.period, "stat" : "Average",
          "metrics" : concat([["AWS/RDS", "FreeStorageSpace", { "stat" : "Average" }]],
            [for id in var.rds_instance_ids : ["AWS/RDS", "FreeStorageSpace", "DBInstanceIdentifier", id, { "stat" : "Average" }]]
          )
        }
      }
    ] : [],
    length(var.elasticache_replication_group_ids) > 0 ? [
      {
        "type" : "metric", "x" : 0, "y" : 18, "w" : 24, "h" : 6,
        "properties" : {
          "title" : "Redis CPU", "region" : var.region, "period" : var.period, "stat" : "Average",
          "metrics" : concat([["AWS/ElastiCache", "CPUUtilization", { "stat" : "Average" }]],
            [for rg in var.elasticache_replication_group_ids : ["AWS/ElastiCache", "CPUUtilization", "ReplicationGroupId", rg, { "stat" : "Average" }]]
          )
        }
      }
    ] : [],
    length(var.alb_arn_suffixes) > 0 ? [
      {
        "type" : "metric", "x" : 0, "y" : 24, "w" : 24, "h" : 6,
        "properties" : {
          "title" : "ALB 5XX & Latency", "region" : var.region, "period" : var.period, "stat" : "Sum",
          "metrics" : concat(
            [["AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", { "stat" : "Sum" }]],
            [for s in var.alb_arn_suffixes : ["AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", "LoadBalancer", s, { "stat" : "Sum" }]],
            [[".", "TargetResponseTime", { "stat" : "Average" }]],
            [for s in var.alb_arn_suffixes : ["AWS/ApplicationELB", "TargetResponseTime", "LoadBalancer", s, { "stat" : "Average" }]]
          )
        }
      }
    ] : [],
    length(var.msk_cluster_arns) > 0 ? [
      {
        "type" : "metric", "x" : 0, "y" : 30, "w" : 24, "h" : 6,
        "properties" : {
          "title" : "MSK Bytes In/Out (per broker)", "region" : var.region, "period" : var.period, "stat" : "Average",
          "metrics" : concat(
            [["AWS/Kafka", "BytesInPerSec", { "stat" : "Average" }]],
            [for a in var.msk_cluster_arns : ["AWS/Kafka", "BytesInPerSec", "Cluster Name", a, { "stat" : "Average" }]],
            [[".", "BytesOutPerSec", { "stat" : "Average" }]],
            [for a in var.msk_cluster_arns : ["AWS/Kafka", "BytesOutPerSec", "Cluster Name", a, { "stat" : "Average" }]]
          )
        }
      }
    ] : []
  )
}

resource "aws_cloudwatch_dashboard" "main" {
  dashboard_name = module.name.name
  dashboard_body = jsonencode({ widgets = local.dash_metrics })
}
