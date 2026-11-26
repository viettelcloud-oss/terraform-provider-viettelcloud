# Manages one viettelcloud_fs_filesystem_snapshot of an existing filesystem for
# the chains lifecycle, description, import, and drift of
# tests/filesystem_snapshot_resource.tftest.hcl. description defaults to null,
# so a run that omits it leaves the attribute unconfigured.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "name" { type = string }
variable "filesystem_id" { type = string }
variable "description" {
  type    = string
  default = null
}

resource "viettelcloud_fs_filesystem_snapshot" "snapshot" {
  name        = var.name
  description = var.description
  filesystem  = { id = var.filesystem_id }
}

output "snapshot" { value = viettelcloud_fs_filesystem_snapshot.snapshot }
