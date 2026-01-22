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

variable "queue_name" {
  description = "Base queue name ('.fifo' is auto-appended for FIFO)"
  type        = string
  default     = null

  validation {
    condition     = var.queue_name == null ? true : length(trimspace(var.queue_name)) > 0
    error_message = "queue_name must be null or a non-empty string."
  }
}

variable "queue_type" {
  description = "Queue type"
  type        = string
  default     = "Standard"

  validation {
    condition     = contains(["Standard", "FIFO"], var.queue_type)
    error_message = "queue_type must be 'Standard' or 'FIFO'."
  }
}

variable "content_based_deduplication" {
  description = "For FIFO queues, enable content-based deduplication"
  type        = bool
  default     = true
}

variable "visibility_timeout_seconds" {
  description = "How long a message is hidden after a consumer receives it"
  type        = number
  default     = 30

  validation {
    condition     = var.visibility_timeout_seconds >= 0 && var.visibility_timeout_seconds <= 43200
    error_message = "visibility_timeout_seconds must be between 0 and 43200 seconds."
  }
}

variable "message_retention_seconds" {
  description = "How long to retain messages in the queue"
  type        = number
  default     = 345600 # 4 days

  validation {
    condition     = var.message_retention_seconds >= 60 && var.message_retention_seconds <= 1209600
    error_message = "message_retention_seconds must be between 60 and 1,209,600 seconds."
  }
}

variable "delay_seconds" {
  description = "Default delivery delay for new messages"
  type        = number
  default     = 0

  validation {
    condition     = var.delay_seconds >= 0 && var.delay_seconds <= 900
    error_message = "delay_seconds must be between 0 and 900 seconds."
  }
}

variable "receive_wait_time_seconds" {
  description = "Long polling wait time for ReceiveMessage"
  type        = number
  default     = 10

  validation {
    condition     = var.receive_wait_time_seconds >= 0 && var.receive_wait_time_seconds <= 20
    error_message = "receive_wait_time_seconds must be between 0 and 20 seconds."
  }
}

variable "max_message_size" {
  description = "Max message size in bytes (1024–262144)"
  type        = number
  default     = 262144

  validation {
    condition     = var.max_message_size >= 1024 && var.max_message_size <= 262144
    error_message = "max_message_size must be between 1,024 and 262,144 bytes."
  }
}

# FIFO-only tuning
variable "fifo_throughput_limit" {
  description = "FIFO throughput limit (perQueue | perMessageGroupId)"
  type        = string
  default     = "perQueue"

  validation {
    condition     = contains(["perQueue", "perMessageGroupId"], var.fifo_throughput_limit)
    error_message = "fifo_throughput_limit must be 'perQueue' or 'perMessageGroupId'."
  }
}

variable "deduplication_scope" {
  description = "FIFO deduplication scope (queue | messageGroup)"
  type        = string
  default     = "queue"

  validation {
    condition     = contains(["queue", "messageGroup"], var.deduplication_scope)
    error_message = "deduplication_scope must be 'queue' or 'messageGroup'."
  }
}

# DLQ
variable "enable_dlq" {
  description = "Create and wire a dead-letter queue"
  type        = bool
  default     = true
}

variable "max_receive_count" {
  description = "Moves message to DLQ after this many receives"
  type        = number
  default     = 5

  validation {
    condition     = var.max_receive_count >= 1
    error_message = "max_receive_count must be >= 1."
  }
}

variable "dlq_message_retention_seconds" {
  description = "Retention for messages in the DLQ"
  type        = number
  default     = 1209600 # 14 days

  validation {
    condition     = var.dlq_message_retention_seconds >= 60 && var.dlq_message_retention_seconds <= 1209600
    error_message = "dlq_message_retention_seconds must be between 60 and 1,209,600 seconds."
  }
}

# Encryption
variable "sse_mode" {
  description = "Server-side encryption mode: NONE | SQS_MANAGED | KMS"
  type        = string
  default     = "SQS_MANAGED"

  validation {
    condition     = contains(["NONE", "SQS_MANAGED", "KMS"], var.sse_mode)
    error_message = "sse_mode must be NONE, SQS_MANAGED, or KMS."
  }
}

variable "kms_master_key_id" {
  description = "KMS key ARN/ID (required when sse_mode = \"KMS\")"
  type        = string
  default     = null

  # Keep validation self-contained: either null, or a non-empty string.
  validation {
    condition     = var.kms_master_key_id == null ? true : length(trimspace(var.kms_master_key_id)) > 0
    error_message = "kms_master_key_id must be null or a non-empty string."
  }
}

variable "kms_data_key_reuse_period_seconds" {
  description = "How long KMS data keys are reused (60–86400)"
  type        = number
  default     = 300

  validation {
    condition     = var.kms_data_key_reuse_period_seconds >= 60 && var.kms_data_key_reuse_period_seconds <= 86400
    error_message = "kms_data_key_reuse_period_seconds must be between 60 and 86,400 seconds."
  }
}

variable "tags" {
  description = "Additional resource tags"
  type        = map(string)
  default     = {}
}
