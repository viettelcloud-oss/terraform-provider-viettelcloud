# Plan-only fixture for the import chain of
# tests/member_roles_resource.tftest.hcl. It imports the role set that run
# "createMemberRoles_importChainFixture_rolesAssigned" created, rather than
# creating one of its own.
#
# Never apply it. That role set already belongs to state_key "import", so a
# second owner would clear the member's roles twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "member_roles_id" {
  type = string
}

variable "username" {
  type = string
}

variable "roles" {
  type = list(object({
    urn     = string
    enabled = bool
  }))
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_project_member_roles.imported
  id = var.member_roles_id
}

# member and roles are required, so both come from the state of the run that
# created this role set. Every remaining attribute is computed.
resource "viettelcloud_project_member_roles" "imported" {
  member = {
    username = var.username
  }

  roles = [
    for role in var.roles : {
      role    = { urn = role.urn }
      enabled = role.enabled
    }
  ]
}
