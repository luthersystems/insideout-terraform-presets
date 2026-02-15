variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project slug used for default naming/tags"
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

variable "table_name" {
  description = "Specific table name (defaults to <project>-app when null)"
  type        = string
  default     = null
  validation {
    condition     = var.table_name == null ? true : length(trimspace(var.table_name)) > 0
    error_message = "table_name, if set, must be a non-empty string."
  }
}

variable "billing_mode" {
  description = "PAY_PER_REQUEST or PROVISIONED"
  type        = string
  default     = "PAY_PER_REQUEST"
  validation {
    condition     = contains(["PAY_PER_REQUEST", "PROVISIONED"], var.billing_mode)
    error_message = "billing_mode must be PAY_PER_REQUEST or PROVISIONED."
  }
}

variable "read_capacity" {
  description = "Only used when billing_mode = PROVISIONED"
  type        = number
  default     = 5
  validation {
    condition     = var.read_capacity >= 1
    error_message = "read_capacity must be >= 1."
  }
}

variable "write_capacity" {
  description = "Only used when billing_mode = PROVISIONED"
  type        = number
  default     = 5
  validation {
    condition     = var.write_capacity >= 1
    error_message = "write_capacity must be >= 1."
  }
}

variable "hash_key" {
  description = "Partition key attribute name"
  type        = string
  default     = "pk"
  validation {
    condition     = length(trimspace(var.hash_key)) > 0
    error_message = "hash_key must be a non-empty string."
  }
}

variable "range_key" {
  description = "Sort key attribute name (leave empty to omit)"
  type        = string
  default     = ""
  # empty string allowed → no validation needed
}

variable "ttl_enabled" {
  description = "Enable TTL"
  type        = bool
  default     = false
}

variable "ttl_attribute" {
  description = "TTL attribute name (epoch seconds)"
  type        = string
  default     = "ttl"
  validation {
    condition     = length(trimspace(var.ttl_attribute)) > 0
    error_message = "ttl_attribute must be a non-empty string."
  }
}

variable "point_in_time_recovery" {
  description = "Enable point-in-time recovery"
  type        = bool
  default     = true
}

variable "stream_enabled" {
  description = "Enable DynamoDB streams"
  type        = bool
  default     = false
}

variable "stream_view_type" {
  description = "Stream view type (e.g., NEW_AND_OLD_IMAGES)"
  type        = string
  default     = "NEW_AND_OLD_IMAGES"
  validation {
    condition     = contains(["NEW_IMAGE", "OLD_IMAGE", "NEW_AND_OLD_IMAGES", "KEYS_ONLY"], var.stream_view_type)
    error_message = "stream_view_type must be one of: NEW_IMAGE, OLD_IMAGE, NEW_AND_OLD_IMAGES, KEYS_ONLY."
  }
}

variable "kms_key_arn" {
  description = "Optional CMK for SSE (null = AWS managed key)"
  type        = string
  default     = null
  validation {
    condition     = var.kms_key_arn == null ? true : length(trimspace(var.kms_key_arn)) > 0
    error_message = "kms_key_arn, if set, must be a non-empty string."
  }
}

variable "global_secondary_indexes" {
  description = <<EOT
List of GSIs:
[
  {
    name                = "gsi1",
    hash_key            = "gsi1pk",
    range_key           = "gsi1sk",            # optional
    projection_type     = "ALL",               # or KEYS_ONLY / INCLUDE
    non_key_attributes  = ["attr1","attr2"],   # only when projection_type=INCLUDE
    read_capacity       = 5,                   # used only when PROVISIONED
    write_capacity      = 5                    # used only when PROVISIONED
  }
]
EOT
  type = list(object({
    name               = string
    hash_key           = string
    range_key          = optional(string)
    projection_type    = string
    non_key_attributes = optional(list(string))
    read_capacity      = optional(number)
    write_capacity     = optional(number)
  }))
  default = []

  # Basic structural checks
  validation {
    condition = alltrue([
      for g in var.global_secondary_indexes :
      length(trimspace(g.name)) > 0
      && length(trimspace(g.hash_key)) > 0
      && contains(["ALL", "KEYS_ONLY", "INCLUDE"], g.projection_type)
    ])
    error_message = "Each GSI must have non-empty name/hash_key and a valid projection_type (ALL, KEYS_ONLY, or INCLUDE)."
  }

  # If non_key_attributes is provided, projection_type must be INCLUDE
  validation {
    condition = alltrue([
      for g in var.global_secondary_indexes :
      !(can(g.non_key_attributes) && g.non_key_attributes != null && length(g.non_key_attributes) > 0 && g.projection_type != "INCLUDE")
    ])
    error_message = "non_key_attributes may only be set when projection_type == INCLUDE."
  }
}

variable "tags" {
  description = "Additional tags"
  type        = map(string)
  default     = {}
}
