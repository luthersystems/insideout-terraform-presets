variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  default     = "sandbox"
}

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

variable "bedrock_project" {
  type = string
}

variable "bedrock_region" {
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

variable "dynamodb_billing_mode" {
  type = string
}

variable "dynamodb_project" {
  type = string
}

variable "dynamodb_region" {
  type = string
}

variable "aws_eks_nodegroup_desired_size" {
  type = number
}

variable "aws_eks_nodegroup_instance_types" {
  type = list(string)
}

variable "aws_eks_nodegroup_max_size" {
  type = number
}

variable "aws_eks_nodegroup_min_size" {
  type = number
}

variable "aws_eks_nodegroup_project" {
  type = string
}

variable "aws_eks_nodegroup_region" {
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

variable "elasticache_replicas" {
  type = number
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

# tflint-ignore: terraform_unused_declarations  # composer always wires var.region at the root (CLAUDE.md mandate)
variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "aws_eks_project" {
  type = string
}

variable "aws_eks_region" {
  type = string
}

variable "s3_project" {
  type = string
}

variable "s3_region" {
  type = string
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
