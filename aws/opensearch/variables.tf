variable "project" {
  type        = string
  description = "Project name for resource naming"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "region" {
  type        = string
  description = "AWS region"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID for OpenSearch domain"
}

variable "subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs for OpenSearch domain"
}

variable "deployment_type" {
  type        = string
  description = "Deployment type (Managed or Serverless)"
  default     = "managed"
}

variable "instance_type" {
  type        = string
  description = "OpenSearch instance type"
  default     = "t3.medium.search"
}

variable "storage_size" {
  type        = string
  description = "Storage size in GB"
  default     = "10GB"
}

variable "multi_az" {
  type        = bool
  description = "Whether to enable Multi-AZ deployment"
  default     = false
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

