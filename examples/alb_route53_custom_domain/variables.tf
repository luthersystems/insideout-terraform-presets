variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  default     = "sandbox"
}

variable "project" {
  description = "Project name prefix (always wired by the composer at the root)"
  type        = string
  default     = ""
}

# tflint-ignore: terraform_unused_declarations  # composer always wires var.region at the root (CLAUDE.md mandate)
variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

# --- VPC -------------------------------------------------------------------

variable "vpc_project" {
  type = string
}

variable "vpc_region" {
  type = string
}

# --- ALB -------------------------------------------------------------------

variable "alb_project" {
  type = string
}

variable "alb_region" {
  type = string
}

# --- Route 53 --------------------------------------------------------------

variable "route53_project" {
  type = string
}

variable "route53_region" {
  type = string
}

variable "route53_domain_name" {
  description = "Apex domain for the hosted zone. Use a reserved test-only TLD (example.invalid / .test) in CI so a stray apply cannot collide with a registered domain."
  type        = string
}

variable "route53_create_zone" {
  description = "If true, create a brand new hosted zone for var.route53_domain_name. If false, supply an existing zone_id directly in main.tf via module.route53.zone_id."
  type        = bool
  default     = true
}
