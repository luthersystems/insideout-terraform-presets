variable "project" {
  description = "Project name used for resource naming"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name (e.g., prod, staging, dev)"
  type        = string
  default     = "prod"
}

variable "vpc_id" {
  description = "VPC ID for Service Connect namespace"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs (available for downstream ECS services)"
  type        = list(string)
  default     = []
}

variable "public_subnet_ids" {
  description = "Public subnet IDs (accepted for wiring compatibility, not used by cluster)"
  type        = list(string)
  default     = []
}

variable "enable_container_insights" {
  description = "Enable CloudWatch Container Insights for the ECS cluster"
  type        = bool
  default     = true
}

variable "capacity_providers" {
  description = "List of capacity providers for the cluster"
  type        = list(string)
  default     = ["FARGATE", "FARGATE_SPOT"]

  validation {
    condition = length(var.capacity_providers) > 0 && length([
      for p in var.capacity_providers : p
      if contains(["FARGATE", "FARGATE_SPOT"], p)
    ]) == length(var.capacity_providers)
    error_message = "capacity_providers must contain at least one value and every value must be one of: FARGATE, FARGATE_SPOT."
  }
}

variable "default_capacity_provider" {
  description = "Default capacity provider for the cluster"
  type        = string
  default     = "FARGATE"

  validation {
    condition     = contains(["FARGATE", "FARGATE_SPOT"], var.default_capacity_provider)
    error_message = "default_capacity_provider must be one of: FARGATE, FARGATE_SPOT."
  }
}

variable "enable_service_connect" {
  description = "Create a Cloud Map namespace for ECS Service Connect"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
