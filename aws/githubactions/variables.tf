variable "project" {
  description = "Project name. Prefixes the role name and sets the Project tag."
  type        = string
}

variable "region" {
  description = "AWS region. Passed to the luthername module for the standard tag set; IAM is global."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment (dev, staging, prod)."
  type        = string
  default     = "dev"
}

variable "tags" {
  description = "Extra tags merged onto every taggable resource."
  type        = map(string)
  default     = {}
}

variable "github_repository" {
  description = "GitHub repository in OWNER/REPO form. Only workflows in this repository can assume the deploy role."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must match OWNER/REPO (e.g. \"luthersystems/insideout-terraform-presets\")."
  }
}

variable "allowed_branches" {
  description = "Branches whose workflows may assume the deploy role. At least one of allowed_branches, allowed_tags and allowed_pull_request must admit something."
  type        = list(string)
  default     = ["main"]

  validation {
    condition     = alltrue([for b in var.allowed_branches : can(regex("^[^*\\s]+$", b))])
    error_message = "allowed_branches entries must be branch names, not empty or wildcards."
  }
}

variable "allowed_tags" {
  description = "Tag patterns (StringLike: * and ? match) whose workflows may assume the deploy role, for example [\"v*\"]."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for t in var.allowed_tags : trimspace(t) != "" && t != "*"])
    error_message = "allowed_tags entries must not be empty or a bare \"*\"."
  }
}

variable "allowed_pull_request" {
  description = "Admit workflows run on pull_request events. Leave false for deploy credentials: a pull request from a fork runs untrusted code."
  type        = bool
  default     = false
}

variable "create_oidc_provider" {
  description = "Create the account's GitHub OIDC provider. An account can have only one, so leave false (the default) when it already exists: the preset then uses oidc_provider_arn, or finds the provider in the account."
  type        = bool
  default     = false
}

variable "oidc_provider_arn" {
  description = "ARN of an existing GitHub OIDC provider. Empty (the default) finds it in the account by URL. Ignored when create_oidc_provider is true."
  type        = string
  default     = ""

  validation {
    condition     = var.oidc_provider_arn == "" || can(regex("^arn:aws[a-z-]*:iam::[0-9]{12}:oidc-provider/token\\.actions\\.githubusercontent\\.com$", var.oidc_provider_arn))
    error_message = "oidc_provider_arn must be the ARN of the token.actions.githubusercontent.com OIDC provider."
  }
}

variable "managed_policy_arns" {
  description = "IAM policy ARNs to attach to the deploy role. The role has no permissions without them."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for a in var.managed_policy_arns : can(regex("^arn:aws[a-z-]*:iam::([0-9]{12}|aws):policy/", a))])
    error_message = "managed_policy_arns entries must be IAM policy ARNs."
  }
}

variable "max_session_duration" {
  description = "Maximum session length of the deploy role, in seconds (3600 to 43200)."
  type        = number
  default     = 3600

  validation {
    condition     = var.max_session_duration >= 3600 && var.max_session_duration <= 43200
    error_message = "max_session_duration must be between 3600 and 43200."
  }
}

variable "iam_scope" {
  description = "IAM path and permissions boundary for the roles, customer managed policies and instance profiles this module creates. The scoped deploy policy allows IAM writes only under /insideout/<project>/ and creates roles only with the workload boundary (sandbox-infrastructure-template#147). Both are set together, because the boundary passes only roles on the path. null keeps the module's defaults. Changing it replaces the roles."
  type = object({
    path                 = string
    permissions_boundary = string
  })
  default = null

  validation {
    condition = var.iam_scope == null ? true : (
      can(regex("^/insideout/[A-Za-z0-9+=,.@_-]+/$", var.iam_scope.path)) &&
      can(regex("^arn:aws[a-z-]*:iam::([0-9]{12}|aws):policy/", var.iam_scope.permissions_boundary))
    )
    error_message = "iam_scope.path must be /insideout/<project>/ and iam_scope.permissions_boundary an IAM policy ARN."
  }
}
