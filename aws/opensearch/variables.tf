variable "project" {
  type        = string
  description = "Project name for resource naming. Used as a prefix for AOSS collection and security policy names, which are capped at 32 chars; longest suffix is '-search' (7), so project must be ≤25."
  validation {
    condition     = length(trimspace(var.project)) > 0 && length(var.project) <= 25
    error_message = "project must be a non-empty string ≤25 characters (AOSS collection and security policy names are capped at 32 chars and this module appends up to 7 chars of suffix)."
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

variable "kms_key_arn" {
  type        = string
  description = "Optional KMS key ARN for the AOSS encryption security policy. If null (default), the AWS-owned AOSS key is used. Serverless mode only."
  default     = null
}

variable "allow_public_access" {
  type        = bool
  description = "AOSS network security policy: when true (default), the collection and dashboards are reachable from the public internet. Set false only if the stack provisions an aws_opensearchserverless_vpc_endpoint (not included in this module). Serverless mode only."
  default     = true
}
