# This fixture imports a group through a plan-only run.
# Its creating run retains ownership and removes the group during teardown.
# Applying this module gives the group two owners and risks duplicate deletion.

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

variable "server_group_id" { type = string }

import {
  to = viettelcloud_lb_server_group.imported
  id = "${var.load_balancer_id}/${var.server_group_id}"
}

# Configure non-computed inputs from the creating run to avoid planned changes.
# Leave optional computed inputs absent to test their values after import refresh.
resource "viettelcloud_lb_server_group" "imported" {
  load_balancer = { id = var.load_balancer_id }
  name          = var.name
  protocol      = var.protocol
  algorithm     = var.algorithm
  health_check  = var.health_check
}

output "subject" { value = viettelcloud_lb_server_group.imported }
