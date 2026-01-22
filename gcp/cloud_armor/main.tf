resource "google_compute_security_policy" "policy" {
  name = "main-policy"
  rule {
    action   = "allow"
    priority = "2147483647"
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    description = "Default rule"
  }
}

output "security_policy_id" {
  value = google_compute_security_policy.policy.self_link
}
