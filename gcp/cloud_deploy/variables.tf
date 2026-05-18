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
  description = "GCP region for the Cloud Deploy delivery pipeline and its targets. Cloud Deploy is a regional service; targets that deploy to Cloud Run / GKE in a DIFFERENT region than the pipeline are supported, but the pipeline resource itself lives in this region."
  type        = string
  default     = "us-central1"
}

variable "labels" {
  description = "Additional labels to apply to label-capable resources (the project label is always merged in)."
  type        = map(string)
  default     = {}
}

# -----------------------------------------------------------------------------
# Pipeline targets — the ordered serial chain of deployment destinations
# -----------------------------------------------------------------------------
# Each entry defines a deployment destination Cloud Deploy promotes a release
# through. The list order IS the promotion order — element [0] is the first
# stage, [n-1] is the last. The typical CD shape is two entries
# (staging -> prod) which is the default below.
#
# Per-entry fields:
#   - name             : pipeline-scoped target identifier. Lowercase letters,
#                        digits, hyphens; must satisfy Cloud Deploy's name
#                        regex `^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`.
#   - runtime          : "run" (Cloud Run) or "gke" (GKE). The preset's
#                        google_clouddeploy_target dispatches on this value
#                        to emit either a `run {}` or `gke {}` block.
#   - runtime_target   : runtime-dispatched destination identifier.
#                        For runtime="run": Cloud Run location (region),
#                        e.g. "us-central1". May differ from var.region.
#                        For runtime="gke": fully-qualified cluster ID,
#                        e.g. "projects/<id>/locations/<loc>/clusters/<name>".
#   - require_approval : optional bool, default false. When true, Cloud
#                        Deploy halts the promotion to this target and
#                        waits for an `gcloud deploy releases promote
#                        --to-target` operator action. Typically only
#                        flipped on for prod.
variable "targets" {
  description = "Ordered list of Cloud Deploy targets the pipeline promotes through (element [0] = first stage). Each target picks a runtime (run | gke) and a runtime-specific destination. Default is a staging -> prod Cloud Run pair in var.region; override for GKE-backed pipelines or multi-region rollouts."
  type = list(object({
    name             = string
    runtime          = string
    runtime_target   = string
    require_approval = optional(bool, false)
  }))
  default = [
    {
      name             = "staging"
      runtime          = "run"
      runtime_target   = "us-central1"
      require_approval = false
    },
    {
      name             = "prod"
      runtime          = "run"
      runtime_target   = "us-central1"
      require_approval = true
    },
  ]

  validation {
    condition     = length(var.targets) > 0
    error_message = "targets must list at least one entry; an empty list creates a delivery pipeline with no promotion stages, which Cloud Deploy rejects at apply with INVALID_ARGUMENT."
  }

  validation {
    condition     = alltrue([for t in var.targets : can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", t.name))])
    error_message = "Every targets[*].name must satisfy Cloud Deploy's identifier regex: start with a lowercase letter, 1-63 chars, contain only lowercase letters / digits / hyphens, and end alphanumeric."
  }

  validation {
    condition     = alltrue([for t in var.targets : contains(["run", "gke"], t.runtime)])
    error_message = "Every targets[*].runtime must be one of: \"run\" (Cloud Run) or \"gke\" (GKE). Other runtimes (Cloud Build custom targets, multi-target deploys) are out of scope for the v1 preset."
  }

  validation {
    condition     = alltrue([for t in var.targets : length(trimspace(t.runtime_target)) > 0])
    error_message = "Every targets[*].runtime_target must be non-empty (Cloud Run location for runtime=\"run\"; fully-qualified GKE cluster ID for runtime=\"gke\")."
  }

  validation {
    # Distinct names: Cloud Deploy's serial_pipeline.stages references
    # targets by name, so duplicate names would silently collapse stages.
    condition     = length(distinct([for t in var.targets : t.name])) == length(var.targets)
    error_message = "Every targets[*].name must be unique within the list — Cloud Deploy promotion stages reference targets by name and duplicates would silently collapse pipeline stages."
  }
}

# -----------------------------------------------------------------------------
# Resource short names (overridable for length-constrained projects)
# -----------------------------------------------------------------------------
variable "service_account_short_name" {
  description = "account_id for the Cloud Deploy runner SA. Hard cap is 30 chars (GCP-imposed). Default \"clouddeploy-runner\" (18 chars) leaves room for downstream consumers."
  type        = string
  default     = "clouddeploy-runner"

  validation {
    # `[a-z]([-a-z0-9]*[a-z0-9])` and 6-30 char length per GCP.
    condition     = can(regex("^[a-z][-a-z0-9]{4,28}[a-z0-9]$", var.service_account_short_name))
    error_message = "service_account_short_name must be 6-30 chars: lowercase letters / digits / hyphens, start with a letter, end alphanumeric."
  }
}

variable "pipeline_short_name" {
  description = "Cloud Deploy delivery-pipeline short name (combined with var.project to form the full pipeline name, bounded by Cloud Deploy's 63-char identifier cap). Default \"delivery\"."
  type        = string
  default     = "delivery"

  validation {
    # Cloud Deploy identifier regex (same as targets[*].name).
    condition     = can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", var.pipeline_short_name))
    error_message = "pipeline_short_name must satisfy Cloud Deploy's identifier regex: start with a lowercase letter, 1-63 chars, lowercase letters / digits / hyphens, end alphanumeric."
  }
}
