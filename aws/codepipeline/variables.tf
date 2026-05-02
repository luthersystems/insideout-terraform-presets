# tflint-ignore: terraform_unused_declarations  # composer always wires var.project at the root (CLAUDE.md mandate)
variable "project" {
  description = "Project name for resource naming"
  type        = string
}

# tflint-ignore: terraform_unused_declarations  # composer always wires var.environment at the root (CLAUDE.md mandate)
variable "environment" {
  description = "Environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

# tflint-ignore: terraform_unused_declarations  # stub module — var.source_repository is reserved for the actual implementation; the module body is currently a placeholder
variable "source_repository" {
  description = "Source code repository (CodeCommit repo name or GitHub connection)"
  type        = string
  default     = ""
}

# tflint-ignore: terraform_unused_declarations  # stub module — var.branch is reserved for the actual implementation; the module body is currently a placeholder
variable "branch" {
  description = "Branch to build from"
  type        = string
  default     = "main"
}

