variable "project" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "cache_mode" {
  description = "Cache mode: CACHE_ALL_STATIC, USE_ORIGIN_HEADERS, or FORCE_CACHE_ALL"
  type        = string
  default     = "CACHE_ALL_STATIC"
}

variable "default_ttl" {
  description = "Default TTL in seconds for cached content"
  type        = number
  default     = 3600
}

variable "max_ttl" {
  description = "Maximum TTL in seconds"
  type        = number
  default     = 86400
}

variable "client_ttl" {
  description = "Client TTL in seconds (Cache-Control max-age)"
  type        = number
  default     = 3600
}

variable "negative_caching" {
  description = "Enable caching of negative responses (404, 410, etc.)"
  type        = bool
  default     = false
}

variable "serve_while_stale" {
  description = "Serve stale content while revalidating in the background"
  type        = number
  default     = 86400
}
