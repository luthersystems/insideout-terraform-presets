variable "project" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "source_repository" {
  description = "Source code repository (CodeCommit repo name or GitHub connection)"
  type        = string
  default     = ""
}

variable "branch" {
  description = "Branch to build from"
  type        = string
  default     = "main"
}

