# Cloud SQL Module using terraform-google-sql-db
# https://github.com/terraform-google-modules/terraform-google-sql-db

locals {
  instance_name = "${var.project}-${var.instance_name}"
  is_postgres   = startswith(var.database_version, "POSTGRES")
}

# Generate random password if not provided
resource "random_password" "user_password" {
  count   = var.user_password == "" ? 1 : 0
  length  = 24
  special = true
}

# Private service connection for private IP
resource "google_compute_global_address" "private_ip_address" {
  count         = var.enable_private_ip && var.network_self_link != null ? 1 : 0
  name          = "${local.instance_name}-private-ip"
  project       = var.project
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = var.network_self_link
}

resource "google_service_networking_connection" "private_vpc_connection" {
  count                   = var.enable_private_ip && var.network_self_link != null ? 1 : 0
  network                 = var.network_self_link
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_address[0].name]
}

# Cloud SQL instance
module "sql_db" {
  source  = "GoogleCloudPlatform/sql-db/google//modules/postgresql"
  version = "~> 21.0"

  count = local.is_postgres ? 1 : 0

  project_id       = var.project
  name             = local.instance_name
  database_version = var.database_version
  region           = var.region
  zone             = "${var.region}-a"

  tier                  = var.tier
  disk_size             = var.disk_size_gb
  disk_type             = var.disk_type
  disk_autoresize       = var.disk_autoresize
  disk_autoresize_limit = var.disk_autoresize_limit

  availability_type = var.availability_type

  # Networking
  ip_configuration = {
    ipv4_enabled        = var.enable_public_ip
    private_network     = var.enable_private_ip ? var.network_self_link : null
    require_ssl         = true
    allocated_ip_range  = null
    authorized_networks = var.authorized_networks
  }

  # Database and user
  db_name  = var.database_name
  db_charset   = "UTF8"
  db_collation = "en_US.UTF8"

  user_name     = var.user_name
  user_password = var.user_password != "" ? var.user_password : random_password.user_password[0].result

  # Backups
  backup_configuration = {
    enabled                        = var.backup_enabled
    start_time                     = var.backup_start_time
    location                       = var.backup_location
    point_in_time_recovery_enabled = var.point_in_time_recovery_enabled
    transaction_log_retention_days = 7
    retained_backups               = 7
    retention_unit                 = "COUNT"
  }

  # Maintenance
  maintenance_window_day  = var.maintenance_window_day
  maintenance_window_hour = var.maintenance_window_hour

  deletion_protection = var.deletion_protection

  database_flags = var.database_flags

  user_labels = merge(
    {
      project = var.project
    },
    var.labels
  )

  depends_on = [google_service_networking_connection.private_vpc_connection]
}

# MySQL version (if needed)
module "sql_db_mysql" {
  source  = "GoogleCloudPlatform/sql-db/google//modules/mysql"
  version = "~> 21.0"

  count = !local.is_postgres ? 1 : 0

  project_id       = var.project
  name             = local.instance_name
  database_version = var.database_version
  region           = var.region
  zone             = "${var.region}-a"

  tier                  = var.tier
  disk_size             = var.disk_size_gb
  disk_type             = var.disk_type
  disk_autoresize       = var.disk_autoresize
  disk_autoresize_limit = var.disk_autoresize_limit

  availability_type = var.availability_type

  ip_configuration = {
    ipv4_enabled        = var.enable_public_ip
    private_network     = var.enable_private_ip ? var.network_self_link : null
    require_ssl         = true
    allocated_ip_range  = null
    authorized_networks = var.authorized_networks
  }

  db_name     = var.database_name
  db_charset  = "utf8mb4"
  db_collation = "utf8mb4_general_ci"

  user_name     = var.user_name
  user_password = var.user_password != "" ? var.user_password : random_password.user_password[0].result

  backup_configuration = {
    enabled                        = var.backup_enabled
    start_time                     = var.backup_start_time
    location                       = var.backup_location
    binary_log_enabled             = var.point_in_time_recovery_enabled
    transaction_log_retention_days = 7
    retained_backups               = 7
    retention_unit                 = "COUNT"
  }

  maintenance_window_day  = var.maintenance_window_day
  maintenance_window_hour = var.maintenance_window_hour

  deletion_protection = var.deletion_protection

  database_flags = var.database_flags

  user_labels = merge(
    {
      project = var.project
    },
    var.labels
  )

  depends_on = [google_service_networking_connection.private_vpc_connection]
}

