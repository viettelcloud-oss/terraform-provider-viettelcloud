# Plan-only fixture for the import chain of
# tests/filesystem_resource.tftest.hcl. It imports the filesystem that
# run "createFilesystem_importChainFixture_filesystemCreated" created.
#
# Never apply it. That filesystem already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
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

# The run loads this fixture through its module block. Keep the import block
# here because an import block inside a run block is unsupported.
import {
  to = viettelcloud_fs_filesystem.imported
  id = var.filesystem_id
}

# Only Required attributes are set, fed from the creating run's state.
# description is Optional and Computed, so leaving it unset keeps whatever
# the import refresh read. Every remaining attribute is Computed.
resource "viettelcloud_fs_filesystem" "imported" {
  name     = var.name
  size     = var.size
  protocol = var.protocol

  # Import restores only the filesystem type id and name, so the selector
  # names the type by name alone. A zone_name criterion has no imported value
  # and would plan an in-place update.
  filesystem_type = {
    name = var.filesystem_type_name
  }

  subnet = {
    id = var.subnet_id
  }
}

output "filesystem_id" {
  value = viettelcloud_fs_filesystem.imported.id
}

output "filesystem_status" {
  value = viettelcloud_fs_filesystem.imported.status
}

output "filesystem_project_id" {
  value = viettelcloud_fs_filesystem.imported.project_id
}

output "filesystem_mount_target_address" {
  value = viettelcloud_fs_filesystem.imported.mount_target_address
}
