variable "project" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

# Splunk-specific variables (placeholders)
variable "splunk_hec_endpoint" {
  description = "Splunk HTTP Event Collector endpoint URL"
  type        = string
  default     = ""
}

variable "splunk_hec_token" {
  description = "Splunk HEC token (should be stored in Secrets Manager)"
  type        = string
  default     = ""
  sensitive   = true
}

