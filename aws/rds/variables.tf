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
  description = "Name/prefix for resources"
  type        = string
  default     = "demo"
  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "vpc_id" {
  description = "VPC ID for the RDS security group"
  type        = string
  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be provided."
  }
}

variable "subnet_ids" {
  description = "Private subnet IDs for RDS (at least 2 in different AZs)"
  type        = list(string)
  validation {
    condition     = length(var.subnet_ids) >= 2
    error_message = "Provide at least two private subnets (in different AZs) for RDS."
  }
}

variable "allowed_cidr_blocks" {
  description = "CIDR blocks permitted to connect to Postgres (5432)"
  type        = list(string)
  default     = ["10.0.0.0/16"]
  validation {
    condition     = length(var.allowed_cidr_blocks) >= 1
    error_message = "allowed_cidr_blocks must include at least one CIDR."
  }
}

# Engine / sizing
variable "engine_version" {
  description = "PostgreSQL engine version; leave null to set major only (e.g., 15) so AWS picks preferred minor."
  type        = string
  default     = null
}

variable "instance_class" {
  description = "Instance class (e.g., db.m7i.2xlarge)"
  type        = string
  default     = "db.t3.medium"
  validation {
    condition     = length(trimspace(var.instance_class)) > 0
    error_message = "instance_class must be a non-empty string (e.g., \"db.m7i.2xlarge\")."
  }
}

variable "allocated_storage" {
  description = "Initial storage (GB)"
  type        = number
  default     = 200
  validation {
    condition     = var.allocated_storage >= 20
    error_message = "allocated_storage must be >= 20 GB."
  }
}

variable "max_allocated_storage" {
  description = "Autoscaling storage limit (GB)"
  type        = number
  default     = 1000
  validation {
    condition     = var.max_allocated_storage >= 0
    error_message = "max_allocated_storage must be >= 0."
  }
}

variable "storage_type" {
  description = "gp2 | gp3 | io1"
  type        = string
  default     = "gp3"
  validation {
    condition     = contains(["gp2", "gp3", "io1"], var.storage_type)
    error_message = "storage_type must be one of: gp2, gp3, io1."
  }
}

variable "storage_encrypted" {
  description = "Enable storage encryption"
  type        = bool
  default     = true
}

variable "kms_key_id" {
  description = "Optional KMS key ARN for encryption (if not default)"
  type        = string
  default     = null
}

# Connectivity / behavior
variable "publicly_accessible" {
  description = "Whether the DB is publicly accessible"
  type        = bool
  default     = false
}

variable "multi_az" {
  description = "Enable Multi-AZ for the primary"
  type        = bool
  default     = false
}

variable "read_replica_count" {
  description = "Number of read replicas to create"
  type        = number
  default     = 1
  validation {
    condition     = var.read_replica_count >= 0
    error_message = "read_replica_count must be >= 0."
  }
}

# Backups / maintenance
variable "backup_retention_days" {
  description = "Backup retention (days) — must be >0 to support replicas"
  type        = number
  default     = 7
  validation {
    condition     = var.backup_retention_days >= 0
    error_message = "backup_retention_days must be >= 0 (use > 0 when replicas are enabled)."
  }
}

variable "backup_window" {
  description = "Preferred backup window (UTC, e.g., 03:00-04:00)"
  type        = string
  default     = "03:00-04:00"
  validation {
    condition     = length(trimspace(var.backup_window)) > 0
    error_message = "backup_window must be a non-empty string (UTC window)."
  }
}

variable "maintenance_window" {
  description = "Preferred maintenance window (UTC)"
  type        = string
  default     = "sun:05:00-sun:06:00"
  validation {
    condition     = length(trimspace(var.maintenance_window)) > 0
    error_message = "maintenance_window must be a non-empty string (UTC window)."
  }
}

# Lifecycle
variable "deletion_protection" {
  type        = bool
  default     = false
  description = "Enable deletion protection on the primary instance"
}

variable "skip_final_snapshot" {
  type        = bool
  default     = true
  description = "Skip final snapshot on deletion (use with caution)"
}

variable "apply_immediately" {
  type        = bool
  default     = true
  description = "Apply modifications immediately"
}

# DB auth
variable "username" {
  description = "Master username"
  type        = string
  default     = "app"
  validation {
    condition     = length(trimspace(var.username)) > 0
    error_message = "username must be a non-empty string."
  }
}

variable "database_name" {
  description = "Initial database name"
  type        = string
  default     = "appdb"
  validation {
    condition     = length(trimspace(var.database_name)) > 0
    error_message = "database_name must be a non-empty string."
  }
}

variable "tags" {
  description = "Extra resource tags"
  type        = map(string)
  default     = {}
}

variable "enable_cloudwatch_logs" {
  description = "Export Postgres logs to CloudWatch Logs"
  type        = bool
  default     = false
}

variable "cloudwatch_logs_exports" {
  description = "RDS log types to export (e.g., [\"postgresql\",\"upgrade\",\"slowquery\"])"
  type        = list(string)
  default     = ["postgresql", "upgrade"]
  validation {
    condition     = length(var.cloudwatch_logs_exports) >= 0
    error_message = "cloudwatch_logs_exports must be a list (use empty to disable)."
  }
}
