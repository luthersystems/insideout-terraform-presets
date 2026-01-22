# InsideOut Terraform Presets

> **Note:** This project is currently in **Beta** and is considered a prototype. It is not yet recommended for mission-critical production workloads without review.

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

## Usage

Each directory contains a standard Terraform module with `main.tf`, `variables.tf`, and `outputs.tf`. While these are optimized for composition by the InsideOut engine, they can also be used as standalone Terraform modules.

## License

Apache License 2.0
