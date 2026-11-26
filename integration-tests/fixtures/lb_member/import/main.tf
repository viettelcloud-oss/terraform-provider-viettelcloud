# This module plans an import while the creating test state owns the member.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "id" { type = string }
variable "server_group_id" { type = string }
variable "name" { type = string }
variable "address" { type = string }
variable "protocol_port" { type = number }

resource "viettelcloud_lb_member" "imported" {
  server_group  = { id = var.server_group_id }
  name          = var.name
  address       = var.address
  protocol_port = var.protocol_port
}

import {
  to = viettelcloud_lb_member.imported
  id = "${var.server_group_id}/${var.id}"
}

output "member" { value = viettelcloud_lb_member.imported }
