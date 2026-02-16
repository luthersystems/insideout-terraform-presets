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

variable "num_keys" {
  type    = number
  default = 1
}

variable "tags" {
  type    = map(string)
  default = {}
}
