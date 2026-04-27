# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

InsideOut Terraform Presets — a library of standardized, composable Terraform module presets for AWS (29 modules) and GCP (22 modules). Used standalone or composed by the InsideOut engine to generate cloud infrastructure stacks. Currently in beta.

The Go module (`zz_embed.go`) embeds all `.tf` and `.tmpl` files via `embed.FS` for use by the InsideOut composition engine.

## Common Commands

```bash
# Validate a specific module
cd aws/<module> && terraform init && terraform validate

# Validate all modules (no Makefile exists yet)
for dir in aws/*/; do (cd "$dir" && terraform init -backend=false && terraform validate); done
for dir in gcp/*/; do (cd "$dir" && terraform init -backend=false && terraform validate); done

# Format check
terraform fmt -check -recursive

# Format fix
terraform fmt -recursive
```

## Architecture

```
aws/<module>/          # AWS Terraform modules (29 modules)
gcp/<module>/          # GCP Terraform modules (22 modules)
zz_embed.go            # Go embed directive exposing FS for all .tf/.tmpl files
go.mod                 # Go module: github.com/luthersystems/insideout-terraform-presets
```

### Module Structure Convention

Every module follows this pattern:
- `main.tf` — Resource definitions, provider requirements, locals
- `variables.tf` — Input variables with validation blocks
- `outputs.tf` — Output values

Some modules include additional files (e.g., `aws/bastion/user_data.sh.tmpl`).

### Provider Versions

- **AWS modules:** Terraform >= 1.5, AWS provider >= 6.0
- **GCP modules:** Terraform >= 1.0, Google provider >= 5.0
- Some modules use `random` provider >= 3.5

### Key Patterns

- **AWS modules** often wrap Terraform Registry community modules (e.g., `terraform-aws-modules/vpc/aws`)
- **GCP modules** use `terraform-google-modules` or direct `google_` resources
- **Naming:** AWS uses camelCase directory names (`apigateway`), GCP uses snake_case (`api_gateway`)
- **Variables:** Extensive `validation` blocks using `can()`, `cidrnetmask()`, `trimspace()`, `contains()`
- **Security defaults:** Encryption enabled, public access blocked, least-privilege IAM where applicable
- **Tagging/Labels:** Standardized via `tags` (AWS) or `labels` (GCP) variables

### Go Embedding

`zz_embed.go` must be updated when adding new file patterns (currently embeds `aws/*/*.tf`, `gcp/*/*.tf`, `aws/*/*.tmpl`). If a new GCP module includes `.tmpl` files, add a corresponding embed directive.

## Downstream Composition (How Presets Are Consumed)

These preset files are embedded at build time into the [reliable](https://github.com/luthersystems/reliable) repo via Go's `embed.FS`. The composition engine (`reliable/internal/reliabletf/`) does:

1. `GetPresetFiles("aws/vpc")` walks the embedded FS, returns all `.tf` files
2. Parses `variables.tf` to discover variable names, types, defaults
3. A Mapper converts user config into variable values
4. Variables are namespaced with `<component>_` prefix to avoid collisions (e.g., `project` becomes `vpc_project`, `region` becomes `ec2_region`)
5. Modules are wired together: `module.vpc.vpc_id` feeds into RDS/ALB/etc.
6. Outputs: root `main.tf` (module blocks), `variables.tf` (namespaced), `<key>.auto.tfvars` (values), `providers.tf`
7. The composed stack is tar.gz'd and deployed via Oracle ([ui-core](https://github.com/luthersystems/ui-core))

## Preset Author Constraints

- **Null validation:** Terraform does NOT short-circuit `||` in validation conditions. `var.x == null || contains([...], var.x)` fails when `x` is null. Always use a ternary: `var.x == null ? true : contains([...], var.x)`
- **Required variables:** Every preset should declare `project` and `region` variables — the composer always maps these
- **Defaults matter:** Variables without defaults become required root variables — the mapper MUST provide values or deploy fails
- **Wiring outputs:** Outputs used for cross-module wiring (e.g., `vpc_id`, `private_subnet_ids`) must be declared in `outputs.tf`
- **Project tag is required on every taggable AWS resource.** Use `tags = merge(module.name.tags, var.tags)` so the `Project` tag emitted by `module.name.tags` reaches the resource. The downstream reliable3 inspector filters on exact `Project = <project>` match, so untagged resources are invisible to drift detection and CloudWatch metrics (see issue #81, [reliable PR #1027](https://github.com/luthersystems/reliable/pull/1027)). If a resource accepts any tag-shaped attribute (including listeners, instance profiles, and IAM roles/policies in provider 5.x+), tag it.
- **GCP mirror:** every labelable GCP resource must set `labels = merge({ project = var.project }, var.labels)` (or equivalent) so project identity propagates. Enforced in CI by `tests/lint-project-label.sh` using an allowlist of label-capable resource types (see script header for how to extend).
- **Pre-plan validation surface:** `ComposeStackWithIssues` / `ComposeSingleWithIssues` run a battery of validators and return structured `[]ValidationIssue` so callers can correct multiple problems in one round-trip (instead of waiting for `terraform plan`). Validators check: missing required variables (aggregated across modules), value-type coercion, module wiring graph, dependency cycles, provider version constraint conflicts, sensitive-output propagation, and composed-root HCL parseability. `ValidateAll` aggregates the IR-level + post-composition checks for callers that don't go through `ComposeStack`. Set `StrictValidate: true` on the opts to escalate any non-empty Issues to an error. CI gates: `TestPresetDefaultsSatisfyValidations` (default vs validation drift), `TestEveryPresetHasResourceOrModuleCall` (placeholder allowlist in `pkg/composer/preset_defaults_test.go`), `TestKnownFieldsNoShrink` (golden at `pkg/composer/testdata/known_fields.golden`; re-seed with `UPDATE_GOLDEN=1`).

## Skills

Before starting a task, check if a matching skill exists and follow its workflow exactly. Multiple skills can chain: e.g., `/pickup-issue` uses the relevant add-module skill for implementation, `/verify` before committing, and `/pr` to ship.

| Task | Skill | File |
|------|-------|------|
| Run local CI validation | `/verify` | `.claude/skills/verify/SKILL.md` |
| Create a pull request | `/pr` | `.claude/skills/pr/SKILL.md` |
| Add a new AWS module | `/add-aws-module` | `.claude/skills/add-aws-module/SKILL.md` |
| Add a new GCP module | `/add-gcp-module` | `.claude/skills/add-gcp-module/SKILL.md` |
| Add an example stack | `/add-example` | `.claude/skills/add-example/SKILL.md` |
| Work on a GitHub issue | `/pickup-issue` | `.claude/skills/pickup-issue/SKILL.md` |
| Tag and release a version | `/release` | `.claude/skills/release/SKILL.md` |
| Audit modules for issues | `/audit` | `.claude/skills/audit/SKILL.md` |
