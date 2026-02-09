module "gcp_vpc" {
  source       = "../../gcp/vpc"
  network_name = "vpc"
  region       = var.gcp_vpc_region
  project      = var.gcp_vpc_project
}

module "gcp_cloud_run" {
  source        = "../../gcp/cloud_run"
  vpc_connector = module.gcp_vpc.connector_id
  cpu           = var.gcp_cloud_run_cpu
  max_instances = var.gcp_cloud_run_max_instances
  memory        = var.gcp_cloud_run_memory
  min_instances = var.gcp_cloud_run_min_instances
  project       = var.gcp_cloud_run_project
  region        = var.gcp_cloud_run_region
}

module "gcp_cloudsql" {
  source            = "../../gcp/cloudsql"
  network_self_link = module.gcp_vpc.network_self_link
  project           = var.gcp_cloudsql_project
  region            = var.gcp_cloudsql_region
  tier              = var.gcp_cloudsql_tier
  availability_type = var.gcp_cloudsql_availability_type
}

module "gcp_gcs" {
  source             = "../../gcp/gcs"
  bucket_name        = var.gcp_gcs_bucket_name
  project            = var.gcp_gcs_project
  region             = var.gcp_gcs_region
  storage_class      = var.gcp_gcs_storage_class
  versioning_enabled = var.gcp_gcs_versioning_enabled
}

module "gcp_cloud_logging" {
  source  = "../../gcp/cloud_logging"
  project = var.gcp_cloud_logging_project
  region  = var.gcp_cloud_logging_region
}
