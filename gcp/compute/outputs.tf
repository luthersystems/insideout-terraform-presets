output "instance_id" {
  description = "The instance ID"
  value       = google_compute_instance.this.instance_id
}

output "instance_name" {
  description = "The instance name"
  value       = google_compute_instance.this.name
}

output "instance_self_link" {
  description = "The instance self link"
  value       = google_compute_instance.this.self_link
}

output "internal_ip" {
  description = "The internal IP address"
  value       = google_compute_instance.this.network_interface[0].network_ip
}

output "external_ip" {
  description = "The external IP address (if enabled)"
  value       = var.enable_public_ip ? google_compute_instance.this.network_interface[0].access_config[0].nat_ip : null
}

output "zone" {
  description = "The zone where the instance is located"
  value       = google_compute_instance.this.zone
}

