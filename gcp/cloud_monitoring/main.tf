resource "google_monitoring_dashboard" "dashboard" {
  dashboard_json = jsonencode({
    displayName = "Main Dashboard"
    gridLayout = {
      columns = "2"
      widgets = []
    }
  })
}
