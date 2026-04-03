output "instance_id" {
  description = "The bastion instance ID"
  value       = google_compute_instance.bastion.instance_id
}

output "instance_name" {
  description = "The bastion instance name"
  value       = google_compute_instance.bastion.name
}

output "internal_ip" {
  description = "The internal IP address"
  value       = google_compute_instance.bastion.network_interface[0].network_ip
}

output "external_ip" {
  description = "The external IP address (null if public IP is disabled)"
  value       = var.enable_public_ip ? google_compute_instance.bastion.network_interface[0].access_config[0].nat_ip : null
}

output "service_account_email" {
  description = "The bastion service account email"
  value       = google_service_account.bastion.email
}

output "zone" {
  description = "The zone where the bastion is located"
  value       = google_compute_instance.bastion.zone
}
