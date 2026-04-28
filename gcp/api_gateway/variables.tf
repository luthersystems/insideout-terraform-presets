variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string
}

variable "project_id" {
  description = "Real GCP project ID where resources are created (e.g. \"my-prod-12345\"). Distinct from var.project, which is the naming/label prefix and need not be a valid GCP project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid GCP project ID: 6–30 chars, lowercase letters/digits/hyphens, start with a letter, end alphanumeric."
  }
}

variable "region" {
  description = "GCP region for the gateway"
  type        = string
  default     = "us-central1"
}

variable "openapi_spec" {
  description = "OpenAPI specification for the API"
  type        = string
  default     = <<-EOT
    swagger: '2.0'
    info:
      title: API
      version: '1.0'
    schemes:
      - https
    produces:
      - application/json
    paths:
      /health:
        get:
          summary: Health check
          operationId: healthCheck
          responses:
            '200':
              description: OK
  EOT
}

variable "backend_service_account" {
  description = "Service account email for backend authentication"
  type        = string
  default     = ""
}
