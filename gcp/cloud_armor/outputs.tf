output "security_policy_id" {
  value       = google_compute_security_policy.policy.self_link
  description = "DEPRECATED: use self_link. Kept for backward compatibility with existing composer wiring."
}

output "policy_id" {
  value       = google_compute_security_policy.policy.id
  description = "The resource ID of the Cloud Armor security policy"
}

output "self_link" {
  value       = google_compute_security_policy.policy.self_link
  description = "The self link of the Cloud Armor security policy (used to attach to backend services)"
}

output "name" {
  value       = google_compute_security_policy.policy.name
  description = "The fully-qualified name of the Cloud Armor security policy"
}
