variable "project" {
  description = "GCP project ID"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "name" {
  description = "Name prefix for load balancer resources"
  type        = string
  default     = "main"
}

variable "network_self_link" {
  description = "VPC network self link"
  type        = string
}

variable "subnet_self_link" {
  description = "Subnet self link"
  type        = string
}

variable "enable_ssl" {
  description = "Enable HTTPS with SSL certificate"
  type        = bool
  default     = true
}

variable "ssl_certificates" {
  description = "List of SSL certificate self links (for existing certs)"
  type        = list(string)
  default     = []
}

variable "managed_ssl_domains" {
  description = "Domains for Google-managed SSL certificates"
  type        = list(string)
  default     = []
}

variable "enable_cdn" {
  description = "Enable Cloud CDN"
  type        = bool
  default     = false
}

variable "cdn_cache_mode" {
  description = "CDN cache mode (CACHE_ALL_STATIC, USE_ORIGIN_HEADERS, FORCE_CACHE_ALL)"
  type        = string
  default     = "CACHE_ALL_STATIC"
}

variable "backends" {
  description = "Backend configurations"
  type = list(object({
    name                   = string
    description            = optional(string)
    protocol               = optional(string, "HTTP")
    port                   = optional(number, 80)
    port_name              = optional(string, "http")
    timeout_sec            = optional(number, 30)
    enable_cdn             = optional(bool, false)
    health_check_path      = optional(string, "/")
    instance_group         = optional(string)
    network_endpoint_group = optional(string)
  }))
  default = []
}

variable "url_map_hosts" {
  description = "Host rules for URL map"
  type = list(object({
    hosts        = list(string)
    path_matcher = string
  }))
  default = []
}

variable "path_matchers" {
  description = "Path matchers for URL map"
  type = list(object({
    name            = string
    default_service = string
    path_rules = optional(list(object({
      paths   = list(string)
      service = string
    })), [])
  }))
  default = []
}

variable "default_backend" {
  description = "Default backend service name"
  type        = string
  default     = ""
}

variable "enable_iap" {
  description = "Enable Identity-Aware Proxy"
  type        = bool
  default     = false
}

variable "iap_oauth2_client_id" {
  description = "OAuth2 client ID for IAP"
  type        = string
  default     = ""
}

variable "iap_oauth2_client_secret" {
  description = "OAuth2 client secret for IAP"
  type        = string
  default     = ""
  sensitive   = true
}

variable "security_policy" {
  description = "Cloud Armor security policy self link"
  type        = string
  default     = null
}

variable "labels" {
  description = "Labels to apply"
  type        = map(string)
  default     = {}
}

