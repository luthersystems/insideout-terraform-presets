variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  default     = "sandbox"
}

variable "apigateway_project" {
  type = string
}

variable "apigateway_region" {
  type = string
}

variable "backups_default_rule" {
  type = any
}

variable "backups_project" {
  type = string
}

variable "backups_region" {
  type = string
}

variable "cloudwatchlogs_project" {
  type = string
}

variable "cloudwatchlogs_region" {
  type = string
}

variable "cloudwatchmonitoring_project" {
  type = string
}

variable "cloudwatchmonitoring_region" {
  type = string
}

variable "cognito_mfa_required" {
  type = bool
}

variable "cognito_project" {
  type = string
}

variable "cognito_region" {
  type = string
}

variable "cognito_sign_in_type" {
  type = string
}

variable "githubactions_project" {
  type = string
}

variable "kms_project" {
  type = string
}

variable "kms_region" {
  type = string
}

variable "lambda_project" {
  type = string
}

variable "lambda_region" {
  type = string
}

variable "lambda_runtime" {
  type = string
}

variable "project" {
  description = "Project name prefix"
  type        = string
  default     = ""
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "s3_project" {
  type = string
}

variable "s3_region" {
  type = string
}

variable "s3_versioning" {
  type = bool
}

variable "secretsmanager_project" {
  type = string
}

variable "secretsmanager_region" {
  type = string
}

variable "sqs_project" {
  type = string
}

variable "sqs_region" {
  type = string
}

variable "vpc_project" {
  type = string
}

variable "vpc_region" {
  type = string
}

variable "waf_project" {
  type = string
}
