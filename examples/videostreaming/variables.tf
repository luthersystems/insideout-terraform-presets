variable "alb_project" {
  type = string
}

variable "alb_region" {
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

variable "dynamodb_billing_mode" {
  type = string
}

variable "dynamodb_project" {
  type = string
}

variable "dynamodb_region" {
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

variable "resource_project" {
  type = string
}

variable "resource_region" {
  type = string
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

variable "vpc_project" {
  type = string
}

variable "vpc_region" {
  type = string
}
