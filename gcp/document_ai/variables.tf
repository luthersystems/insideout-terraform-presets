variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
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
  description = "GCP region for the stack. Document AI itself runs in a multi-region location (us|eu), derived from this region's continent unless var.location overrides it."
  type        = string
  default     = "us-central1"
}

variable "location" {
  description = "Document AI multi-region location: \"us\" or \"eu\" only (NOT a GCP region). Null derives it from var.region's continent. The composer's mapper sets this by translating the stack region; do not pass a region like \"us-central1\"."
  type        = string
  default     = null

  validation {
    condition     = var.location == null ? true : contains(["us", "eu"], var.location)
    error_message = "location must be null, \"us\", or \"eu\" (Document AI multi-region locations)."
  }
}

variable "processor_type" {
  description = "Document AI processor type, e.g. OCR_PROCESSOR, FORM_PARSER_PROCESSOR, INVOICE_PROCESSOR. Immutable — changing it forces replacement. Must be an uppercase processor-type identifier."
  type        = string
  default     = "OCR_PROCESSOR"

  validation {
    condition     = can(regex("^[A-Z][A-Z0-9_]+$", var.processor_type))
    error_message = "processor_type must be an uppercase processor-type identifier (e.g. OCR_PROCESSOR, FORM_PARSER_PROCESSOR)."
  }
}

variable "display_name" {
  description = "Display name of the processor. Null defaults to \"<project>-docai-<type>\" so a bare compose still produces a uniquely-named, attributable processor."
  type        = string
  default     = null
}

variable "kms_key_name" {
  description = "Fully-qualified Cloud KMS CMEK (projects/<p>/locations/<l>/keyRings/<k>/cryptoKeys/<c>) to encrypt processor data. Null uses Google-managed encryption. The key must be reachable from the processor's location."
  type        = string
  default     = null
}

variable "default_processor_version" {
  description = "Optional pretrained model version to pin as the processor's default (e.g. a stable \"pretrained-ocr-...\" version name). Null lets the provider/Google default win. Must be a version valid for the chosen processor_type."
  type        = string
  default     = null
}
