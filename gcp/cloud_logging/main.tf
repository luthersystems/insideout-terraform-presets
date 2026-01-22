resource "google_logging_project_sink" "sink" {
  name        = "main-sink"
  destination = "storage.googleapis.com/${var.project}-logs"
  filter      = "severity >= ERROR"
}
