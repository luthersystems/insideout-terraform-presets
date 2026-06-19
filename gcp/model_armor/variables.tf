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
  description = "GCP region for the Model Armor template (Model Armor locations ARE regions, e.g. us-central1)."
  type        = string
  default     = "us-central1"
}

variable "location" {
  description = "Model Armor location (a GCP region). Null uses var.region. Model Armor is region-limited — choose a region where it is available."
  type        = string
  default     = null
}

variable "template_id" {
  description = "Template ID for the Model Armor template. Null defaults to \"<project>-armor\" so a bare compose produces a uniquely-named, attributable template."
  type        = string
  default     = null

  validation {
    condition     = var.template_id == null ? true : can(regex("^[a-z][a-z0-9-]{0,61}[a-z0-9]$", var.template_id))
    error_message = "template_id must be a valid resource id: lowercase letters/digits/hyphens, start with a letter, end alphanumeric."
  }
}

variable "filter_confidence_level" {
  description = "Confidence floor at which filters trip: LOW_AND_ABOVE (most aggressive), MEDIUM_AND_ABOVE, or HIGH (most permissive). Applied to the prompt-injection/jailbreak and responsible-AI filters."
  type        = string
  default     = "MEDIUM_AND_ABOVE"

  validation {
    condition     = contains(["LOW_AND_ABOVE", "MEDIUM_AND_ABOVE", "HIGH"], var.filter_confidence_level)
    error_message = "filter_confidence_level must be one of LOW_AND_ABOVE, MEDIUM_AND_ABOVE, HIGH."
  }
}

variable "rai_filter_types" {
  description = "Responsible-AI category filters to enforce. Each must be a valid Model Armor RAI filter type."
  type        = list(string)
  default     = ["DANGEROUS", "HATE_SPEECH", "SEXUALLY_EXPLICIT", "HARASSMENT"]

  validation {
    condition = length(var.rai_filter_types) > 0 && alltrue([
      for t in var.rai_filter_types : contains(["DANGEROUS", "HATE_SPEECH", "SEXUALLY_EXPLICIT", "HARASSMENT"], t)
    ])
    error_message = "rai_filter_types must be a non-empty subset of DANGEROUS, HATE_SPEECH, SEXUALLY_EXPLICIT, HARASSMENT."
  }
}

variable "manage_floorsetting" {
  description = "When true, also create the project-wide Model Armor floor setting. ⚠️ SINGLETON: the floor setting is a project/org singleton — creating it where one already exists fails, and two stacks in one project collide. Enable only for the single stack that owns the project's floor. Default false."
  type        = bool
  default     = false
}

variable "labels" {
  description = "Resource labels merged with the standard { project = var.project } identity label on the template."
  type        = map(string)
  default     = {}
}
