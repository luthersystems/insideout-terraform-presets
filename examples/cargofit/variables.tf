variable "alb_project" {
  type = string
}

variable "alb_region" {
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

variable "ec2_cluster_name" {
  type = string
}

variable "ec2_desired_size" {
  type = number
}

variable "ec2_instance_types" {
  type = list(string)
}

variable "ec2_max_size" {
  type = number
}

variable "ec2_min_size" {
  type = number
}

variable "ec2_project" {
  type = string
}

variable "ec2_region" {
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

variable "resource_memory_size" {
  type = number
}

variable "resource_project" {
  type = string
}

variable "resource_region" {
  type = string
}

variable "resource_runtime" {
  type = string
}

variable "resource_timeout" {
  type = number
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

variable "vpc_project" {
  type = string
}

variable "vpc_region" {
  type = string
}

variable "waf_project" {
  type = string
}
