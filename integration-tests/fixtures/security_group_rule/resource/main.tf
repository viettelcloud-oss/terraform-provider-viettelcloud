terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "security_group_id" {
  type = string
}

variable "direction" {
  type = string
}

variable "protocol" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

variable "port_range_min" {
  type    = number
  default = null
}

variable "port_range_max" {
  type    = number
  default = null
}

variable "remote_ip_prefix" {
  type    = string
  default = null
}

resource "viettelcloud_security_group_rule" "rule" {
  security_group   = { id = var.security_group_id }
  direction        = var.direction
  protocol         = var.protocol
  description      = var.description
  port_range_min   = var.port_range_min
  port_range_max   = var.port_range_max
  remote_ip_prefix = var.remote_ip_prefix
}

output "rule_id" {
  value = viettelcloud_security_group_rule.rule.id
}

output "rule_security_group_id" {
  value = viettelcloud_security_group_rule.rule.security_group.id
}

output "rule_security_group_name" {
  value = viettelcloud_security_group_rule.rule.security_group_name
}

output "rule_direction" {
  value = viettelcloud_security_group_rule.rule.direction
}

output "rule_protocol" {
  value = viettelcloud_security_group_rule.rule.protocol
}

output "rule_ethertype" {
  value = viettelcloud_security_group_rule.rule.ethertype
}

output "rule_description" {
  value = viettelcloud_security_group_rule.rule.description
}

output "rule_port_range_min" {
  value = viettelcloud_security_group_rule.rule.port_range_min
}

output "rule_port_range_max" {
  value = viettelcloud_security_group_rule.rule.port_range_max
}

output "rule_remote_ip_prefix" {
  value = viettelcloud_security_group_rule.rule.remote_ip_prefix
}

output "rule_region" {
  value = viettelcloud_security_group_rule.rule.region
}

output "rule_created_at" {
  value = viettelcloud_security_group_rule.rule.created_at
}
