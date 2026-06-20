output "template_id" {
  description = "The template ID of the Model Armor template."
  value       = google_model_armor_template.this.template_id
}

output "template_name" {
  description = "The full generated resource name of the Model Armor template (projects/<project>/locations/<location>/templates/<id>)."
  value       = google_model_armor_template.this.name
}

output "location" {
  description = "The location (region) the Model Armor template runs in."
  value       = google_model_armor_template.this.location
}

output "floorsetting_name" {
  description = "The full resource name of the project floor setting, or null when manage_floorsetting is false."
  value       = var.manage_floorsetting ? google_model_armor_floorsetting.this[0].name : null
}
