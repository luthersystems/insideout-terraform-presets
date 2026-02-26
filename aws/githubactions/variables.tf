variable "project" {
  description = "Project name for resource naming"
  type        = string
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "github_org" {
  description = "GitHub organization or username"
  type        = string
  default     = ""
}

variable "github_repo" {
  description = "GitHub repository name (deprecated, use repository_name)"
  type        = string
  default     = ""
}

# ---------------------------------------------------------------------------
# Repository settings
# ---------------------------------------------------------------------------

variable "repository_name" {
  description = "GitHub repository name. Defaults to '<project>-infra' if empty."
  type        = string
  default     = ""
}

variable "repository_description" {
  description = "Description for the GitHub repository"
  type        = string
  default     = ""
}

variable "repository_visibility" {
  description = "Repository visibility (public or private)"
  type        = string
  default     = "private"

  validation {
    condition     = contains(["public", "private", "internal"], var.repository_visibility)
    error_message = "repository_visibility must be one of: public, private, internal."
  }
}

variable "vulnerability_alerts" {
  description = "Enable vulnerability alerts for the repository"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# Collaborators
# ---------------------------------------------------------------------------

variable "collaborators" {
  description = "List of collaborators to add to the repository"
  type = list(object({
    username   = string
    permission = optional(string, "admin")
  }))
  default = []

  validation {
    condition = alltrue([
      for c in var.collaborators :
      contains(["pull", "triage", "push", "maintain", "admin"], c.permission)
    ])
    error_message = "Each collaborator permission must be one of: pull, triage, push, maintain, admin."
  }
}

# ---------------------------------------------------------------------------
# OIDC / IAM
# ---------------------------------------------------------------------------

variable "create_oidc_provider" {
  description = "Whether to create the GitHub OIDC provider. Set to false if it already exists in the account."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to AWS resources"
  type        = map(string)
  default     = {}
}
