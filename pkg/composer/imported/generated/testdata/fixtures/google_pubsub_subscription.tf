name                         = "orders-processor"
topic                        = google_pubsub_topic.orders.id
project                      = google_project.main.project_id
ack_deadline_seconds         = 30
filter                       = null
enable_exactly_once_delivery = false

retry_policy {
  minimum_backoff = "10s"
  maximum_backoff = "600s"
}

labels = {
  environment = "staging"
}
