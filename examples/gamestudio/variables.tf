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

variable "bedrock_project" {
  type = string
}

variable "bedrock_region" {
  type = string
}

variable "cloudwatchlogs_project" {
  type = string
}

variable "cloudwatchlogs_region" {
  type = string
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

variable "dynamodb_billing_mode" {
  type = string
}

variable "dynamodb_project" {
  type = string
}

variable "dynamodb_region" {
  type = string
}

variable "githubactions_project" {
  type = string
}

variable "lambda_memory_size" {
  type = number
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

variable "lambda_timeout" {
  type = number
}

variable "opensearch_project" {
  type = string
}

variable "opensearch_region" {
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

variable "secretsmanager_num_secrets" {
  type = number
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
