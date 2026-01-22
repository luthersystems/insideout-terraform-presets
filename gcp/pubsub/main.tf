# GCP Pub/Sub Topic and Subscription

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

resource "google_pubsub_topic" "this" {
  project = var.project
  name    = "${var.project}-${var.topic_name}"

  message_retention_duration = var.message_retention_duration

  labels = {
    project = var.project
    managed = "terraform"
  }
}

resource "google_pubsub_subscription" "this" {
  project = var.project
  name    = "${var.project}-${var.topic_name}-sub"
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

  labels = {
    project = var.project
    managed = "terraform"
  }
}

# Dead letter topic for failed messages
resource "google_pubsub_topic" "dead_letter" {
  count   = var.enable_dead_letter ? 1 : 0
  project = var.project
  name    = "${var.project}-${var.topic_name}-dlq"

  labels = {
    project = var.project
    managed = "terraform"
  }
}
