variable "region" {
  type = string
}

variable "project" {
  type = string
}

variable "num_secrets" {
  type    = number
  default = 1
}

variable "tags" {
  type    = map(string)
  default = {}
}
