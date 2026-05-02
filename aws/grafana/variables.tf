# tflint-ignore: terraform_unused_declarations  # composer always wires var.project at the root (CLAUDE.md mandate)
variable "project" {
  description = "Project name for resource naming"
  type        = string
}

# tflint-ignore: terraform_unused_declarations  # composer always wires var.environment at the root (CLAUDE.md mandate)
variable "environment" {
  description = "Environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

# Grafana-specific variables (placeholders)
# tflint-ignore: terraform_unused_declarations  # stub module — var.grafana_admin_role_arn is reserved for the actual implementation; the module body is currently a placeholder
variable "grafana_admin_role_arn" {
  description = "IAM role ARN for Grafana admin access"
  type        = string
  default     = ""
}

# tflint-ignore: terraform_unused_declarations  # stub module — var.authentication_providers is reserved for the actual implementation; the module body is currently a placeholder
variable "authentication_providers" {
  description = "Authentication providers (SAML, AWS_SSO)"
  type        = list(string)
  default     = ["AWS_SSO"]
}

