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
  description = "GCP region. Used as the Firestore location_id when var.location_id is empty — Firestore has a fixed list of valid locations (see var.location_id) that overlaps with but is not identical to the set of regular GCP regions, so override location_id when your stack region isn't a Firestore-valid value."
  type        = string
  default     = "us-central1"
}

variable "location_id" {
  description = "Firestore database location_id. When empty, defaults to var.region. Firestore accepts a fixed list of locations: multi-region (nam5, eur3) or specific regions like us-central1, us-east1, europe-west1, asia-east1. See https://cloud.google.com/firestore/docs/locations for the full list. Override this when your stack region (var.region) isn't on that list."
  type        = string
  default     = ""
}
