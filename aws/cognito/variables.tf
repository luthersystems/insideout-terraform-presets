variable "region" {
  description = "AWS region"
  type        = string
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project/prefix used for resource names"
  type        = string
  default     = "demo"
  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "sign_in_type" {
  description = "How users sign in: email | username | both (username plus email alias)"
  type        = string
  default     = "email"
  validation {
    condition     = contains(["email", "username", "both"], var.sign_in_type)
    error_message = "sign_in_type must be one of: email, username, both."
  }
}

variable "mfa_required" {
  description = "Require MFA for sign-in"
  type        = bool
  default     = false
}

variable "oauth_callback_urls" {
  description = "Allowed OAuth2 callback URLs for the hosted UI/app client"
  type        = list(string)
  default     = ["http://localhost:3000/callback"]
  validation {
    condition     = length(var.oauth_callback_urls) == 0 || alltrue([for u in var.oauth_callback_urls : can(regex("^https?://", u))])
    error_message = "Each callback URL must start with http:// or https://"
  }
}

variable "oauth_logout_urls" {
  description = "Allowed OAuth2 logout URLs"
  type        = list(string)
  default     = ["http://localhost:3000/logout"]
  validation {
    condition     = length(var.oauth_logout_urls) == 0 || alltrue([for u in var.oauth_logout_urls : can(regex("^https?://", u))])
    error_message = "Each logout URL must start with http:// or https://"
  }
}

variable "create_domain" {
  description = "Create a Cognito hosted UI domain"
  type        = bool
  default     = true
}

variable "domain_prefix" {
  description = "Optional custom domain prefix (defaults to project). Lowercase/[-a-z0-9] only."
  type        = string
  default     = null
  validation {
    # allow null/empty, otherwise `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`
    condition     = var.domain_prefix == null || var.domain_prefix == "" || can(regex("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$", var.domain_prefix))
    error_message = "domain_prefix must be lowercase and contain only [a-z0-9-], 1–63 chars, and not start/end with '-'."
  }
}

# ---------------- Federated Identity Providers ----------------

variable "oidc_identity_providers" {
  description = <<EOT
List of OIDC IdPs to federate (e.g., Okta, Auth0):
[
  {
    name                     = "Okta",
    client_id                = "xxx",
    client_secret            = "xxx",
    issuer                   = "https://dev-123456.okta.com/oauth2/default",
    authorize_scopes         = "openid email profile",   # optional
    attributes_request_method = "GET",                   # optional, default GET
    attribute_mapping = {                                # optional
      email = "email"
      name  = "name"
    }
  }
]
EOT
  type = list(object({
    name                      = string
    client_id                 = string
    client_secret             = string
    issuer                    = string
    authorize_scopes          = optional(string)
    attributes_request_method = optional(string) # GET | POST
    attribute_mapping         = optional(map(string))
  }))
  default   = []
  sensitive = true

  validation {
    condition = length(var.oidc_identity_providers) == 0 || alltrue([
      for p in var.oidc_identity_providers :
      length(trimspace(p.name)) > 0
      && length(trimspace(p.client_id)) > 0
      && length(trimspace(p.client_secret)) > 0
      && can(regex("^https://", p.issuer))
      && (
        p.attributes_request_method == null
        ? true
        : contains(["GET", "POST"], upper(p.attributes_request_method))
      )
    ])
    error_message = "Each OIDC provider must have non-empty name/client_id/client_secret, an https:// issuer, and attributes_request_method of GET or POST (if set)."
  }
}

variable "saml_identity_providers" {
  description = <<EOT
List of SAML IdPs to federate:
[
  {
    name              = "OktaSAML",
    metadata_url      = "https://example.okta.com/app/xxx/sso/saml/metadata", # or leave null and use metadata_file
    metadata_file     = null,                                                  # path on disk if you prefer file
    attribute_mapping = {
      email = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
      name  = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"
    }
  }
]
EOT
  type = list(object({
    name              = string
    metadata_url      = optional(string)
    metadata_file     = optional(string)
    attribute_mapping = optional(map(string))
  }))
  default = []

  validation {
    condition = length(var.saml_identity_providers) == 0 || alltrue([
      for p in var.saml_identity_providers :
      length(trimspace(p.name)) > 0
      && (
        # require at least one of metadata_url or metadata_file
        (p.metadata_url != null && can(regex("^https?://", p.metadata_url)))
        || (p.metadata_file != null && length(trimspace(p.metadata_file)) > 0)
      )
    ])
    error_message = "Each SAML provider must have a non-empty name and either a valid http(s) metadata_url or a non-empty metadata_file path."
  }
}

variable "tags" {
  description = "Tags to apply to supported resources"
  type        = map(string)
  default     = {}
}
