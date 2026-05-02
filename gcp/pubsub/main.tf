# GCP Pub/Sub Topic and Subscription

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

# Per-deploy suffix so retries after state loss don't 409 on the topic /
# subscription / dead-letter-topic names (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

resource "google_pubsub_topic" "this" {
  project = var.project_id
  name    = "${var.project}-${var.topic_name}-${random_id.suffix.hex}"

  message_retention_duration = var.message_retention_duration

  labels = merge({
    project = var.project
    managed = "terraform"
  }, var.labels)
}

resource "google_pubsub_subscription" "this" {
  project = var.project_id
  name    = "${var.project}-${var.topic_name}-sub-${random_id.suffix.hex}"
  topic   = google_pubsub_topic.this.id

  ack_deadline_seconds       = var.ack_deadline_seconds
  message_retention_duration = var.subscription_message_retention
  retain_acked_messages      = var.retain_acked_messages

  expiration_policy {
    ttl = var.subscription_expiration_ttl
  }

  retry_policy {
    minimum_backoff = var.retry_minimum_backoff
    maximum_backoff = var.retry_maximum_backoff
  }

  labels = merge({
    project = var.project
    managed = "terraform"
  }, var.labels)
}

# Dead letter topic for failed messages
resource "google_pubsub_topic" "dead_letter" {
  count   = var.enable_dead_letter ? 1 : 0
  project = var.project_id
  name    = "${var.project}-${var.topic_name}-dlq-${random_id.suffix.hex}"

  labels = merge({
    project = var.project
    managed = "terraform"
  }, var.labels)
}
