# /add-gcp-module — Add New GCP Module Skill

Create a new GCP Terraform preset module following all project conventions.

## Trigger

Use when asked to add a new GCP module, create a GCP preset, or scaffold a GCP Terraform module.

## Workflow

### 1. Name the Module

GCP modules use **snake_case** directory names (e.g., `api_gateway`, `cloud_run`, `cloud_sql`).

```
gcp/<module_name>/
```

### 2. Create main.tf

Every `main.tf` must include:

```hcl
terraform {
  required_version = ">= 1.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}
```

Key rules:
- If using community modules, source from `terraform-google-modules` with version pin
- Otherwise use direct `google_*` resources
- If the module needs the `random` provider, add it with `>= 3.5`
- Enable encryption, enforce least-privilege IAM by default
- **Label every labelable resource** with `labels = merge({ project = var.project }, var.labels)` so `Project` identity propagates to the inspector. Coverage is CI-enforced by `tests/lint-project-label.sh` — if you add a new label-capable resource type, add it to the `LABEL_CAPABLE_GCP` allowlist in that script.

### 3. Create variables.tf

**Required variables** (every GCP preset that creates project-scoped resources must declare both `project` and `project_id` — see issue #157):

```hcl
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
  description = "GCP region"
  type        = string
}

variable "labels" {
  description = "Resource labels"
  type        = map(string)
  default     = {}
}
```

**Where to use each in main.tf:**
- `var.project_id` — every `project = ...` argument on a `google_*` resource, every vendored sub-module's `project_id = ...`, and the workload-identity pool name `${var.project_id}.svc.id.goog`. These are the real project IDs Google's API checks.
- `var.project` — naming interpolations (`name = "${var.project}-..."`) and label values (`labels = merge({ project = var.project }, var.labels)`). The reliable3 inspector groups by the `project` label value being the per-stack prefix; do NOT switch label values to `var.project_id`.

**Validation patterns:**
- **Null-safe validation:** Always use ternary: `var.x == null ? true : contains([...], var.x)`
- Use `can()`, `regex()`, `trimspace()`, `contains()` as appropriate
- Variables without defaults become required root variables

### 4. Create outputs.tf

Declare outputs for cross-module wiring:

```hcl
output "id" {
  description = "Resource ID"
  value       = google_<resource>.<name>.id
}
```

Common wiring outputs: `id`, `name`, `self_link`, `network_id`, `ip_address`.

### 5. Check Go Embedding

Verify patterns in `zz_embed.go`:
- `.tf` files: already covered by `gcp/*/*.tf`
- `.tmpl` files: **NOT covered** — if adding `.tmpl` files, add `//go:embed gcp/*/*.tmpl` to `zz_embed.go`

### 6. Format and Validate

```bash
terraform fmt gcp/<module_name>/
cd gcp/<module_name> && terraform init -backend=false -input=false && terraform validate
```

### 7. Verify Go Embed Compiles

```bash
go build ./...
```

## Anti-Patterns

- Using camelCase directory names (GCP uses snake_case)
- Using `tags` instead of `labels` (GCP convention)
- Using `var.x == null || condition` in validation
- Forgetting `project`, `project_id`, or `region` variables (project-scoped GCP resources need both project and project_id — see issue #157)
- Passing `var.project` to `google_*.project = ...` or to a vendored `project_id = ...` argument — these need `var.project_id`, the real GCP project ID
- Adding `.tmpl` files without updating `zz_embed.go`
- Creating a labelable GCP resource without `labels = merge(..., var.labels)` — inspector drift detection relies on `Project` label propagation (AWS mirror: issue #81)

## Checklist

- [ ] Directory: `gcp/<snake_case_name>/`
- [ ] `main.tf` with `required_providers` (Google >= 5.0, Terraform >= 1.0)
- [ ] `variables.tf` with `project`, `project_id`, `region`, `labels` variables (issue #157)
- [ ] All `google_*.project = ...` and vendored `project_id = ...` reference `var.project_id` (NOT `var.project`)
- [ ] `outputs.tf` with wiring outputs
- [ ] Null-safe validation (ternary pattern)
- [ ] Security defaults (encryption, least-privilege IAM)
- [ ] Every labelable resource has `labels = merge(..., var.labels)`
- [ ] `terraform fmt` clean
- [ ] `terraform validate` passes
- [ ] `go build ./...` succeeds
- [ ] `zz_embed.go` updated if `.tmpl` files added
