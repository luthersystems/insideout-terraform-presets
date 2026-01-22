variable "region" {
  description = "AWS region for WAFv2 API calls (for CLOUDFRONT scope, use us-east-1)"
  type        = string
  default     = "us-east-1"
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project/prefix used for naming"
  type        = string
  default     = "demo"
  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "scope" {
  description = "WAFv2 scope: CLOUDFRONT (global) or REGIONAL"
  type        = string
  default     = "CLOUDFRONT"
  validation {
    condition     = contains(["CLOUDFRONT", "REGIONAL"], var.scope)
    error_message = "scope must be CLOUDFRONT or REGIONAL."
  }
}

# NOTE: For REGIONAL associations you must provide a non-null resource_arn in root wiring.
variable "resource_arn" {
  description = "ARN of the protected resource (REGIONAL only; for CLOUDFRONT, pass WebACL ARN to CloudFront 'web_acl_id' instead)"
  type        = string
  default     = null
  validation {
    condition     = var.resource_arn == null ? true : length(trimspace(var.resource_arn)) > 0
    error_message = "If provided, resource_arn must be a non-empty string."
  }
}

variable "managed_rule_groups" {
  description = "List of managed rule groups to attach to the WebACL."
  type = list(object({
    name            = string
    vendor          = string
    priority        = number
    override_action = optional(string, "none") # allowed: none | count
  }))
  default = []
  validation {
    condition = alltrue([
      for g in var.managed_rule_groups :
      length(trimspace(g.name)) > 0 &&
      length(trimspace(g.vendor)) > 0 &&
      g.priority >= 0 &&
      contains(["none", "count"], lower(g.override_action))
    ])
    error_message = "Each managed_rule_group must have non-empty name/vendor, priority >= 0, and override_action of 'none' or 'count'."
  }
}

variable "tags" {
  description = "Resource tags"
  type        = map(string)
  default     = {}
}
