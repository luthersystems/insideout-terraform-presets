# Seed stack provider for the Stage 2d (#264) GCP discover smoke.
#
# Hits real GCP via Application Default Credentials. There is no GCP
# equivalent of LocalStack for the Cloud Asset Inventory API, so the
# operator must point this at a throwaway sandbox project.
#
# Used only by tests/gcp-discover-smoke.sh.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
