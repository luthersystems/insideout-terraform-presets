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
- **GCP `var.project` vs `var.project_id` (issue #157):** these are two different things — never conflate them. `var.project` is the stack naming/label prefix and may legally hold values like `"io-abc123"` that aren't valid GCP project IDs. `var.project_id` is the real GCP project ID where resources are created (e.g. `"my-prod-12345"`). Every GCP module that creates resources in a project must declare both, and every `project = ...` argument on a `google_*` resource — and every vendored sub-module's `project_id = ...` — must reference `var.project_id`, never `var.project`. The `${var.project_id}.svc.id.goog` workload identity pool name in `gcp/gke` is a real-project-ID use too. Resource name interpolations (`name = "${var.project}-..."`) and label values (`labels = merge({ project = var.project }, var.labels)`) keep using `var.project` so the lint script and reliable3 inspector grouping continue to work. The composer surfaces `gcp_project_id_required` / `gcp_invalid_project_id` ValidationIssues at compose time, so callers see the misconfiguration before Oracle is called. Callers populate the new field via `ComposeStackOpts.GCPProjectID` (and `ComposeSingleOpts.GCPProjectID`).
- **Pre-plan validation surface:** `ComposeStackWithIssues` / `ComposeSingleWithIssues` run a battery of validators and return structured `[]ValidationIssue` so callers can correct multiple problems in one round-trip (instead of waiting for `terraform plan`). Validators check: missing required variables (aggregated across modules), value-type coercion, module wiring graph, dependency cycles, provider version constraint conflicts, sensitive-output propagation, and composed-root HCL parseability. `ValidateAll` aggregates the IR-level + post-composition checks for callers that don't go through `ComposeStack`. Set `StrictValidate: true` on the opts to escalate any non-empty Issues to an error. CI gates: `TestPresetDefaultsSatisfyValidations` (default vs validation drift), `TestEveryPresetHasResourceOrModuleCall` (placeholder allowlist in `pkg/composer/preset_defaults_test.go`), `TestKnownFieldsNoShrink` (golden at `pkg/composer/testdata/known_fields.golden`; re-seed with `UPDATE_GOLDEN=1`).
- **Root-only blocks are forbidden in presets (issue #199):** Terraform 1.5+ permits `import {}` and `removed {}` blocks **only in the root module**. Every preset in this repo is consumed as a *child* module by the composer (`luthersystems/reliable` emits `module "<key>" { source = "..." }` in a generated root), so any `import {}` or `removed {}` block placed in `aws/<m>/*.tf` or `gcp/<m>/*.tf` will fail `terraform init` with `An import block was detected in "module.<name>". Import blocks are only allowed in the root module.` This regressed v0.7.0 (#199) — standalone `terraform validate` accepted the block because the preset was treated as the root, but the bundle failed init at customer deploy time. Enforced in CI by `tests/lint-no-root-only-blocks.sh` (static grep) and the `validate-presets-as-child` workflow job (wraps each preset and runs `terraform init`). The same rule applies to `provider {}` blocks with arguments — root-only since TF 0.13.
- **Idempotency contract (issues #197, #199):** every preset must support back-to-back `terraform destroy` → `terraform apply` cycles without manual intervention. Resources backed by cloud-API singletons that can't truly be deleted — Identity Platform, Firestore databases, KMS key rings, project-level service activations — must be modeled as **adoption**, not creation, because the provider's CREATE will reject the second apply with `400 INVALID_*` once the underlying GCP/AWS state survives the destroy. The adoption mechanism is a root-level `import { to = module.<name>.<resource> ... }` block — but per the rule above, the preset itself **cannot** declare it. The composer must emit the import block alongside the module instantiation; until that lands, presets pin `lifecycle { ignore_changes = all }` and document that callers running against pre-existing singletons must `terraform import` out-of-band before the first apply. See `gcp/identity_platform/main.tf` for the current shape (CREATE + `ignore_changes = all`, header comment links #199). For race-prone resources whose CREATE is order-sensitive (e.g. GCP IAM eventual consistency before downstream resource validation), insert an explicit `time_sleep` between the binding and the dependent — see `gcp/cloud_build/main.tf::time_sleep.wait_iam_propagation` (90s covers ~p99 propagation; only fires on creation). Today's known singleton candidates: `gcp_identity_platform` (#197 mitigated, #199 composer-emit-import follow-up). Audit follow-up: `gcp_firestore` (singleton database — once created, the project's default database can't be removed; needs the same adoption treatment, blocked on the same composer change).

## Shared / Helper Modules (issue #203)

Three reserved buckets hold internal helper modules consumed by other presets
via `source = "../_shared/<name>"` (per-cloud) or
`source = "../../_shared/<name>"` (cross-cloud). They are NOT top-level
presets — the composer skips any directory whose name begins with `_` when
listing preset keys, so they never get a `module "<key>" {}` block in the
composed root.

| Path | Scope | Examples |
|---|---|---|
| `aws/_shared/<name>/` | AWS-only helpers | AWS tag merging, ARN parsing, account-ID validation, S3 bucket-name sanitization |
| `gcp/_shared/<name>/` | GCP-only helpers | Singleton existence probe (#202 inline precedent), GCP label merging, project / project_id split helpers |
| `_shared/<name>/` (top-level) | Cloud-agnostic helpers | Severity tagging conventions (#204), runbook URL prefix builders, naming-prefix normalization, time/date utilities |

### Conventions

- **Leading-underscore signals "not a top-level preset."** The composer's
  `ListPresetKeysForCloud` and `ListClouds` skip `_*` dir entries via
  `composer.isInternalDirName`. Do not work around this — if a directory
  needs to be enumerated as a preset, name it without the underscore.
- **Per-cloud isolation.** GCP-only stacks must not pull AWS helpers into
  their composed workspace, and vice versa. AWS helpers go in
  `aws/_shared/`; GCP helpers go in `gcp/_shared/`. Cross-cloud helpers go
  in top-level `_shared/`.
- **Cross-cloud helpers MUST NOT declare cloud-specific providers.**
  Modules under top-level `_shared/` may not declare `aws`, `google`,
  `google-beta`, `azurerm`, etc. — they ride along with both AWS-only and
  GCP-only stacks, so dragging in a cloud-specific provider would force
  every consumer to install it. Enforced by
  `tests/lint-shared-no-cloud-providers.sh`. If a helper genuinely needs
  to touch a cloud API, it belongs in a per-cloud bucket.
- **Plan-time-known outputs.** If a consumer's `count` / `for_each` will
  depend on a shared-module output, the output must be plan-time-known. The
  v0.7.2 inline existence-probe (PR #202) is the canonical example —
  `data.http` of a deterministic-name resource is plan-time-known iff the
  inputs are plan-time-known.
- **Module-package boundaries.** Terraform rejects `../` source traversal
  across module package boundaries with `Local module path escapes module
  package`. The composer (`luthersystems/reliable`) is responsible for
  bundling shared modules into the same workspace as their consumers so the
  relative source path resolves within a single package. For local
  `tests/validate-as-child.sh` runs, the script auto-detects
  `source = "../_shared/..."` references and copies the referenced helpers
  into the synthetic child-module package alongside the wrapped preset.
- **Embed coverage.** New file extensions in `_shared` buckets need
  matching `//go:embed` directives in `zz_embed.go` (the existing globs
  cover `.tf` for all three buckets and `.tmpl` for AWS top-level only —
  add a glob if a shared module needs other extensions). Each `_shared`
  glob requires at least one matching file at compile time; the
  `_smoke` placeholder fixtures in each bucket satisfy this until real
  shared modules land.
- **No real shared modules ship in this repo yet.** Issue #203 set up the
  framework / plumbing only. The `gcp/identity_platform` inline existence
  probe from PR #202 stays inline for now; conversion to
  `gcp/_shared/<name>` is a follow-up PR once a second consumer materializes
  or once the framework is exercised by a real cross-cloud helper (likely
  the severity-tagging convention from #204).

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
