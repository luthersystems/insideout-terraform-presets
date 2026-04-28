name                       = "orders-events"
project                    = google_project.main.project_id
kms_key_name               = google_kms_crypto_key.pubsub.id
message_retention_duration = null

labels = {
  environment = "staging"
}
