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

### 3. Create variables.tf

**Required variables** (every preset must declare these):

```hcl
variable "project" {
  description = "GCP project ID"
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project))
    error_message = "Project ID must be 6-30 characters, lowercase alphanumeric with hyphens."
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
- Forgetting `project` or `region` variables
- Adding `.tmpl` files without updating `zz_embed.go`

## Checklist

- [ ] Directory: `gcp/<snake_case_name>/`
- [ ] `main.tf` with `required_providers` (Google >= 5.0, Terraform >= 1.0)
- [ ] `variables.tf` with `project`, `region`, `labels` variables
- [ ] `outputs.tf` with wiring outputs
- [ ] Null-safe validation (ternary pattern)
- [ ] Security defaults (encryption, least-privilege IAM)
- [ ] `terraform fmt` clean
- [ ] `terraform validate` passes
- [ ] `go build ./...` succeeds
- [ ] `zz_embed.go` updated if `.tmpl` files added
