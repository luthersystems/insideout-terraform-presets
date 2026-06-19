mock_provider "google" {}

# gcp/compute GPU shape tests (#767). Verifies that:
#   - A GPU on an N1 machine attaches guest_accelerator (type + count) and
#     forces scheduling.on_host_maintenance = "TERMINATE" (GCP rejects live
#     migration for GPU instances).
#   - A non-GPU instance attaches no accelerator and keeps the normal
#     MIGRATE maintenance policy.
#   - The machine-type precondition rejects a GPU on a non-N1 machine and on a
#     bundled-GPU (G2/A2) machine (expect_failures on the instance resource).

run "gpu_vm_on_n1_attaches_accelerator_and_terminates" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/default"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/default"
    machine_type      = "n1-standard-4"
    gpu_type          = "nvidia-tesla-t4"
    gpu_count         = 2
  }

  assert {
    condition     = length(google_compute_instance.this.guest_accelerator) == 1
    error_message = "A GPU instance must attach exactly one guest_accelerator block."
  }

  assert {
    condition     = google_compute_instance.this.guest_accelerator[0].type == "nvidia-tesla-t4"
    error_message = "guest_accelerator.type must be the configured gpu_type."
  }

  assert {
    condition     = google_compute_instance.this.guest_accelerator[0].count == 2
    error_message = "guest_accelerator.count must be the configured gpu_count."
  }

  assert {
    condition     = google_compute_instance.this.scheduling[0].on_host_maintenance == "TERMINATE"
    error_message = "GPU instances must set on_host_maintenance=TERMINATE; GCP rejects live migration with a GPU attached."
  }
}

run "gpu_count_defaults_to_one" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/default"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/default"
    machine_type      = "n1-standard-4"
    gpu_type          = "nvidia-tesla-t4"
  }

  assert {
    condition     = google_compute_instance.this.guest_accelerator[0].count == 1
    error_message = "gpu_count must default to 1 when unset (preset default)."
  }
}

run "no_gpu_attaches_nothing_and_keeps_migrate" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/default"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/default"
    machine_type      = "e2-medium"
  }

  assert {
    condition     = length(google_compute_instance.this.guest_accelerator) == 0
    error_message = "A non-GPU instance must attach no guest_accelerator block."
  }

  assert {
    condition     = google_compute_instance.this.scheduling[0].on_host_maintenance == "MIGRATE"
    error_message = "A non-GPU, non-preemptible instance must keep the default MIGRATE maintenance policy."
  }
}

# -----------------------------------------------------------------------------
# Negative cases: the machine-type precondition (resource-hosted because it is a
# cross-variable rule) rejects GPU on incompatible machine types at plan time.
# expect_failures matches by resource address.
# -----------------------------------------------------------------------------

run "gpu_on_non_n1_machine_fails_precondition" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/default"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/default"
    machine_type      = "e2-standard-4" # not N1 — cannot attach a GPU
    gpu_type          = "nvidia-tesla-t4"
  }

  expect_failures = [
    google_compute_instance.this,
  ]
}

run "gpu_on_bundled_g2_machine_fails_precondition" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    network_self_link = "projects/test/global/networks/default"
    subnet_self_link  = "projects/test/regions/us-central1/subnetworks/default"
    machine_type      = "g2-standard-4" # G2 bundles its L4 GPU — guest_accelerator invalid
    gpu_type          = "nvidia-l4"
  }

  expect_failures = [
    google_compute_instance.this,
  ]
}
