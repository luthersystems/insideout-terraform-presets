mock_provider "google" {}
mock_provider "kubernetes" {}
mock_provider "random" {}

# gcp/gke GPU node-pool shape tests (#767, #752 review). Verifies that:
#   - A GPU on an N1 machine resolves the node-pool accelerator config (type +
#     count + auto-driver version) that the vendored gke module wires into
#     guest_accelerator { ... gpu_driver_installation_config { ... } }.
#   - A GPU on an accelerator-optimized family (G2->nvidia-l4, A3->nvidia-h100)
#     ALSO resolves the accelerator config: unlike a Compute VM, a GKE node pool
#     DECLARES the accelerator paired with the machine type. This is the #752
#     review fix — the bundled families are valid on GKE, not rejected.
#   - A non-GPU node pool resolves an inert accelerator config (count 0, no
#     driver) so the module emits no guest_accelerator block.
#   - The machine-family precondition (hosted on random_id.suffix because the node
#     pool lives inside the module) rejects a GPU on a family that takes none
#     (e.g. N2), and the pairing precondition rejects a bundled family paired with
#     the wrong GPU type (expect_failures on random_id.suffix).
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

run "gpu_nodepool_on_g2_resolves_bundled_l4_accelerator" {
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
    machine_type        = "g2-standard-4" # G2 pairs with nvidia-l4 — VALID on GKE
    gpu_type            = "nvidia-l4"
    gpu_count           = 1
  }

  assert {
    condition     = output.gpu_node_pool.enabled == true
    error_message = "A G2 GPU node pool must report enabled=true — GKE declares the bundled accelerator."
  }

  assert {
    condition     = output.gpu_node_pool.type == "nvidia-l4"
    error_message = "G2 node-pool accelerator type must be nvidia-l4."
  }

  assert {
    condition     = output.gpu_node_pool.count == 1
    error_message = "G2 node-pool accelerator count must be the configured gpu_count."
  }
}

run "gpu_nodepool_on_a3_resolves_bundled_h100_accelerator" {
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
    machine_type        = "a3-highgpu-8g" # A3 pairs with nvidia-h100-80gb — VALID on GKE
    gpu_type            = "nvidia-h100-80gb"
    gpu_count           = 8
  }

  assert {
    condition     = output.gpu_node_pool.enabled == true
    error_message = "An A3 GPU node pool must report enabled=true — GKE declares the bundled accelerator."
  }

  assert {
    condition     = output.gpu_node_pool.type == "nvidia-h100-80gb"
    error_message = "A3 node-pool accelerator type must be nvidia-h100-80gb."
  }

  assert {
    condition     = output.gpu_node_pool.count == 8
    error_message = "A3 node-pool accelerator count must be the configured gpu_count."
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
# Negative cases: the GPU preconditions (hosted on random_id.suffix because the
# node pool lives inside the vendored module) reject GPU on a non-GPU machine
# family and a bundled family paired with the wrong GPU type at plan time.
# expect_failures matches by resource address.
#
# Maintainer note: random_id.suffix now carries TWO GPU preconditions — the
# machine-family rule and the bundled-family pairing rule. expect_failures
# matches by resource address, so each case below only needs to trip ONE of
# them; that is exactly what these exercise (the n2 case trips the family rule,
# the g2+wrong-type case trips the pairing rule). If a NON-GPU precondition is
# ever added to this resource, split these so an unrelated failure can't mask a
# regression in the GPU rules.
# -----------------------------------------------------------------------------

run "gpu_on_non_gpu_family_fails_precondition" {
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
    machine_type        = "n2-standard-4" # neither N1 nor accelerator-optimized — no GPU
    gpu_type            = "nvidia-tesla-t4"
  }

  expect_failures = [
    random_id.suffix,
  ]
}

run "gpu_on_bundled_g2_with_mismatched_type_fails_pairing" {
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
    machine_type        = "g2-standard-4"     # G2 pairs with nvidia-l4 only
    gpu_type            = "nvidia-tesla-a100" # A100 is an A2 GPU — wrong pairing
  }

  expect_failures = [
    random_id.suffix,
  ]
}
