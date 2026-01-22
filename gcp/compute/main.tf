# GCP Compute Engine Instance Module
# https://github.com/terraform-google-modules/terraform-google-vm

locals {
  instance_name = "${var.project}-${var.instance_name}"
}

data "google_compute_image" "this" {
  family  = var.image_family
  project = var.image_project
}

resource "google_compute_instance" "this" {
  name         = local.instance_name
  project      = var.project
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
    on_host_maintenance = var.preemptible ? "TERMINATE" : "MIGRATE"
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
}

