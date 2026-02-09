variable "alb_project" {
  type = string
}

variable "alb_region" {
  type = string
}

variable "apigateway_project" {
  type = string
}

variable "apigateway_region" {
  type = string
}

variable "cloudfront_project" {
  type = string
}

variable "cloudfront_region" {
  type = string
}

variable "cloudwatchlogs_project" {
  type = string
}

variable "cloudwatchlogs_region" {
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

variable "elasticache_ha" {
  type = bool
}

variable "elasticache_project" {
  type = string
}

variable "elasticache_region" {
  type = string
}

variable "githubactions_project" {
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
  default     = "us-west-2"
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
