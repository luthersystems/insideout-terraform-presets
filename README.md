# InsideOut Terraform Presets

This repository contains the Terraform module presets used by [InsideOut](https://github.com/luthersystems/reliable) to generate cloud infrastructure.

## Structure

- `aws/`: Terraform modules for Amazon Web Services
- `gcp/`: Terraform modules for Google Cloud Platform

## Usage

These modules are designed to be composed together by the InsideOut engine. Each directory contains a standard Terraform module with `main.tf`, `variables.tf`, and `outputs.tf`.

## License

Apache License 2.0
