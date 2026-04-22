variable "project" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region (unused — Cloud Armor policies are global; kept for composer convention)"
  type        = string
  default     = "us-central1"
}

variable "name" {
  description = "Name suffix for the security policy (prefixed with project)"
  type        = string
  default     = "policy"
}

variable "description" {
  description = "Description of the security policy"
  type        = string
  default     = "Cloud Armor policy managed by Terraform"
}

variable "default_action" {
  description = "Action for the default (catch-all) rule. 'allow' or a 'deny(<code>)' variant."
  type        = string
  default     = "allow"

  validation {
    condition     = contains(["allow", "deny(403)", "deny(404)", "deny(502)"], var.default_action)
    error_message = "default_action must be one of: allow, deny(403), deny(404), deny(502)."
  }
}

variable "rules" {
  description = "Custom IP-range rules. Priorities must be unique and below 2147483647."
  type = list(object({
    priority      = number
    action        = string
    description   = string
    src_ip_ranges = list(string)
  }))
  default = []

  validation {
    condition     = alltrue([for r in var.rules : r.priority < 2147483647 && r.priority >= 0])
    error_message = "Each rule priority must be in [0, 2147483646]."
  }
}

variable "preconfigured_waf_rules" {
  description = "Google-provided WAF expressions (e.g., 'sqli-v33-stable', 'xss-v33-stable'). See https://cloud.google.com/armor/docs/rule-tuning."
  type = list(object({
    priority   = number
    action     = string
    expression = string
  }))
  default = []

  validation {
    condition     = alltrue([for r in var.preconfigured_waf_rules : r.priority < 2147483647 && r.priority >= 0])
    error_message = "Each preconfigured_waf_rules priority must be in [0, 2147483646]."
  }
}

variable "rate_limit" {
  description = "Optional rate-based ban applied to all source IPs. Null disables rate limiting."
  type = object({
    priority         = number
    count            = number
    interval_sec     = number
    enforce_on_key   = string
    exceed_action    = string
    ban_duration_sec = number
  })
  default = null

  validation {
    condition     = var.rate_limit == null ? true : contains(["IP", "ALL", "HTTP_HEADER", "XFF_IP", "HTTP_COOKIE", "HTTP_PATH", "SNI", "REGION_CODE"], var.rate_limit.enforce_on_key)
    error_message = "rate_limit.enforce_on_key must be a valid Cloud Armor enforce key."
  }
}

variable "adaptive_protection_enabled" {
  description = "Enable Layer 7 DDoS adaptive protection"
  type        = bool
  default     = false
}

variable "labels" {
  description = "Labels to apply"
  type        = map(string)
  default     = {}
}
