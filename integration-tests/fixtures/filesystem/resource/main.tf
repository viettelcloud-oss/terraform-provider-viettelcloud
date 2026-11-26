# Managed filesystem for tests/filesystem_resource.tftest.hcl. The lifecycle
# chain, the protocol, filesystem_type, and subnet replacement chains, and the
# import chain each apply it in their own state_key and change it through the
# variables below.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "name" {
  type = string
}

variable "size" {
  type    = number
  default = 100
}

variable "protocol" {
  type    = string
  default = "NFS"
}

variable "description" {
  type    = string
  default = null
}

variable "filesystem_type_name" {
  type = string
}

variable "zone_name" {
  type = string
}

variable "subnet_id" {
  type = string
}

resource "viettelcloud_fs_filesystem" "filesystem" {
  name        = var.name
  size        = var.size
  protocol    = var.protocol
  description = var.description

  filesystem_type = {
    name      = var.filesystem_type_name
    zone_name = var.zone_name
  }

  subnet = {
    id = var.subnet_id
  }
}

output "filesystem_id" {
  value = viettelcloud_fs_filesystem.filesystem.id
}

output "filesystem_name" {
  value = viettelcloud_fs_filesystem.filesystem.name
}

output "filesystem_size" {
  value = viettelcloud_fs_filesystem.filesystem.size
}

output "filesystem_protocol" {
  value = viettelcloud_fs_filesystem.filesystem.protocol
}

output "filesystem_description" {
  value = viettelcloud_fs_filesystem.filesystem.description
}

output "filesystem_status" {
  value = viettelcloud_fs_filesystem.filesystem.status
}

output "filesystem_zone_name" {
  value = viettelcloud_fs_filesystem.filesystem.zone_name
}

output "filesystem_zone_id" {
  value = viettelcloud_fs_filesystem.filesystem.zone_id
}

output "filesystem_subnet_id" {
  value = viettelcloud_fs_filesystem.filesystem.subnet.id
}

output "filesystem_mount_target_address" {
  value = viettelcloud_fs_filesystem.filesystem.mount_target_address
}

output "filesystem_project_id" {
  value = viettelcloud_fs_filesystem.filesystem.project_id
}

output "filesystem_created_at" {
  value = viettelcloud_fs_filesystem.filesystem.created_at
}
