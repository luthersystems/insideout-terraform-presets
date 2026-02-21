output "security_policy_id" {
  value       = google_compute_security_policy.policy.self_link
  description = "The self link of the Cloud Armor security policy"
}
