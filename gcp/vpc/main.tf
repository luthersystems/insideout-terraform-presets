# GCP VPC Module using terraform-google-network
# https://github.com/terraform-google-modules/terraform-google-network

locals {
  network_name = "${var.project}-${var.network_name}"
  subnet_name  = "${var.project}-subnet-${var.region}"
}

module "vpc" {
  source  = "terraform-google-modules/network/google"
  version = "~> 9.0"

  project_id   = var.project
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
        range_name    = "${var.project}-pods"
        ip_cidr_range = var.secondary_ranges.pods_cidr
      },
      {
        range_name    = "${var.project}-services"
        ip_cidr_range = var.secondary_ranges.services_cidr
      }
    ]
  } : {}
}

# Cloud Router for NAT
resource "google_compute_router" "router" {
  count   = var.enable_cloud_nat ? 1 : 0
  name    = "${var.project}-router"
  project = var.project
  region  = var.region
  network = module.vpc.network_id
}

# Cloud NAT for private instances
module "cloud_nat" {
  count   = var.enable_cloud_nat ? 1 : 0
  source  = "terraform-google-modules/cloud-nat/google"
  version = "~> 5.0"

  project_id    = var.project
  region        = var.region
  router        = google_compute_router.router[0].name
  name          = "${var.project}-nat"
  network       = module.vpc.network_id
  create_router = false
}

# Basic firewall rules
resource "google_compute_firewall" "allow_internal" {
  name    = "${local.network_name}-allow-internal"
  project = var.project
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
  project = var.project
  network = module.vpc.network_name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  # IAP's IP range for SSH tunneling
  source_ranges = ["35.235.240.0/20"]
}

