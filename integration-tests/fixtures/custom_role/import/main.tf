# Plan-only fixture for the import chain of
# tests/custom_role_resource.tftest.hcl. It imports the Custom Role that run
# "createCustomRole_importChainFixture_roleCreated" created, rather than
# creating one of its own.
#
# Never apply it. That Custom Role already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "custom_role_id" {
  type = string
}

variable "display_name" {
  type = string
}

variable "action_urns" {
  type = list(string)
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_project_custom_role.imported
  id = var.custom_role_id
}

# Only the required attributes are set, fed from the state of the run that
# created this Custom Role. description is optional and computed, so leaving
# it unset keeps whatever the import refresh read. enabled is left unset too:
# the role is enabled, which matches the default.
resource "viettelcloud_project_custom_role" "imported" {
  display_name = var.display_name
  actions      = [for urn in var.action_urns : { urn = urn }]
}
