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

variable "aws_eks_nodegroup_cluster_name" {
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

variable "githubactions_project" {
  type = string
}

variable "aws_lambda_memory_size" {
  type = number
}

variable "aws_lambda_project" {
  type = string
}

variable "aws_lambda_region" {
  type = string
}

variable "aws_lambda_runtime" {
  type = string
}

variable "aws_lambda_timeout" {
  type = number
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
