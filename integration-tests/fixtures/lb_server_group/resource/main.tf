# This fixture owns one server group under an existing load balancer.
# The test suite owns the server group and removes it during teardown.
# The load balancer remains an external prerequisite.

terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "load_balancer_id" { type = string }
variable "name" { type = string }
variable "protocol" {
  type    = string
  default = "HTTP"
}
variable "algorithm" {
  type    = string
  default = "ROUND_ROBIN"
}
variable "health_check" {
  type = object({
    monitor_type     = string
    delay            = number
    timeout          = number
    max_retries      = number
    max_retries_down = number
    path             = optional(string)
    http_method      = optional(string)
    expected_codes   = optional(string)
  })
  default = null
}

variable "description" {
  type    = string
  default = null
}
variable "enable_h2" {
  type    = bool
  default = null
}
variable "session_persistence" {
  type = object({
    type        = string
    cookie_name = optional(string)
  })
  default = null
}

resource "viettelcloud_lb_server_group" "subject" {
  load_balancer       = { id = var.load_balancer_id }
  name                = var.name
  protocol            = var.protocol
  algorithm           = var.algorithm
  description         = var.description
  enable_h2           = var.enable_h2
  session_persistence = var.session_persistence
  health_check        = var.health_check
}

output "subject" { value = viettelcloud_lb_server_group.subject }
