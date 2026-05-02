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

# Datadog-specific variables (placeholders)
# tflint-ignore: terraform_unused_declarations  # stub module — var.datadog_api_key is reserved for the actual implementation; the module body is currently a placeholder
variable "datadog_api_key" {
  description = "Datadog API key (should be stored in Secrets Manager)"
  type        = string
  default     = ""
  sensitive   = true
}

# tflint-ignore: terraform_unused_declarations  # stub module — var.datadog_site is reserved for the actual implementation; the module body is currently a placeholder
variable "datadog_site" {
  description = "Datadog site (e.g., datadoghq.com, datadoghq.eu)"
  type        = string
  default     = "datadoghq.com"
}

