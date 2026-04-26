variable "project" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "topic_name" {
  description = "Name of the Pub/Sub topic"
  type        = string
  default     = "main"
}

variable "message_retention_duration" {
  description = "Message retention duration for the topic"
  type        = string
  default     = "604800s" # 7 days
  validation {
    condition     = can(regex("^[0-9]+s$", var.message_retention_duration))
    error_message = "message_retention_duration must be a duration in whole seconds, e.g. 604800s."
  }
}

variable "ack_deadline_seconds" {
  description = "Acknowledgment deadline in seconds"
  type        = number
  default     = 20
}

variable "subscription_message_retention" {
  description = "Message retention duration for the subscription"
  type        = string
  default     = "604800s" # 7 days
  validation {
    condition     = can(regex("^[0-9]+s$", var.subscription_message_retention))
    error_message = "subscription_message_retention must be a duration in whole seconds, e.g. 604800s."
  }
}

variable "retain_acked_messages" {
  description = "Retain acknowledged messages"
  type        = bool
  default     = false
}

variable "subscription_expiration_ttl" {
  description = "Subscription expiration TTL (empty string for never)"
  type        = string
  default     = "" # Never expire
  validation {
    condition     = var.subscription_expiration_ttl == "" ? true : can(regex("^[0-9]+s$", var.subscription_expiration_ttl))
    error_message = "subscription_expiration_ttl must be empty or a duration in whole seconds, e.g. 86400s."
  }
}

variable "retry_minimum_backoff" {
  description = "Minimum retry backoff"
  type        = string
  default     = "10s"
  validation {
    condition     = can(regex("^[0-9]+s$", var.retry_minimum_backoff))
    error_message = "retry_minimum_backoff must be a duration in whole seconds, e.g. 10s."
  }
}

variable "retry_maximum_backoff" {
  description = "Maximum retry backoff"
  type        = string
  default     = "600s"
  validation {
    condition     = can(regex("^[0-9]+s$", var.retry_maximum_backoff))
    error_message = "retry_maximum_backoff must be a duration in whole seconds, e.g. 600s."
  }
}

variable "enable_dead_letter" {
  description = "Enable dead letter topic for failed messages"
  type        = bool
  default     = false
}
