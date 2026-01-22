variable "project" {
  description = "GCP project ID"
  type        = string
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
