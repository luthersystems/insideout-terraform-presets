variable "project" {
  description = "Project name. Prefixes every resource name and sets the Project tag."
  type        = string
}

variable "region" {
  description = "AWS region. Passed to the luthername module for the standard tag set."
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

variable "grafana_version" {
  description = "Grafana version of the workspace."
  type        = string
  default     = "10.4"
}

variable "authentication_providers" {
  description = "How users log in: AWS_SSO (IAM Identity Center, which must be on in the account or its organization before the deploy), SAML, or both."
  type        = list(string)
  default     = ["AWS_SSO"]

  validation {
    condition     = length(var.authentication_providers) > 0 && alltrue([for p in var.authentication_providers : contains(["AWS_SSO", "SAML"], p)])
    error_message = "authentication_providers must be one or both of AWS_SSO and SAML."
  }
}

variable "admin_sso_user_ids" {
  description = "IAM Identity Center user IDs that get the Grafana ADMIN role (AWS_SSO only)."
  type        = list(string)
  default     = []
}

variable "admin_sso_group_ids" {
  description = "IAM Identity Center group IDs that get the Grafana ADMIN role (AWS_SSO only)."
  type        = list(string)
  default     = []
}

variable "saml_idp_metadata_url" {
  description = "URL of the SAML identity provider's metadata (SAML only)."
  type        = string
  default     = ""

  validation {
    condition     = var.saml_idp_metadata_url == "" || startswith(var.saml_idp_metadata_url, "https://")
    error_message = "saml_idp_metadata_url must be an https URL."
  }
}

variable "saml_role_assertion" {
  description = "SAML assertion attribute that holds the user's roles (SAML only)."
  type        = string
  default     = "role"
}

variable "saml_admin_role_values" {
  description = "Values of the role assertion that give the Grafana ADMIN role (SAML only)."
  type        = list(string)
  default     = []
}

variable "saml_editor_role_values" {
  description = "Values of the role assertion that give the Grafana EDITOR role (SAML only). At least one is needed for SAML."
  type        = list(string)
  default     = []
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
