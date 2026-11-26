# Data source fixture for tests/filesystem_data_source.tftest.hcl. It creates
# one filesystem and looks it up by ID, by name and zone, and by name and
# subnet_id, so that each lookup can be compared with the managed filesystem.

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
  name     = var.name
  size     = 100
  protocol = "NFS"

  filesystem_type = {
    name      = var.filesystem_type_name
    zone_name = var.zone_name
  }

  subnet = {
    id = var.subnet_id
  }
}

# Direct ID lookup: the canonical path for known UUIDs.
data "viettelcloud_fs_filesystem" "by_id" {
  id = viettelcloud_fs_filesystem.filesystem.id
}

# Name + zone lookup: guaranteed unique when the test fixture is the only
# filesystem with this name in the zone. The criteria read the managed
# filesystem so that Terraform looks it up after the filesystem exists.
data "viettelcloud_fs_filesystem" "by_name_zone" {
  name = viettelcloud_fs_filesystem.filesystem.name
  zone = viettelcloud_fs_filesystem.filesystem.zone_name
}

# Name + subnet_id lookup: guaranteed unique for the same reason.
data "viettelcloud_fs_filesystem" "by_name_subnet" {
  name      = viettelcloud_fs_filesystem.filesystem.name
  subnet_id = viettelcloud_fs_filesystem.filesystem.subnet.id
}

output "filesystem_id" {
  value = viettelcloud_fs_filesystem.filesystem.id
}

output "filesystem_zone_name" {
  value = viettelcloud_fs_filesystem.filesystem.zone_name
}

output "filesystem_protocol" {
  value = viettelcloud_fs_filesystem.filesystem.protocol
}

output "filesystem_project_id" {
  value = viettelcloud_fs_filesystem.filesystem.project_id
}

output "filesystem_status" {
  value = viettelcloud_fs_filesystem.filesystem.status
}
