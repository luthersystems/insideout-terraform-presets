mock_provider "google" {}

# Regression for #141. google_compute_url_map requires exactly one of
# default_service / default_url_redirect. An earlier revision left both
# unset when var.backends == [], so the GCP provider rejected the resource
# at apply time. The wrapper now renders a placeholder default_url_redirect
# whenever no backends are configured; these tests pin that branch.

run "empty_backends_uses_redirect_placeholder" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/n"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/s"
  }

  assert {
    condition     = google_compute_url_map.this.default_service == null
    error_message = "default_service must be null when no backends are configured"
  }

  assert {
    condition     = length(google_compute_url_map.this.default_url_redirect) == 1
    error_message = "Expected default_url_redirect block when backends is empty"
  }

  assert {
    condition     = google_compute_url_map.this.default_url_redirect[0].host_redirect == "placeholder.invalid"
    error_message = "Empty-backends URL map must redirect to placeholder.invalid"
  }

  assert {
    condition     = google_compute_url_map.this.default_url_redirect[0].redirect_response_code == "FOUND"
    error_message = "Empty-backends redirect must return 302 FOUND"
  }
}

run "with_backends_uses_default_service" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/n"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/s"
    backends = [
      {
        name           = "api"
        instance_group = "projects/test/zones/us-central1-a/instanceGroups/api"
      },
    ]
  }

  assert {
    condition     = length(google_compute_url_map.this.default_url_redirect) == 0
    error_message = "default_url_redirect must not be set when backends are configured"
  }

  assert {
    condition     = length(google_compute_backend_service.this) == 1
    error_message = "Expected one backend service to be created from var.backends"
  }
}
