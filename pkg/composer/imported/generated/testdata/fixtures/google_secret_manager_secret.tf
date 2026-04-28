secret_id = "orders-api-key"
project   = google_project.main.project_id
expire_time = null

replication {
  auto {}
}

labels = {
  environment = "staging"
}
