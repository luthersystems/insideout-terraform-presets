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
  description = "Project/prefix for resource names"
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

variable "vpc_id" {
  description = "VPC ID for the Redis security group"
  type        = string
  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string."
  }
}

variable "cache_subnet_ids" {
  description = "Private subnet IDs for ElastiCache (min 2 AZs recommended)"
  type        = list(string)
  validation {
    condition     = length(var.cache_subnet_ids) >= 2
    error_message = "Provide at least 2 subnets (ideally across AZs) for high availability."
  }
}

variable "allowed_cidr_blocks" {
  description = "CIDR blocks permitted to connect to Redis (6379)"
  type        = list(string)
  default     = ["10.0.0.0/16"]
  validation {
    condition     = length(var.allowed_cidr_blocks) >= 1
    error_message = "allowed_cidr_blocks must contain at least one CIDR."
  }
}

# Sizing
variable "node_type" {
  description = "ElastiCache node type (cache.*). cache.r6g.xlarge ~= 4 vCPU"
  type        = string
  default     = "cache.r6g.xlarge"
  validation {
    condition     = length(trimspace(var.node_type)) > 0
    error_message = "node_type must be a non-empty string (e.g., cache.r6g.large)."
  }
}

variable "engine_version" {
  description = "Redis engine version"
  type        = string
  default     = "7.1"
  validation {
    condition     = length(trimspace(var.engine_version)) > 0
    error_message = "engine_version must be a non-empty string (e.g., 7.1)."
  }
}

# HA / replicas
variable "ha" {
  description = "Enable Multi-AZ automatic failover"
  type        = bool
  default     = false
}

variable "replicas" {
  description = "Number of read replicas (per primary)"
  type        = number
  default     = 1
  validation {
    condition     = var.replicas >= 0
    error_message = "replicas must be >= 0."
  }
}

# Maintenance / snapshots
variable "maintenance_window" {
  description = "Preferred maintenance window (UTC)"
  type        = string
  default     = "sun:05:00-sun:06:00"
  validation {
    condition     = length(trimspace(var.maintenance_window)) > 0
    error_message = "maintenance_window must be a non-empty string."
  }
}

variable "snapshot_window" {
  description = "Preferred snapshot window (UTC)"
  type        = string
  default     = "03:00-04:00"
  validation {
    condition     = length(trimspace(var.snapshot_window)) > 0
    error_message = "snapshot_window must be a non-empty string."
  }
}

variable "snapshot_retention_days" {
  description = "Number of daily snapshots to retain"
  type        = number
  default     = 7
  validation {
    condition     = var.snapshot_retention_days >= 0
    error_message = "snapshot_retention_days must be >= 0."
  }
}

variable "apply_immediately" {
  type    = bool
  default = true
}

variable "enable_cloudwatch_logs" {
  description = "Enable Redis log delivery to CloudWatch Logs"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Extra tags"
  type        = map(string)
  default     = {}
}
