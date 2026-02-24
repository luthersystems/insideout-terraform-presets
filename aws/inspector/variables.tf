variable "project" {
  description = "Name prefix for resources"
  type        = string
  default     = "demo"
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string

  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "insideout_project_id" {
  description = "InsideOut project UUID used to construct the deterministic role name (e.g. 78476478-06ca-4f4b-a325-3128a966df42)"
  type        = string

  validation {
    condition     = can(regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", var.insideout_project_id))
    error_message = "insideout_project_id must be a valid UUID."
  }
}

variable "terraform_sa_role_arn" {
  description = "ARN of the Terraform service account role trusted to assume this inspector role"
  type        = string

  validation {
    condition     = can(regex("^arn:aws:iam::", var.terraform_sa_role_arn))
    error_message = "terraform_sa_role_arn must be a valid IAM role ARN."
  }
}

variable "tags" {
  description = "Extra resource tags"
  type        = map(string)
  default     = {}
}
