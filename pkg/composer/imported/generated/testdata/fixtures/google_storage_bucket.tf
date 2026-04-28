name                        = "io-buqiks112yag-assets"
location                    = "US"
storage_class               = "STANDARD"
uniform_bucket_level_access = true
project                     = google_project.main.project_id
force_destroy               = null

versioning {
  enabled = true
}

labels = {
  environment = "staging"
}
