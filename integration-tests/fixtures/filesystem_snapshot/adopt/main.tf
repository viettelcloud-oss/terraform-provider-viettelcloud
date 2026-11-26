# Deletes a snapshot outside the state that created it, for the drift chain of
# tests/filesystem_snapshot_resource.tftest.hcl. With adopt = true it imports
# the snapshot into its own state key. With adopt = false the resource leaves
# the configuration, so apply destroys the snapshot on the backend while the
# creating state still records it.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "adopt" { type = bool }
variable "snapshot_id" { type = string }
variable "name" { type = string }
variable "filesystem_id" { type = string }

locals {
  adopted = var.adopt ? toset([var.snapshot_id]) : toset([])
}

import {
  for_each = local.adopted
  to       = viettelcloud_fs_filesystem_snapshot.adopted[each.key]
  id       = each.value
}

resource "viettelcloud_fs_filesystem_snapshot" "adopted" {
  for_each = local.adopted

  name       = var.name
  filesystem = { id = var.filesystem_id }
}

output "adopted_count" { value = length(viettelcloud_fs_filesystem_snapshot.adopted) }
