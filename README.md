# InsideOut Terraform Presets

> **Note:** This project is currently in **Beta**. Always review the terraforms before deploying mission-critical production workloads.

This repository contains the standard, tested Terraform module presets used by [InsideOut](https://insideout.luthersystems.com) to generate cloud infrastructure.

## What is InsideOut?

InsideOut is a streamlined platform to build, configure, deploy, and manage your product infrastructure. It helps you get your infrastructure up and running faster, letting you focus on your application logic.

*   **Landing Page**: [insideout.luthersystems.com](https://insideout.luthersystems.com)
*   **Agent Prototype**: [insideout.luthersystemsapp.com](https://insideout.luthersystemsapp.com)
*   **Discord Community**: [insideout.luthersystems.com/discord](https://insideout.luthersystems.com/discord)
*   **Subreddit**: [r/luthersystems](https://www.reddit.com/r/luthersystems/)

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

This repo is imported as a Go module (`github.com/luthersystems/insideout-terraform-presets`) by the [reliable](https://github.com/luthersystems/reliable) backend. It exposes an embedded `fs.FS` filesystem (via `go:embed`) containing Terraform preset files (`.tf`, `.tfvars`, `.tmpl`) organized by cloud provider and component:

```
aws/vpc/          → variables.tf, main.tf
aws/lambda/       → variables.tf, main.tf
gcp/cloudsql/     → variables.tf, main.tf
...
```

The reliable repo's Terraform composition engine (`internal/reliabletf/`) reads these presets at build time and uses them to:

1. **Compose full Terraform stacks** — When a user designs infrastructure through the AI chat, the backend maps each selected component (e.g. `KeyVPC`, `KeyPostgres`) to a preset directory via `PresetKeyMap` in `contracts.go` (e.g. `KeyPostgres` → `aws/rds/`).
2. **Discover module variables** — Parses `variables.tf` from each preset to understand what inputs each module accepts, enabling dynamic variable injection from user-provided config.
3. **Rebase and merge** — Preset files are rebased into a unified directory structure under `modules/<component>/` and combined with a root `main.tf` that wires everything together.
4. **Send to Oracle** — The composed Terraform is sent to the Oracle deployment service which runs `terraform init/plan/apply`.

### Update workflow

After changing presets here, pull the latest version in the reliable repo:

```bash
go get github.com/luthersystems/insideout-terraform-presets@main
```

## Standalone Usage

Each directory contains a standard Terraform module with `main.tf`, `variables.tf`, and `outputs.tf`. While these are optimized for composition by the InsideOut engine, they can also be used as standalone Terraform modules.

## License

Apache License 2.0
