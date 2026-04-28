# GCP Vertex AI Dataset
# https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/vertex_ai_dataset

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

locals {
  metadata_schemas = {
    image       = "gs://google-cloud-aiplatform/schema/dataset/metadata/image_1.0.0.yaml"
    text        = "gs://google-cloud-aiplatform/schema/dataset/metadata/text_1.0.0.yaml"
    tabular     = "gs://google-cloud-aiplatform/schema/dataset/metadata/tabular_1.0.0.yaml"
    video       = "gs://google-cloud-aiplatform/schema/dataset/metadata/video_1.0.0.yaml"
    time_series = "gs://google-cloud-aiplatform/schema/dataset/metadata/time_series_1.0.0.yaml"
  }

  resolved_schema_uri = coalesce(var.metadata_schema_uri, local.metadata_schemas[var.dataset_type])
}

resource "google_vertex_ai_dataset" "dataset" {
  display_name        = var.dataset_name
  metadata_schema_uri = local.resolved_schema_uri
  region              = var.region
  project             = var.project_id

  dynamic "encryption_spec" {
    for_each = var.encryption_kms_key_name == null ? [] : [var.encryption_kms_key_name]
    content {
      kms_key_name = encryption_spec.value
    }
  }

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}
