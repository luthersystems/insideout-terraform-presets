# InsideOut Terraform Presets

> **Note:** This project is currently in **Beta**. Always review the terraforms before deploying mission-critical production workloads.

This repository contains the standard, tested Terraform module presets used by [InsideOut](https://insideout.luthersystems.com) to generate cloud infrastructure.

## What is InsideOut?

InsideOut is a streamlined platform to build, configure, deploy, and manage your product infrastructure. It helps you get your infrastructure up and running faster, letting you focus on your application logic.

*   **Landing Page**: [insideout.luthersystems.com](https://insideout.luthersystems.com)
*   **Agent Prototype**: [insideout.luthersystemsapp.com](https://insideout.luthersystemsapp.com)
*   **Discord Community**: [insideout.luthersystems.com/discord](https://insideout.luthersystems.com/discord)
*   **Subreddit**: [r/luthersystems](https://www.reddit.com/r/luthersystems/)
*   **Kiro IDE Power**: [insideout-power](https://github.com/luthersystems/insideout-power) — Kiro IDE Power for AI-powered cloud infrastructure design

## About These Presets

This repository serves as the library of standard Terraform modules that are composed by InsideOut to generate complete cloud stacks. They are designed to be:
- **Modular**: Composable by nature.
- **Standardized**: Following cloud best practices and security defaults.
- **Tested**: Verified through the InsideOut deployment and inspection pipelines.

*Based on [Luther Enterprise Terraform Modules](https://github.com/luthersystems/tf-modules).*

## Structure

- `aws/`: Terraform modules for Amazon Web Services (VPC, EKS, RDS, S3, etc.)
- `gcp/`: Terraform modules for Google Cloud Platform (VPC, GKE, Cloud Run, Cloud SQL, etc.)

## How InsideOut Consumes These Presets

This repo is imported as a Go module (`github.com/luthersystems/insideout-terraform-presets`) by the InsideOut backend. It exposes an embedded `fs.FS` filesystem (via `go:embed`) containing Terraform preset files (`.tf`, `.tfvars`, `.tmpl`) organized by cloud provider and component:

```
aws/vpc/          → variables.tf, main.tf
aws/lambda/       → variables.tf, main.tf
gcp/cloudsql/     → variables.tf, main.tf
...
```

The InsideOut Terraform composition engine reads these presets at build time and uses them to:

1. **Compose full Terraform stacks** — When a user designs infrastructure through the AI chat, the backend maps each selected component (e.g. `KeyVPC`, `KeyPostgres`) to a preset directory (e.g. `KeyPostgres` → `aws/rds/`).
2. **Discover module variables** — Parses `variables.tf` from each preset to understand what inputs each module accepts, enabling dynamic variable injection from user-provided config.
3. **Rebase and merge** — Preset files are rebased into a unified directory structure under `modules/<component>/` and combined with a root `main.tf` that wires everything together.
4. **Apply** — The composed Terraform is handed to the InsideOut deployment service which runs `terraform init/plan/apply`.

## Generated Consumer Artifacts

Downstream consumers should prefer `cmd/imported-codegen` over hand-maintained mirrors when they need imported-resource metadata in TypeScript or JSON. The generator already emits:

- `zod`: TypeScript Zod schemas and registries for supported imported Terraform types.
- `policy-ts`: TypeScript policy projections from `pkg/composer/imported/policy`.
- `labels`, `capabilities`, `dependencies`, `drift-fields`: JSON metadata artifacts for UI/runtime consumers.
- `managed-map`: TypeScript `MANAGED_TO_IMPORTED_TFTYPE`, generated from `pkg/imported.ManagedComponentPrimaryTFTypes`.

Example:

```bash
go run ./cmd/imported-codegen managed-map --output /path/to/reliable/lib/stack/managed-imported-map.ts
```

When adding a new cross-repo mirror, check this generator path first so the canonical data stays in presets and consumer repos regenerate it.

## Standalone Usage

Each directory contains a standard Terraform module with `main.tf`, `variables.tf`, and `outputs.tf`. While these are optimized for composition by the InsideOut engine, they can also be used as standalone Terraform modules.

## License

Apache License 2.0
