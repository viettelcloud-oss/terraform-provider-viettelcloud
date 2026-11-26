# This module owns its lookup subject so each suite can run independently.

terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "load_balancer_id" {
  type = string
}

variable "name" {
  type = string
}

variable "protocol" {
  type = string
}

variable "protocol_port" {
  type = number
}

variable "description" {
  type    = string
  default = null
}

variable "server_group_id" {
  type    = string
  default = null
}

variable "default_tls_cert_id" {
  type    = string
  default = null
}

variable "sni_tls_cert_ids" {
  type    = set(string)
  default = null
}

variable "allowed_cidrs" {
  type    = set(string)
  default = null
}

variable "listener_timeout" {
  type    = number
  default = null
}

variable "server_timeout" {
  type    = number
  default = null
}

variable "server_connecting_timeout" {
  type    = number
  default = null
}

variable "enable_h2" {
  type    = bool
  default = null
}

variable "set_x_forwarded_for" {
  type    = bool
  default = null
}

variable "set_x_forwarded_port" {
  type    = bool
  default = null
}

variable "set_x_forwarded_proto" {
  type    = bool
  default = null
}

resource "viettelcloud_lb_listener" "listener" {
  load_balancer             = { id = var.load_balancer_id }
  name                      = var.name
  protocol                  = var.protocol
  protocol_port             = var.protocol_port
  description               = var.description
  server_group              = var.server_group_id == null ? null : { id = var.server_group_id }
  default_tls_cert          = var.default_tls_cert_id == null ? null : { id = var.default_tls_cert_id }
  sni_tls_certs             = var.sni_tls_cert_ids == null ? null : [for id in var.sni_tls_cert_ids : { id = id }]
  allowed_cidrs             = var.allowed_cidrs
  listener_timeout          = var.listener_timeout
  server_timeout            = var.server_timeout
  server_connecting_timeout = var.server_connecting_timeout
  enable_h2                 = var.enable_h2
  set_x_forwarded_for       = var.set_x_forwarded_for
  set_x_forwarded_port      = var.set_x_forwarded_port
  set_x_forwarded_proto     = var.set_x_forwarded_proto
}

data "viettelcloud_lb_listener" "by_id" {
  load_balancer_id = var.load_balancer_id
  id               = viettelcloud_lb_listener.listener.id
}

data "viettelcloud_lb_listener" "by_name" {
  load_balancer_id = var.load_balancer_id
  name             = "  ${viettelcloud_lb_listener.listener.name}  "
}

data "viettelcloud_lb_listener" "by_both" {
  load_balancer_id = var.load_balancer_id
  id               = viettelcloud_lb_listener.listener.id
  name             = viettelcloud_lb_listener.listener.name
}

output "listener" {
  value = viettelcloud_lb_listener.listener
}
output "by_id" {
  value = data.viettelcloud_lb_listener.by_id
}

output "by_name" {
  value = data.viettelcloud_lb_listener.by_name
}

output "by_both" {
  value = data.viettelcloud_lb_listener.by_both
}
