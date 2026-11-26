# Deletes a filesystem outside the state that created it, for the drift chain
# of tests/filesystem_resource.tftest.hcl. With adopt = true it imports the
# filesystem into its own state key. With adopt = false the resource leaves the
# configuration, so apply destroys the filesystem on the backend while the
# creating state still records it.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "adopt" {
  type = bool
}

variable "filesystem_id" {
  type = string
}

variable "name" {
  type = string
}

variable "size" {
  type = number
}

variable "protocol" {
  type = string
}

variable "filesystem_type_name" {
  type = string
}

variable "subnet_id" {
  type = string
}

locals {
  adopted = var.adopt ? toset([var.filesystem_id]) : toset([])
}

import {
  for_each = local.adopted
  to       = viettelcloud_fs_filesystem.adopted[each.key]
  id       = each.value
}

resource "viettelcloud_fs_filesystem" "adopted" {
  for_each = local.adopted

  name     = var.name
  size     = var.size
  protocol = var.protocol

  # Import restores only the filesystem type id and name, so the selector
  # names the type by name alone, as in the import fixture.
  filesystem_type = {
    name = var.filesystem_type_name
  }

  subnet = {
    id = var.subnet_id
  }
}

output "adopted_count" {
  value = length(viettelcloud_fs_filesystem.adopted)
}
