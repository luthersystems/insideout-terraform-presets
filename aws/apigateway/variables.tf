variable "region" {
  type = string
}

variable "project" {
  type = string
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "domain_name" {
  type    = string
  default = null
}

variable "certificate_arn" {
  type    = string
  default = null
}

variable "throttling_burst_limit" {
  description = "Optional override for the $default stage's burst limit (requests). When null, inherits the AWS account-level default."
  type        = number
  default     = null
  validation {
    condition     = var.throttling_burst_limit == null ? true : var.throttling_burst_limit >= 0
    error_message = "throttling_burst_limit must be null or a non-negative integer."
  }
}

variable "throttling_rate_limit" {
  description = "Optional override for the $default stage's steady-state rate limit (requests per second). When null, inherits the AWS account-level default."
  type        = number
  default     = null
  validation {
    condition     = var.throttling_rate_limit == null ? true : var.throttling_rate_limit >= 0
    error_message = "throttling_rate_limit must be null or a non-negative number."
  }
}

variable "tags" {
  type    = map(string)
  default = {}
}
