# GCP VPC Module using terraform-google-network
# https://github.com/terraform-google-modules/terraform-google-network

# Per-deploy suffix so retries after state loss don't 409 on existing
# network/subnet/firewall/router/connector names (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  network_name = "${var.project}-${var.network_name}-${random_id.suffix.hex}"
  subnet_name  = "${var.project}-subnet-${var.region}-${random_id.suffix.hex}"
}

module "vpc" {
  source  = "terraform-google-modules/network/google"
  version = "~> 9.0"

  project_id   = var.project_id
  network_name = local.network_name
  routing_mode = "GLOBAL"

  subnets = [
    {
      subnet_name           = local.subnet_name
      subnet_ip             = var.subnet_cidr
      subnet_region         = var.region
      subnet_private_access = true
      subnet_flow_logs      = true
    }
  ]

  secondary_ranges = var.gke_cluster_name != null ? {
    (local.subnet_name) = [
      {
        range_name    = "${var.project}-pods-${random_id.suffix.hex}"
        ip_cidr_range = var.secondary_ranges.pods_cidr
      },
      {
        range_name    = "${var.project}-services-${random_id.suffix.hex}"
        ip_cidr_range = var.secondary_ranges.services_cidr
      }
    ]
  } : {}
}

# Cloud Router for NAT
resource "google_compute_router" "router" {
  count   = var.enable_cloud_nat ? 1 : 0
  name    = "${var.project}-router-${random_id.suffix.hex}"
  project = var.project_id
  region  = var.region
  network = module.vpc.network_id
}

# Cloud NAT for private instances
module "cloud_nat" {
  count   = var.enable_cloud_nat ? 1 : 0
  source  = "terraform-google-modules/cloud-nat/google"
  version = "~> 5.0"

  project_id    = var.project_id
  region        = var.region
  router        = google_compute_router.router[0].name
  name          = "${var.project}-nat-${random_id.suffix.hex}"
  network       = module.vpc.network_id
  create_router = false
}

# Basic firewall rules
resource "google_compute_firewall" "allow_internal" {
  name    = "${local.network_name}-allow-internal"
  project = var.project_id
  network = module.vpc.network_name

  allow {
    protocol = "icmp"
  }
  allow {
    protocol = "tcp"
    ports    = ["0-65535"]
  }
  allow {
    protocol = "udp"
    ports    = ["0-65535"]
  }

  source_ranges = [var.subnet_cidr]
}

resource "google_compute_firewall" "allow_ssh_iap" {
  name    = "${local.network_name}-allow-ssh-iap"
  project = var.project_id
  network = module.vpc.network_name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  # IAP's IP range for SSH tunneling
  source_ranges = ["35.235.240.0/20"]
}

# Serverless VPC Access Connector for Cloud Run / Cloud Functions
resource "google_vpc_access_connector" "serverless" {
  count = var.enable_serverless_connector ? 1 : 0

  # Connector names are limited to 25 chars total. Use a 4-char suffix slice
  # to keep within budget while still cycling on state-loss recovery.
  name          = "${var.project}-conn-${substr(random_id.suffix.hex, 0, 4)}"
  project       = var.project_id
  region        = var.region
  network       = module.vpc.network_self_link
  ip_cidr_range = var.connector_cidr

  lifecycle {
    # The composed connector name "<project>-conn-<4hex>" budgets exactly
    # 25 chars when var.project is 15 chars (the InsideOut session-prefix
    # default). Surface the constraint at plan time so a too-long
    # var.project fails fast with a clear message instead of erroring at
    # apply when the GCP API rejects the name.
    precondition {
      condition     = length(var.project) <= 15
      error_message = "var.project must be ≤ 15 chars when enable_serverless_connector = true. The composed connector name is \"<project>-conn-<4hex>\" (project + 10 chars), and the VPC connector API caps names at 25 chars. Either disable enable_serverless_connector or shorten var.project."
    }
  }
}

