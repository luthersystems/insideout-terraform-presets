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
}

variable "retry_minimum_backoff" {
  description = "Minimum retry backoff"
  type        = string
  default     = "10s"
}

variable "retry_maximum_backoff" {
  description = "Maximum retry backoff"
  type        = string
  default     = "600s"
}

variable "enable_dead_letter" {
  description = "Enable dead letter topic for failed messages"
  type        = bool
  default     = false
}
