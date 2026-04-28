# GCP Bastion Host Module
# Secure bastion using IAP tunneling and OS Login.

terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

locals {
  instance_name = "${var.project}-bastion"
}

# Dedicated service account for the bastion
resource "google_service_account" "bastion" {
  account_id   = "${var.project}-bastion"
  display_name = "${var.project} Bastion Host"
  project      = var.project_id
}

# Allow IAP tunneling to the bastion
resource "google_project_iam_member" "iap_tunnel" {
  project = var.project_id
  role    = "roles/iap.tunnelResourceAccessor"
  member  = "serviceAccount:${google_service_account.bastion.email}"
}

data "google_compute_image" "this" {
  family  = var.image_family
  project = var.image_project
}

resource "google_compute_instance" "bastion" {
  name         = local.instance_name
  project      = var.project_id
  zone         = var.zone
  machine_type = var.machine_type

  boot_disk {
    initialize_params {
      image = data.google_compute_image.this.self_link
      size  = var.disk_size_gb
      type  = "pd-ssd"
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

  metadata = {
    enable-oslogin = "TRUE"
  }

  service_account {
    email  = google_service_account.bastion.email
    scopes = ["cloud-platform"]
  }

  shielded_instance_config {
    enable_secure_boot          = true
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }

  tags = ["bastion", "ssh"]

  labels = merge(
    {
      project = var.project
      role    = "bastion"
    },
    var.labels,
  )

  allow_stopping_for_update = true
}

# Firewall rule: allow SSH from IAP range only
resource "google_compute_firewall" "bastion_iap_ssh" {
  name    = "${local.instance_name}-allow-iap-ssh"
  project = var.project_id
  network = var.network_self_link

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  # IAP's IP range for SSH tunneling
  source_ranges = ["35.235.240.0/20"]
  target_tags   = ["bastion"]
}
