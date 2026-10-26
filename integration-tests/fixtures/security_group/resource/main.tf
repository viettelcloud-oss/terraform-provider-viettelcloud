# Security group fixture for the lifecycle, omitted_description, and import
# chains of tests/security_group_resource.tftest.hcl. Every chain applies this
# module under its own state_key and owns the security group it creates.
#
# The outputs below feed the import chain, which configures only name and
# region and asserts the rest against the values this module observed.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "region" {
  type = string
}

variable "name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

resource "viettelcloud_security_group" "security_group" {
  name        = var.name
  description = var.description
  region      = { name = var.region }
}

output "security_group_id" {
  value = viettelcloud_security_group.security_group.id
}

output "security_group_name" {
  value = viettelcloud_security_group.security_group.name
}

output "security_group_description" {
  value = viettelcloud_security_group.security_group.description
}

output "security_group_display_name" {
  value = viettelcloud_security_group.security_group.display_name
}

output "security_group_is_default" {
  value = viettelcloud_security_group.security_group.is_default
}

output "security_group_region" {
  value = viettelcloud_security_group.security_group.region.name
}

output "security_group_region_id" {
  value = viettelcloud_security_group.security_group.region_id
}

output "security_group_project_id" {
  value = viettelcloud_security_group.security_group.project_id
}

output "security_group_created_at" {
  value = viettelcloud_security_group.security_group.created_at
}

output "security_group_updated_at" {
  value = viettelcloud_security_group.security_group.updated_at
}
