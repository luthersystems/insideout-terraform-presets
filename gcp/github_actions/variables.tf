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

# tflint-ignore: terraform_unused_declarations  # composer always wires var.region at the root (CLAUDE.md mandate); WIF itself is global so the module body doesn't consume it.
variable "region" {
  description = "GCP region. WIF is global; this variable exists for composer wiring uniformity."
  type        = string
  default     = "us-central1"
}

# -----------------------------------------------------------------------------
# GitHub repository identity
# -----------------------------------------------------------------------------
variable "github_repository" {
  description = "GitHub repository in OWNER/REPO format (e.g. \"luthersystems/insideout-terraform-presets\"). Only workflows from this exact repo can mint deploy credentials via the WIF provider."
  type        = string

  validation {
    # GitHub login + repo name character classes per
    # https://docs.github.com/en/get-started/learning-about-github/types-of-github-accounts
    # — letters, digits, hyphen, underscore, dot, slash separator.
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must match OWNER/REPO (e.g. \"luthersystems/insideout-terraform-presets\")."
  }
}

# -----------------------------------------------------------------------------
# Ref-pattern gates — at least one must be non-trivial
# -----------------------------------------------------------------------------
# These compose into the WIF provider's CEL attribute_condition. The
# repository check is always enforced; these gates further narrow which
# refs / event types from that repository can mint credentials.
#
# Defaults: allow_branches = ["main"] only. Conservative and matches the
# typical "deploy on push to main" GitHub Actions setup. Callers wanting
# tag-based release deploys override allowed_tags; PR-preview deploys
# override allowed_pull_request.
variable "allowed_branches" {
  description = "Branch names (e.g. [\"main\", \"release\"]) whose workflows may impersonate the deploy SA. Empty list disables branch-based access. At least one of allowed_branches / allowed_tags / allowed_pull_request must be non-empty (else WIF would accept any workflow — security regression)."
  type        = list(string)
  default     = ["main"]
}

variable "allowed_tags" {
  description = "Tag patterns (e.g. [\"v*\", \"release-*\"]) whose workflows may impersonate the deploy SA. Pattern matching is exact-string against the ref (refs/tags/<pattern>) — Cloud DNS / WIF do not support glob expansion. For glob behaviour, list each expected tag explicitly. Default empty disables tag-based access."
  type        = list(string)
  default     = []
}

variable "allowed_pull_request" {
  description = "Allow workflows triggered by pull_request events to impersonate the deploy SA. Use for PR-preview deploys; leave false for production-only credentials. Default false (recommended — PR workflows from forks should NOT mint deploy creds)."
  type        = bool
  default     = false
}

# -----------------------------------------------------------------------------
# Deploy role grants on the SA
# -----------------------------------------------------------------------------
variable "deploy_roles" {
  description = "List of project-level IAM roles granted to the deploy SA. Defaults to the minimum for a Cloud Run deploy (roles/run.admin updates services; roles/iam.serviceAccountUser lets the SA attach runtime SAs to revisions). For other targets, override — e.g. GKE deploys add roles/container.developer, GCS uploads add roles/storage.admin, BigQuery jobs add roles/bigquery.jobUser. Avoid roles/owner — least-privilege."
  type        = list(string)
  default = [
    "roles/run.admin",
    "roles/iam.serviceAccountUser",
  ]

  validation {
    condition     = length(var.deploy_roles) > 0
    error_message = "deploy_roles must list at least one role; an empty list creates a powerless SA that the workflow can impersonate but cannot use."
  }

  validation {
    # Defensive: callers occasionally mistake project-name strings for role names.
    condition     = alltrue([for r in var.deploy_roles : can(regex("^roles/", r))])
    error_message = "Every entry in deploy_roles must start with \"roles/\" (e.g. \"roles/run.admin\"). Custom roles use the full \"projects/<id>/roles/<name>\" form — also accepted, but must include the projects/ prefix."
  }
}

# -----------------------------------------------------------------------------
# Resource short names (overridable for length-constrained projects)
# -----------------------------------------------------------------------------
variable "pool_short_name" {
  description = "Workload identity pool short name (combined with var.project to form the full pool ID, bounded to 32 chars total). Shorten when var.project itself is long."
  type        = string
  default     = "github-actions"

  validation {
    # Pool ID regex: `^[a-z][a-z0-9-]{3,30}[a-z0-9]$` per Google (4–32 chars
    # total). The composed `<project>-<pool_short_name>` must satisfy this
    # — pin a 3-char floor on the short_name so even a 1-char project
    # clears Google's 4-char minimum after the hyphen join. (Terraform
    # 1.5+ forbids cross-variable references in error_message, so the
    # composed-id arithmetic is documented in this comment instead.)
    condition     = can(regex("^[a-z][a-z0-9-]{1,30}[a-z0-9]$", var.pool_short_name))
    error_message = "pool_short_name must be 3–32 chars: start with a lowercase letter, end alphanumeric, contain only lowercase letters / digits / hyphens. The 3-char floor ensures the composed pool ID clears Google's 4-char minimum even for a 1-char project prefix."
  }
}

variable "provider_short_name" {
  description = "OIDC provider short name within the pool. Defaults to \"github\"."
  type        = string
  default     = "github"

  validation {
    # Same Google 4-char-floor reasoning as pool_short_name above.
    condition     = can(regex("^[a-z][a-z0-9-]{1,30}[a-z0-9]$", var.provider_short_name))
    error_message = "provider_short_name must be 3–32 chars: start with a lowercase letter, end alphanumeric, contain only lowercase letters / digits / hyphens."
  }
}

variable "service_account_short_name" {
  description = "account_id for the deploy SA. Hard cap is 30 chars (GCP-imposed). Default \"github-deploy\" leaves room for downstream consumers that prefix again."
  type        = string
  default     = "github-deploy"

  validation {
    # `[a-z]([-a-z0-9]*[a-z0-9])` and 6-30 char length per GCP.
    condition     = can(regex("^[a-z][-a-z0-9]{4,28}[a-z0-9]$", var.service_account_short_name))
    error_message = "service_account_short_name must be 6-30 chars: lowercase letters / digits / hyphens, start with a letter, end alphanumeric."
  }
}

# -----------------------------------------------------------------------------
# Labels — applied to label-capable resources (SAs and WIF resources don't
# accept labels at the time of writing; this var is reserved for future use
# and for consistency with the rest of the repo's GCP preset UX).
# -----------------------------------------------------------------------------
# tflint-ignore: terraform_unused_declarations  # WIF resources / google_service_account do not accept labels per the google provider schema as of v5.x; declared for UX consistency with other GCP presets.
variable "labels" {
  description = "Additional labels to apply to label-capable resources (currently unused — google_iam_workload_identity_pool, ..._provider, and google_service_account do not accept labels per the google provider schema as of v5.x). Reserved for UX consistency with other GCP presets."
  type        = map(string)
  default     = {}
}

# -----------------------------------------------------------------------------
# Cross-variable validation: at least one ref-pattern gate must be non-trivial
# -----------------------------------------------------------------------------
# Hosted as a precondition on google_iam_workload_identity_pool_provider in
# main.tf (Terraform 1.5+ requires variable validation blocks to reference
# their own variable, which can't express a cross-variable rule). See
# the precondition block at the provider resource for the actual check.
