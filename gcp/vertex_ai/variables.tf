variable "project" { type = string }
variable "region" { type = string }

variable "labels" {
  description = "Labels to apply"
  type        = map(string)
  default     = {}
}
