variable "region" {
  description = "AWS Region"
  type        = string
  default     = "us-east-1"
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project slug for names/tags"
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

variable "cluster_name" {
  description = "Override the MSK cluster name"
  type        = string
  default     = null
  validation {
    condition     = var.cluster_name == null ? true : length(trimspace(var.cluster_name)) > 0
    error_message = "cluster_name, if set, must be a non-empty string."
  }
}

variable "vpc_id" {
  description = "VPC where brokers live"
  type        = string
  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string."
  }
}

variable "subnet_ids" {
  description = "Private subnet IDs (>= 2 AZs, ideally 3)"
  type        = list(string)
  validation {
    condition     = length(var.subnet_ids) >= 2 && alltrue([for s in var.subnet_ids : length(trimspace(s)) > 0])
    error_message = "Provide at least 2 subnet_ids and ensure none are empty."
  }
}

variable "kafka_version" {
  description = "Kafka version for MSK"
  type        = string
  default     = "3.6.0"
  validation {
    condition     = length(trimspace(var.kafka_version)) > 0
    error_message = "kafka_version must be a non-empty string (e.g., 3.6.0)."
  }
}

variable "broker_instance_type" {
  description = "Broker instance size"
  type        = string
  default     = "kafka.m5.large"
  validation {
    condition     = length(trimspace(var.broker_instance_type)) > 0
    error_message = "broker_instance_type must be a non-empty string."
  }
}

variable "number_of_broker_nodes" {
  description = "Total broker nodes (must be multiple of number of AZs)"
  type        = number
  default     = 3
  validation {
    condition     = var.number_of_broker_nodes >= 2
    error_message = "number_of_broker_nodes must be >= 2."
  }
  # Note: multiple-of-AZs constraint is enforced in main using locals/count, not here.
}

variable "broker_ebs_volume_size" {
  description = "EBS volume size per broker (GiB)"
  type        = number
  default     = 100
  validation {
    condition     = var.broker_ebs_volume_size >= 1
    error_message = "broker_ebs_volume_size must be >= 1 GiB."
  }
}

variable "client_cidr_blocks" {
  description = "CIDR ranges allowed to connect to brokers"
  type        = list(string)
  default     = ["10.0.0.0/8"]
  validation {
    condition     = alltrue([for c in var.client_cidr_blocks : length(trimspace(c)) > 0])
    error_message = "client_cidr_blocks entries must be non-empty strings."
  }
}

variable "allow_plaintext" {
  description = "Allow plaintext client connections on 9092 (TLS is recommended)"
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "KMS key for at-rest encryption (omit for AWS-managed key)"
  type        = string
  default     = null
  validation {
    condition     = var.kms_key_arn == null ? true : length(trimspace(var.kms_key_arn)) > 0
    error_message = "kms_key_arn, if set, must be a non-empty string."
  }
}

variable "enable_cloudwatch_logs" {
  description = "Send broker logs to CloudWatch Logs"
  type        = bool
  default     = true
}

variable "cloudwatch_retention_days" {
  description = "CloudWatch Logs retention in days"
  type        = number
  default     = 14
  validation {
    condition     = var.cloudwatch_retention_days >= 1
    error_message = "cloudwatch_retention_days must be >= 1."
  }
}

variable "enhanced_monitoring" {
  description = "MSK enhanced monitoring level: DEFAULT | PER_BROKER | PER_TOPIC_PER_BROKER"
  type        = string
  default     = "DEFAULT"
  validation {
    condition     = contains(["DEFAULT", "PER_BROKER", "PER_TOPIC_PER_BROKER"], var.enhanced_monitoring)
    error_message = "enhanced_monitoring must be one of: DEFAULT, PER_BROKER, PER_TOPIC_PER_BROKER."
  }
}

variable "tags" {
  description = "Additional resource tags"
  type        = map(string)
  default     = {}
}
