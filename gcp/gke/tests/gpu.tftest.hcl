mock_provider "google" {}
mock_provider "kubernetes" {}
mock_provider "random" {}

# gcp/gke GPU node-pool shape tests (#767). Verifies that:
#   - A GPU on an N1 machine resolves the node-pool accelerator config (type +
#     count + auto-driver version) that the vendored gke module wires into
#     guest_accelerator { ... gpu_driver_installation_config { ... } }.
#   - A non-GPU node pool resolves an inert accelerator config (count 0, no
#     driver) so the module emits no guest_accelerator block.
#   - The machine-type precondition (hosted on random_id.suffix because the node
#     pool lives inside the module) rejects a GPU on a non-N1 machine and on a
#     bundled-GPU (A3) machine (expect_failures on random_id.suffix).
#
# Assertions read the gpu_node_pool output: the gke module's node-pool resources
# are not directly addressable from tftest (only module outputs are), so the
# preset surfaces the resolved accelerator config it feeds the module.

run "gpu_nodepool_on_n1_resolves_accelerator_and_driver" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    network_self_link   = "projects/test/global/networks/default"
    subnet_self_link    = "projects/test/regions/us-central1/subnetworks/default"
    pods_range_name     = "pods"
    services_range_name = "svcs"
    regional            = false
    node_zones          = ["us-central1-a"]
    machine_type        = "n1-standard-4"
    gpu_type            = "nvidia-tesla-t4"
    gpu_count           = 2
  }

  assert {
    condition     = output.gpu_node_pool.enabled == true
    error_message = "A GPU node pool must report enabled=true."
  }

  assert {
    condition     = output.gpu_node_pool.type == "nvidia-tesla-t4"
    error_message = "node-pool accelerator type must be the configured gpu_type."
  }

  assert {
    condition     = output.gpu_node_pool.count == 2
    error_message = "node-pool accelerator count must be the configured gpu_count."
  }

  assert {
    condition     = output.gpu_node_pool.driver_version == "DEFAULT"
    error_message = "GKE auto driver install must default to DEFAULT (gpu_driver_installation_config.gpu_driver_version)."
  }
}

run "gpu_count_defaults_to_one_and_driver_override" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    network_self_link   = "projects/test/global/networks/default"
    subnet_self_link    = "projects/test/regions/us-central1/subnetworks/default"
    pods_range_name     = "pods"
    services_range_name = "svcs"
    regional            = false
    node_zones          = ["us-central1-a"]
    machine_type        = "n1-standard-8"
    gpu_type            = "nvidia-tesla-v100"
    gpu_driver_version  = "LATEST"
  }

  assert {
    condition     = output.gpu_node_pool.count == 1
    error_message = "gpu_count must default to 1 when unset."
  }

  assert {
    condition     = output.gpu_node_pool.driver_version == "LATEST"
    error_message = "gpu_driver_version override (LATEST) must flow into the node-pool driver config."
  }
}

run "no_gpu_resolves_inert_accelerator_config" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    network_self_link   = "projects/test/global/networks/default"
    subnet_self_link    = "projects/test/regions/us-central1/subnetworks/default"
    pods_range_name     = "pods"
    services_range_name = "svcs"
    regional            = false
    node_zones          = ["us-central1-a"]
    machine_type        = "e2-standard-4"
  }

  assert {
    condition     = output.gpu_node_pool.enabled == false
    error_message = "A non-GPU node pool must report enabled=false."
  }

  assert {
    condition     = output.gpu_node_pool.count == 0
    error_message = "A non-GPU node pool must resolve accelerator_count=0 so the module emits no guest_accelerator."
  }

  assert {
    condition     = output.gpu_node_pool.driver_version == ""
    error_message = "A non-GPU node pool must resolve an empty driver_version so the module emits no gpu_driver_installation_config."
  }
}

# -----------------------------------------------------------------------------
# Negative cases: the machine-type precondition (hosted on random_id.suffix)
# rejects GPU on incompatible machine types at plan time. expect_failures
# matches by resource address.
#
# Maintainer note: random_id.suffix currently carries exactly one precondition
# (the GPU machine-type rule). If a second precondition is added to that
# resource, split these negative exercises so the failures don't collide.
# -----------------------------------------------------------------------------

run "gpu_on_non_n1_machine_fails_precondition" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    network_self_link   = "projects/test/global/networks/default"
    subnet_self_link    = "projects/test/regions/us-central1/subnetworks/default"
    pods_range_name     = "pods"
    services_range_name = "svcs"
    regional            = false
    node_zones          = ["us-central1-a"]
    machine_type        = "n2-standard-4" # not N1 — cannot attach a GPU
    gpu_type            = "nvidia-tesla-t4"
  }

  expect_failures = [
    random_id.suffix,
  ]
}

run "gpu_on_bundled_a3_machine_fails_precondition" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    network_self_link   = "projects/test/global/networks/default"
    subnet_self_link    = "projects/test/regions/us-central1/subnetworks/default"
    pods_range_name     = "pods"
    services_range_name = "svcs"
    regional            = false
    node_zones          = ["us-central1-a"]
    machine_type        = "a3-highgpu-8g" # A3 bundles its H100 — accelerator config invalid
    gpu_type            = "nvidia-h100-80gb"
  }

  expect_failures = [
    random_id.suffix,
  ]
}
