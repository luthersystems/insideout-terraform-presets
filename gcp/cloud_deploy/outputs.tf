output "pipeline_name" {
  value       = google_clouddeploy_delivery_pipeline.this.name
  description = "Cloud Deploy delivery pipeline short name (var.project-prefixed). Pass to `gcloud deploy releases create --delivery-pipeline=<this>`."
}

output "pipeline_id" {
  value       = google_clouddeploy_delivery_pipeline.this.id
  description = "Fully-qualified delivery pipeline resource ID (projects/<id>/locations/<region>/deliveryPipelines/<name>)."
}

output "service_account_email" {
  value       = google_service_account.deploy_runner.email
  description = "Email of the runner SA Cloud Deploy executes render / deploy / verify jobs as. Grant additional roles here for downstream resources the pipeline's targets must touch (e.g. roles/run.developer for Cloud Run deploys, roles/container.developer for GKE deploys)."
}

output "target_names" {
  value       = [for t in var.targets : "${var.project}-${t.name}"]
  description = "Ordered list of var.project-prefixed Cloud Deploy target names the pipeline promotes through. Same order as var.targets — element [0] is the first promotion stage. Each element matches the corresponding google_clouddeploy_target.name (= local.target_full_names entry)."
}

output "target_short_names" {
  value       = [for t in var.targets : t.name]
  description = "Ordered list of caller-supplied short names (unprefixed). Useful when the consumer needs to reference targets by the same identifier the caller used in var.targets."
}

output "target_ids" {
  value       = { for k, v in google_clouddeploy_target.this : k => v.id }
  description = "Map from target short name to fully-qualified target resource ID. Keys are the caller-supplied short names (unprefixed); values are the projects/<id>/locations/<region>/targets/<full-name> resource IDs. Useful for downstream wiring that needs the Cloud Deploy target reference (e.g. cross-stack release-promotion automation)."
}
