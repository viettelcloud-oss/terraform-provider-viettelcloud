# Filesystem created from a snapshot for the from_snapshot chain of
# tests/filesystem_resource.tftest.hcl. It configures only name and snapshot,
# plus size when a run grows the filesystem, because the API rejects a create
# with snapshot_id and the fields of a new filesystem. The data source reads
# the filesystem back by ID, so the backend_* outputs show what the backend
# recorded, independent of the managed resource state.
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

# Null leaves size out of the configuration, so the backend takes it from the
# snapshot and the state keeps it.
variable "size" {
  type    = number
  default = null
}

variable "snapshot_id" {
  type = string
}

resource "viettelcloud_fs_filesystem" "filesystem" {
  name = var.name
  size = var.size

  snapshot = {
    id = var.snapshot_id
  }
}

data "viettelcloud_fs_filesystem" "restored" {
  id = viettelcloud_fs_filesystem.filesystem.id
}

output "filesystem_id" {
  value = viettelcloud_fs_filesystem.filesystem.id
}

output "filesystem_status" {
  value = viettelcloud_fs_filesystem.filesystem.status
}

output "filesystem_size" {
  value = viettelcloud_fs_filesystem.filesystem.size
}

output "filesystem_protocol" {
  value = viettelcloud_fs_filesystem.filesystem.protocol
}

output "filesystem_type_name" {
  value = viettelcloud_fs_filesystem.filesystem.filesystem_type.name
}

output "filesystem_type_id" {
  value = viettelcloud_fs_filesystem.filesystem.filesystem_type.id
}

output "filesystem_subnet_id" {
  value = viettelcloud_fs_filesystem.filesystem.subnet.id
}

output "filesystem_snapshot_id" {
  value = viettelcloud_fs_filesystem.filesystem.snapshot.id
}

output "backend_size" {
  value = data.viettelcloud_fs_filesystem.restored.size
}

output "backend_snapshot_id" {
  value = data.viettelcloud_fs_filesystem.restored.snapshot_id
}

output "backend_filesystem_type_id" {
  value = data.viettelcloud_fs_filesystem.restored.filesystem_type_id
}
