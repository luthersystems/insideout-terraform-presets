variable "project" {
  type        = string
  description = "Project name for resource naming"
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

