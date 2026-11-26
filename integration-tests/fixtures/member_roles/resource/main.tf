# This fixture owns the role set of one existing project member. Terraform
# cannot add or remove project members, so the member must already exist.
#
# Set assigned to false to destroy the role set inside a run: destroy clears
# every role and leaves the member in the project.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "username" {
  type = string
}

# An omitted enabled stays null, so the provider applies its default of true.
variable "roles" {
  type = list(object({
    urn     = string
    enabled = optional(bool)
  }))
}

variable "assigned" {
  type    = bool
  default = true
}

resource "viettelcloud_project_member_roles" "this" {
  count = var.assigned ? 1 : 0

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

output "username" {
  value = var.username
}

output "member_roles_id" {
  value = one(viettelcloud_project_member_roles.this[*].id)
}

output "member_user_id" {
  value = one(viettelcloud_project_member_roles.this[*].member_user_id)
}

output "member_email" {
  value = one(viettelcloud_project_member_roles.this[*].member_email)
}

output "member_is_admin" {
  value = one(viettelcloud_project_member_roles.this[*].member_is_admin)
}

output "created_at" {
  value = one(viettelcloud_project_member_roles.this[*].created_at)
}

output "updated_at" {
  value = one(viettelcloud_project_member_roles.this[*].updated_at)
}

output "assignments" {
  value = one(viettelcloud_project_member_roles.this[*].assignments)
}

# role_settings repeats the configured roles in the input shape of the
# import fixture, read back from state.
output "role_settings" {
  value = flatten([
    for member_roles in viettelcloud_project_member_roles.this : [
      for role in member_roles.roles : {
        urn     = role.role.urn
        enabled = role.enabled
      }
    ]
  ])
}
