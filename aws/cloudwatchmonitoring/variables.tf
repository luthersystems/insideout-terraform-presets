variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project/prefix used for names"
  type        = string
  default     = "demo"
  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

# IDs wired from other modules (all optional)
variable "instance_ids" {
  description = "EC2 instance IDs to monitor (bastion/VMs)"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.instance_ids : length(trimspace(s)) > 0])
    error_message = "instance_ids entries must be non-empty strings."
  }
}

variable "rds_instance_ids" {
  description = "RDS DB instance identifiers to monitor"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.rds_instance_ids : length(trimspace(s)) > 0])
    error_message = "rds_instance_ids entries must be non-empty strings."
  }
}

variable "elasticache_replication_group_ids" {
  description = "ElastiCache replication group IDs to monitor"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.elasticache_replication_group_ids : length(trimspace(s)) > 0])
    error_message = "elasticache_replication_group_ids entries must be non-empty strings."
  }
}

variable "msk_cluster_arns" {
  description = "MSK cluster ARNs to include on dashboard"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.msk_cluster_arns : length(trimspace(s)) > 0])
    error_message = "msk_cluster_arns entries must be non-empty strings."
  }
}

variable "alb_arn_suffixes" {
  description = "Application Load Balancer ARN suffixes for metrics"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.alb_arn_suffixes : length(trimspace(s)) > 0])
    error_message = "alb_arn_suffixes entries must be non-empty strings."
  }
}

variable "sqs_queue_arns" {
  description = "SQS queue ARNs to monitor for backlog"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.sqs_queue_arns : length(trimspace(s)) > 0])
    error_message = "sqs_queue_arns entries must be non-empty strings."
  }
}

# Notifications / thresholds
variable "alarm_emails" {
  description = "Email addresses to subscribe to the alarm SNS topic"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for s in var.alarm_emails : length(trimspace(s)) > 0])
    error_message = "alarm_emails entries must be non-empty strings."
  }
}

variable "cpu_high_threshold" {
  description = "CPU utilization threshold (%) for EC2/RDS/Redis alarms"
  type        = number
  default     = 80
  validation {
    condition     = var.cpu_high_threshold >= 0 && var.cpu_high_threshold <= 100
    error_message = "cpu_high_threshold must be between 0 and 100."
  }
}

variable "rds_free_storage_gb_threshold" {
  description = "Free storage threshold (GB) for RDS alarm"
  type        = number
  default     = 10
  validation {
    condition     = var.rds_free_storage_gb_threshold >= 0
    error_message = "rds_free_storage_gb_threshold must be >= 0."
  }
}

variable "sqs_backlog_threshold" {
  description = "Backlog (visible messages) threshold per queue"
  type        = number
  default     = 1000
  validation {
    condition     = var.sqs_backlog_threshold >= 0
    error_message = "sqs_backlog_threshold must be >= 0."
  }
}

variable "eval_periods" {
  description = "Evaluation periods for metric alarms"
  type        = number
  default     = 2
  validation {
    condition     = var.eval_periods >= 1
    error_message = "eval_periods must be >= 1."
  }
}

variable "period" {
  description = "Metric period in seconds"
  type        = number
  default     = 300
  validation {
    condition     = var.period >= 60
    error_message = "period must be at least 60 seconds."
  }
}

variable "tags" {
  description = "Tags to add to supported resources"
  type        = map(string)
  default     = {}
}
