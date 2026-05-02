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

# tflint-ignore: terraform_unused_declarations  # stub module — var.github_org is reserved for the actual implementation; the module body is currently a placeholder
variable "github_org" {
  description = "GitHub organization or username"
  type        = string
  default     = ""
}

# tflint-ignore: terraform_unused_declarations  # stub module — var.github_repo is reserved for the actual implementation; the module body is currently a placeholder
variable "github_repo" {
  description = "GitHub repository name"
  type        = string
  default     = ""
}

