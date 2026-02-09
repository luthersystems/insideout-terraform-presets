resource "google_vertex_ai_dataset" "dataset" {
  display_name        = "main-dataset"
  metadata_schema_uri = "gs://google-cloud-aiplatform/schema/dataset/metadata/image_1.0.0.yaml"
  region              = var.region
}
