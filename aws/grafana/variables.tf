variable "project" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

# Grafana-specific variables (placeholders)
variable "grafana_admin_role_arn" {
  description = "IAM role ARN for Grafana admin access"
  type        = string
  default     = ""
}

variable "authentication_providers" {
  description = "Authentication providers (SAML, AWS_SSO)"
  type        = list(string)
  default     = ["AWS_SSO"]
}

