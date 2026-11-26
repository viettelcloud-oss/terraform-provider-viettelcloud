# Plan-only fixture for the import chain of
# tests/filesystem_snapshot_resource.tftest.hcl. It imports the snapshot that
# run "createFilesystemSnapshot_importChainFixture_created" created, rather
# than creating one of its own.
#
# Never apply it. That snapshot already belongs to state_key "import", so a
# second owner would delete it twice at teardown.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "id" { type = string }
variable "name" { type = string }
variable "filesystem_id" { type = string }

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_fs_filesystem_snapshot.imported
  id = var.id
}

# Only the required attributes are set, fed from the state of the run that
# created the snapshot. description is optional and computed, so leaving it
# unset keeps whatever the import refresh read.
resource "viettelcloud_fs_filesystem_snapshot" "imported" {
  name       = var.name
  filesystem = { id = var.filesystem_id }
}

output "snapshot" { value = viettelcloud_fs_filesystem_snapshot.imported }
