# GCP Compute Engine Instance Module
# https://github.com/terraform-google-modules/terraform-google-vm

# Per-deploy suffix so retries after state loss don't 409 on existing
# compute instance names (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  instance_name = "${var.project}-${var.instance_name}-${random_id.suffix.hex}"

  # GPU attachment (#767). A non-empty gpu_type requests an attached GPU.
  _gpu_enabled = trimspace(var.gpu_type) != ""

  # Machine family = the part before the first "-" (n1-standard-4 -> n1). Mirrors
  # the composer machineFamily() derive; kept in lockstep by TestGCPGPUFamiliesDrift.
  _machine_family = lower(split("-", trimspace(var.machine_type))[0])

  # GPUs attach via guest_accelerator ONLY on N1 machines. A2/A3/A4/G2/G4
  # accelerator-optimized machines bundle their GPU with the machine type, so
  # attaching a separate accelerator there is invalid and GCP rejects it.
  _gpu_bundled_machine_families = ["a2", "a3", "a4", "a4x", "g2", "g4"]
  _is_n1_machine                = local._machine_family == "n1"
  _is_bundled_gpu_machine       = contains(local._gpu_bundled_machine_families, local._machine_family)

  # GCP rejects live migration for any GPU-bearing instance, so a GPU instance
  # MUST terminate on host maintenance. Force it by construction (validate-don't-
  # mask): the preset never lets MIGRATE coexist with a GPU.
  _on_host_maintenance = local._gpu_enabled ? "TERMINATE" : (var.preemptible ? "TERMINATE" : "MIGRATE")
}

data "google_compute_image" "this" {
  family  = var.image_family
  project = var.image_project
}

resource "google_compute_instance" "this" {
  name         = local.instance_name
  project      = var.project_id
  zone         = var.zone
  machine_type = var.machine_type

  boot_disk {
    initialize_params {
      image = data.google_compute_image.this.self_link
      size  = var.disk_size_gb
      type  = var.disk_type
    }
  }

  network_interface {
    network    = var.network_self_link
    subnetwork = var.subnet_self_link

    dynamic "access_config" {
      for_each = var.enable_public_ip ? [1] : []
      content {
        # Ephemeral public IP
      }
    }
  }

  scheduling {
    preemptible         = var.preemptible
    automatic_restart   = !var.preemptible
    on_host_maintenance = local._on_host_maintenance
  }

  # GPU attachment (#767). Only emitted when gpu_type is set. on_host_maintenance
  # is forced to TERMINATE above when this block is present.
  dynamic "guest_accelerator" {
    for_each = local._gpu_enabled ? [1] : []
    content {
      type  = var.gpu_type
      count = var.gpu_count > 0 ? var.gpu_count : 1
    }
  }

  service_account {
    email  = var.service_account_email != "" ? var.service_account_email : null
    scopes = var.service_account_scopes
  }

  tags = var.tags

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )

  metadata = merge(
    var.metadata,
    var.startup_script != "" ? {
      startup-script = var.startup_script
    } : {}
  )

  # Enable OS Login for secure SSH access
  metadata_startup_script = null

  shielded_instance_config {
    enable_secure_boot          = true
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }

  allow_stopping_for_update = true

  lifecycle {
    # GPU machine-type compatibility (#767). VALIDATE, don't mask. A GPU attaches
    # via guest_accelerator only on N1 machines; A2/A3/A4/G2/G4 bundle their GPU
    # with the machine type (so an explicit accelerator is invalid) and every
    # other family takes none. Cross-variable rule lives here as a resource
    # precondition (Terraform forbids multi-variable conditions in variable
    # validation blocks).
    precondition {
      condition     = !local._gpu_enabled || (local._is_n1_machine && !local._is_bundled_gpu_machine)
      error_message = "gpu_type is set but machine_type=${var.machine_type} cannot attach a GPU: GCP attaches GPUs via guest_accelerator only on N1 machines (e.g. n1-standard-4). A2/A3/A4/G2/G4 accelerator-optimized machines bundle their GPU with the machine type — use that machine type alone with no gpu_type."
    }
  }
}

