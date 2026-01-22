variable "project" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "allow_duplicate_emails" {
  description = "Allow multiple accounts with the same email"
  type        = bool
  default     = false
}

variable "enable_email_signin" {
  description = "Enable email/password sign-in"
  type        = bool
  default     = true
}

variable "password_required" {
  description = "Require password for email sign-in"
  type        = bool
  default     = true
}

variable "enable_phone_signin" {
  description = "Enable phone number sign-in"
  type        = bool
  default     = false
}

variable "enable_anonymous_signin" {
  description = "Enable anonymous sign-in"
  type        = bool
  default     = false
}

variable "mfa_enabled" {
  description = "Enable multi-factor authentication"
  type        = bool
  default     = false
}

variable "mfa_state" {
  description = "MFA state: ENABLED, DISABLED, or MANDATORY"
  type        = string
  default     = "ENABLED"
}

variable "mfa_enabled_providers" {
  description = "List of MFA providers to enable"
  type        = list(string)
  default     = ["PHONE_SMS"]
}

variable "enable_google_signin" {
  description = "Enable Google OAuth sign-in"
  type        = bool
  default     = false
}

variable "google_client_id" {
  description = "Google OAuth client ID"
  type        = string
  default     = ""
}

variable "google_client_secret" {
  description = "Google OAuth client secret"
  type        = string
  default     = ""
  sensitive   = true
}
