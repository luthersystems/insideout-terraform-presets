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

# Splunk-specific variables (placeholders)
# tflint-ignore: terraform_unused_declarations  # stub module — var.splunk_hec_endpoint is reserved for the actual implementation; the module body is currently a placeholder
variable "splunk_hec_endpoint" {
  description = "Splunk HTTP Event Collector endpoint URL"
  type        = string
  default     = ""
}

# tflint-ignore: terraform_unused_declarations  # stub module — var.splunk_hec_token is reserved for the actual implementation; the module body is currently a placeholder
variable "splunk_hec_token" {
  description = "Splunk HEC token (should be stored in Secrets Manager)"
  type        = string
  default     = ""
  sensitive   = true
}

