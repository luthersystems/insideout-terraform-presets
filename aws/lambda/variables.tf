variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "project" {
  description = "Name/prefix for resources"
  type        = string
  default     = "demo"
}

variable "runtime" {
  description = "Lambda runtime (e.g., nodejs20.x, python3.12, go1.x, java21)"
  type        = string
  default     = "nodejs20.x"
}

variable "memory_size" {
  description = "Memory size in MB"
  type        = number
  default     = 128
}

variable "timeout" {
  description = "Timeout in seconds"
  type        = number
  default     = 3
}

variable "handler" {
  description = "Lambda handler (e.g., index.handler)"
  type        = string
  default     = "index.handler"
}

variable "vpc_id" {
  description = "Optional VPC ID for Lambda VPC access"
  type        = string
  default     = null
}

variable "subnet_ids" {
  description = "Subnet IDs for VPC access"
  type        = list(string)
  default     = []
}

variable "security_group_ids" {
  description = "Security Group IDs for VPC access"
  type        = list(string)
  default     = []
}

variable "environment_variables" {
  description = "Environment variables for Lambda"
  type        = map(string)
  default     = {}
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 14
}

variable "tags" {
  description = "Extra resource tags"
  type        = map(string)
  default     = {}
}
