# /add-aws-module — Add New AWS Module Skill

Create a new AWS Terraform preset module following all project conventions.

## Trigger

Use when asked to add a new AWS module, create an AWS preset, or scaffold an AWS Terraform module.

## Workflow

### 1. Name the Module

AWS modules use **camelCase** directory names (e.g., `apigateway`, `cloudwatchlogs`, `secretsmanager`).

```
aws/<modulename>/
```

### 2. Create main.tf

Every `main.tf` must include:

```hcl
terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

data "aws_region" "current" {}
```

Key rules:
- Use `data.aws_region.current.region` (not `var.region`) for constructing service names
- If the module wraps a community module, use `source = "terraform-aws-modules/<name>/aws"` with a version pin
- If the module needs a provider alias (like WAF needing `aws.us_east_1`), add `configuration_aliases` and create a `.validate-skip` marker file
- Enable encryption, block public access, enforce least-privilege IAM by default

### 3. Create variables.tf

**Required variables** (every preset must declare these — the composition engine always maps them):

```hcl
variable "project" {
  description = "Project name"
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,61}[a-z0-9]$", var.project))
    error_message = "Project must be lowercase alphanumeric with hyphens, 3-63 characters."
  }
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "tags" {
  description = "Resource tags"
  type        = map(string)
  default     = {}
}
```

**Validation patterns:**
- **Null-safe validation:** Always use ternary: `var.x == null ? true : contains([...], var.x)` — Terraform does NOT short-circuit `||`
- Use `can()`, `regex()`, `cidrnetmask()`, `trimspace()`, `contains()` as appropriate
- Variables without defaults become required root variables the mapper MUST provide

### 4. Create outputs.tf

Declare outputs used for cross-module wiring:

```hcl
output "arn" {
  description = "ARN of the resource"
  value       = aws_<resource>.<name>.arn
}
```

Common wiring outputs: `arn`, `id`, `vpc_id`, `private_subnet_ids`, `security_group_id`, `endpoint`.

### 5. Check Go Embedding

Verify the new file patterns are covered by `zz_embed.go`:
- `.tf` files: already covered by `aws/*/*.tf`
- `.tmpl` files: already covered by `aws/*/*.tmpl`
- New extensions: add a `//go:embed` directive to `zz_embed.go`

### 6. Format and Validate

```bash
terraform fmt aws/<modulename>/
cd aws/<modulename> && terraform init -backend=false -input=false && terraform validate
```

### 7. Verify Go Embed Compiles

```bash
go build ./...
```

## Anti-Patterns

- Using `var.region` instead of `data.aws_region.current.region` in service names
- Using `var.x == null || condition` in validation (will crash on null)
- Forgetting `project` or `region` variables
- Leaving variables without defaults unless intentionally required
- Public access enabled by default

## Checklist

- [ ] Directory: `aws/<camelCaseName>/`
- [ ] `main.tf` with `required_providers` (AWS >= 6.0, Terraform >= 1.5)
- [ ] `variables.tf` with `project`, `region`, `tags` variables
- [ ] `outputs.tf` with wiring outputs
- [ ] Null-safe validation (ternary pattern)
- [ ] Security defaults (encryption, no public access)
- [ ] `terraform fmt` clean
- [ ] `terraform validate` passes
- [ ] `go build ./...` succeeds
- [ ] `.validate-skip` added if using `configuration_aliases`
