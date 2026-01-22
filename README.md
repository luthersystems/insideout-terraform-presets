# InsideOut Terraform Presets

This repository contains the standard, tested, and production-ready Terraform module presets used by [InsideOut](https://insideout.luthersystems.com).

## What is InsideOut?

InsideOut is the most streamlined way to build, configure, deploy, and manage your product infrastructure. It allows you to build, configure, deploy, and manage your infra 10x faster, letting you focus on your application instead of your infrastructure.

*   **Landing Page**: [insideout.luthersystems.com](https://insideout.luthersystems.com)
*   **Agent Prototype**: [insideout.luthersystemsapp.com](https://insideout.luthersystemsapp.com)
*   **Discord Community**: [insideout.luthersystems.com/discord](https://insideout.luthersystems.com/discord)
*   **Subreddit**: [r/luthersystems](https://www.reddit.com/r/luthersystems/)

## About These Presets

This repository serves as the library of standard and tested Terraform modules that are composed by InsideOut to generate complete cloud stacks. They are designed to be:
- **Modular**: Composable by nature.
- **Standardized**: Following cloud best practices and security defaults.
- **Tested**: Verified through the InsideOut deployment and inspection pipelines.

## Structure

- `aws/`: Terraform modules for Amazon Web Services (VPC, EKS, RDS, S3, etc.)
- `gcp/`: Terraform modules for Google Cloud Platform (VPC, GKE, Cloud Run, Cloud SQL, etc.)

## Usage

Each directory contains a standard Terraform module with `main.tf`, `variables.tf`, and `outputs.tf`. While these are optimized for composition by the InsideOut engine, they can also be used as standalone Terraform modules.

## License

Apache License 2.0
