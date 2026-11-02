variable "region" {
  description = "AWS region (e.g., us-east-1)"
  type        = string
}

variable "project" {
  description = "Name prefix for resources"
  type        = string
  default     = "demo"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "vpc_id" {
  description = "VPC ID where the ALB is deployed"
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnet IDs for the ALB"
  type        = list(string)
}

variable "internal" {
  description = "Make the ALB internal (no public address) instead of internet-facing. Use it for a stack that is reached only through CloudFront, a VPN or a peered network. Changing it replaces the ALB. Default false keeps the current internet-facing ALB."
  type        = bool
  default     = false
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for an internal ALB (internal = true). Empty (default) uses public_subnet_ids. Ignored when the ALB is internet-facing."
  type        = list(string)
  default     = []
}

variable "allow_cidrs" {
  description = "CIDR blocks allowed to access the ALB (HTTP/HTTPS)"
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "target_port" {
  description = "Port your application listens on"
  type        = number
  default     = 80
}

variable "target_protocol" {
  description = "Target group protocol"
  type        = string
  default     = "HTTP"
}

variable "target_type" {
  description = "Target type: instance | ip | lambda"
  type        = string
  default     = "instance"
  validation {
    condition     = contains(["instance", "ip", "lambda"], var.target_type)
    error_message = "target_type must be one of: instance, ip, lambda."
  }
}

variable "health_check_path" {
  description = "Health check path"
  type        = string
  default     = "/"
}

variable "health_check_protocol" {
  description = "Health check protocol"
  type        = string
  default     = "HTTP"
}

variable "certificate_arn" {
  description = "ACM certificate ARN (set to enable HTTPS + HTTP→HTTPS redirect)"
  type        = string
  default     = null
}

variable "enable_deletion_protection" {
  description = "Protect the ALB from accidental deletion"
  type        = bool
  default     = false
}

variable "drop_invalid_header_fields" {
  description = "Drop HTTP headers with invalid field names (anything outside [-A-Za-z0-9], e.g. underscores) at the ALB instead of forwarding them. On by default as a request-smuggling defense; set false only if the app needs such headers."
  type        = bool
  default     = true
}

variable "access_logs_retention_days" {
  description = "Days to retain ALB access logs in the in-module S3 bucket before expiration."
  type        = number
  default     = 90
  validation {
    condition     = var.access_logs_retention_days >= 1
    error_message = "access_logs_retention_days must be >= 1."
  }
}

variable "tags" {
  description = "Common resource tags"
  type        = map(string)
  default     = {}
}

variable "egress_cidrs" {
  description = "CIDR blocks the security group may send traffic to. Default 0.0.0.0/0 keeps the current open egress. Set it, for example to the VPC CIDR, to narrow egress. Narrowing changes the network behaviour of a live stack on its next apply (the rule updates in place). Check first that every target is in those CIDRs (IP targets in a peered VPC or on-premises) and that no listener rule uses authenticate-oidc, which calls the IdP over the internet."
  type        = list(string)
  default     = ["0.0.0.0/0"]
  validation {
    condition     = length(var.egress_cidrs) > 0 && alltrue([for c in var.egress_cidrs : can(cidrhost(c, 0))])
    error_message = "egress_cidrs must contain at least one valid CIDR block."
  }
}
