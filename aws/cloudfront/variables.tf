variable "region" {
  description = "AWS region (used if creating an S3 origin)"
  type        = string
  default     = "us-east-1"
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Name/prefix for resources"
  type        = string
  default     = "demo"
  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "origin_type" {
  description = "Origin type for CloudFront: 's3' or 'http' (custom origin like an ALB)"
  type        = string
  default     = "s3"
  validation {
    condition     = contains(["s3", "http"], var.origin_type)
    error_message = "origin_type must be 's3' or 'http'."
  }
}

/* ----------------- S3 origin options ----------------- */

variable "create_bucket" {
  description = "Create an S3 bucket for origin (demo). If false, use s3_bucket_name."
  type        = bool
  default     = true
}

variable "s3_bucket_name" {
  description = "Existing S3 bucket name to use as origin (when origin_type='s3' and create_bucket=false)"
  type        = string
  default     = null
  validation {
    # allow null, otherwise look like a bucket name
    condition     = var.s3_bucket_name == null || can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.s3_bucket_name))
    error_message = "s3_bucket_name must be null or a valid S3 bucket name."
  }
}

/* ------------- Custom (HTTP) origin options ------------- */

variable "custom_origin_domain" {
  description = "Domain name of the custom origin (required when origin_type='http')"
  type        = string
  default     = null
  validation {
    # allow null, otherwise look like a hostname
    condition     = var.custom_origin_domain == null || can(regex("^[A-Za-z0-9.-]+\\.[A-Za-z]{2,}$", var.custom_origin_domain))
    error_message = "custom_origin_domain must be null or a valid hostname (e.g., alb-123.us-east-1.elb.amazonaws.com)."
  }
}

variable "origin_path" {
  description = "Optional origin path, e.g., '/public'"
  type        = string
  default     = ""
}

/* ---------------- Behavior / caching ---------------- */

variable "default_ttl_seconds" {
  description = "Default TTL (seconds)"
  type        = number
  default     = 3600
  validation {
    condition     = var.default_ttl_seconds >= 0
    error_message = "default_ttl_seconds must be >= 0."
  }
}

variable "default_root_object" {
  description = "Serve this object at root (optional)"
  type        = string
  default     = null
}

variable "forward_query_string" {
  description = "Forward query strings to origin (can reduce caching)"
  type        = bool
  default     = false
}

variable "forward_cookies" {
  description = "Forward cookies to origin (can reduce caching)"
  type        = bool
  default     = false
}

/* ------------ Price class and cert/aliases ------------ */

variable "price_class" {
  description = "CloudFront price class"
  type        = string
  default     = "PriceClass_100"
  validation {
    condition     = contains(["PriceClass_100", "PriceClass_200", "PriceClass_All"], var.price_class)
    error_message = "price_class must be one of: PriceClass_100, PriceClass_200, PriceClass_All."
  }
}

variable "aliases" {
  description = "Alternate domain names (CNAMEs)"
  type        = list(string)
  default     = []
  validation {
    condition     = length(var.aliases) == 0 || alltrue([for h in var.aliases : can(regex("^[A-Za-z0-9.-]+\\.[A-Za-z]{2,}$", h))])
    error_message = "Each alias must be a valid hostname (e.g., example.com, app.example.org)."
  }
}

variable "acm_certificate_arn" {
  description = "ACM cert ARN for HTTPS with custom domain (in us-east-1 for CloudFront)"
  type        = string
  default     = null
  validation {
    # allow null, otherwise look like an ACM certificate ARN (ideally in us-east-1)
    condition     = var.acm_certificate_arn == null || can(regex("^arn:aws:acm:us-east-1:[0-9]{12}:certificate\\/.+$", var.acm_certificate_arn))
    error_message = "acm_certificate_arn must be null or an ACM certificate ARN in us-east-1."
  }
}

/* ------ Attach WAFv2 WebACL to CloudFront (ARN) ------ */

variable "web_acl_id" {
  description = "WAFv2 Web ACL ARN to associate with this distribution (set by WAF module)"
  type        = string
  default     = null
  validation {
    # allow null, otherwise look like a WAFv2 global WebACL ARN
    condition     = var.web_acl_id == null || can(regex("^arn:aws:wafv2:us-east-1:[0-9]{12}:global\\/webacl\\/.+\\/.+$", var.web_acl_id))
    error_message = "web_acl_id must be null or a valid WAFv2 global WebACL ARN."
  }
}

/* ----------------------- Logging ---------------------- */

variable "enable_logging" {
  description = "Enable access logging"
  type        = bool
  default     = false
}

variable "logging_bucket_domain" {
  description = "Logging bucket domain (e.g., 'my-logs-bucket.s3.amazonaws.com')"
  type        = string
  default     = null
  validation {
    condition     = var.logging_bucket_domain == null || can(regex("^[A-Za-z0-9.-]+\\.s3\\.amazonaws\\.com$", var.logging_bucket_domain))
    error_message = "logging_bucket_domain must be null or look like '<bucket>.s3.amazonaws.com'."
  }
}

variable "logging_prefix" {
  type    = string
  default = "cloudfront/"
}

variable "custom_error_responses" {
  description = "Optional list of custom error responses"
  type = list(object({
    error_code            = number
    response_code         = optional(number)
    response_page_path    = optional(string)
    error_caching_min_ttl = optional(number)
  }))
  default = []
}

variable "tags" {
  type    = map(string)
  default = {}
}
