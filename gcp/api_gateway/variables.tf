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
  description = "OpenAPI 2.0 specification for the API. The default is a minimal spec that satisfies GCP API Gateway's validator (host + x-google-backend on each operation) so the module composes and applies cleanly out of the box; override it with your real spec."
  type        = string
  default     = <<-EOT
    swagger: '2.0'
    info:
      title: API
      description: Placeholder API. Override var.openapi_spec to publish your own.
      version: '1.0'
    host: example.com
    schemes:
      - https
    produces:
      - application/json
    paths:
      /health:
        get:
          summary: Health check
          operationId: healthCheck
          x-google-backend:
            address: https://example.com/health
          responses:
            '200':
              description: OK
              schema:
                type: string
  EOT
}

variable "backend_service_account" {
  description = "Service account email for backend authentication"
  type        = string
  default     = ""
}

variable "labels" {
  description = "Labels to apply to API Gateway resources. Merged with the canonical { project = var.project, managed = \"terraform\" } baseline."
  type        = map(string)
  default     = {}
}
