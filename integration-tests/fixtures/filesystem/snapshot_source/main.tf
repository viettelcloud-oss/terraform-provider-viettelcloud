# Source objects for the from_snapshot chain of
# tests/filesystem_resource.tftest.hcl: one NFS filesystem and two snapshots
# of it. Snapshot B waits for snapshot A so the backend takes one snapshot of
# the filesystem at a time.
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

variable "filesystem_type_name" {
  type = string
}

variable "zone_name" {
  type = string
}

variable "subnet_id" {
  type = string
}

resource "viettelcloud_fs_filesystem" "source" {
  name     = var.name
  size     = var.size
  protocol = "NFS"

  filesystem_type = {
    name      = var.filesystem_type_name
    zone_name = var.zone_name
  }

  subnet = {
    id = var.subnet_id
  }
}

resource "viettelcloud_fs_filesystem_snapshot" "a" {
  name       = "${var.name}-a"
  filesystem = { id = viettelcloud_fs_filesystem.source.id }
}

resource "viettelcloud_fs_filesystem_snapshot" "b" {
  name       = "${var.name}-b"
  filesystem = { id = viettelcloud_fs_filesystem.source.id }

  depends_on = [viettelcloud_fs_filesystem_snapshot.a]
}

output "filesystem_id" {
  value = viettelcloud_fs_filesystem.source.id
}

output "snapshot_a_id" {
  value = viettelcloud_fs_filesystem_snapshot.a.id
}

output "snapshot_a_status" {
  value = viettelcloud_fs_filesystem_snapshot.a.status
}

output "snapshot_b_id" {
  value = viettelcloud_fs_filesystem_snapshot.b.id
}

output "snapshot_b_status" {
  value = viettelcloud_fs_filesystem_snapshot.b.status
}
