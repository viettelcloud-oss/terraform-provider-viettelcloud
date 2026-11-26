# This fixture owns one Custom Role of the test project. Every chain gives
# its role a display name that no other chain and no existing role uses,
# because the backend derives a name that must be unique in the project.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "display_name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

# A null enabled leaves the attribute unconfigured, so the provider applies
# its default of true.
variable "enabled" {
  type    = bool
  default = null
}

variable "action_urns" {
  type = list(string)
}

resource "viettelcloud_project_custom_role" "this" {
  display_name = var.display_name
  description  = var.description
  enabled      = var.enabled
  actions      = [for urn in var.action_urns : { urn = urn }]
}

output "custom_role_id" {
  value = viettelcloud_project_custom_role.this.id
}

output "name" {
  value = viettelcloud_project_custom_role.this.name
}

output "display_name" {
  value = viettelcloud_project_custom_role.this.display_name
}

output "description" {
  value = viettelcloud_project_custom_role.this.description
}

output "enabled" {
  value = viettelcloud_project_custom_role.this.enabled
}

output "urn" {
  value = viettelcloud_project_custom_role.this.urn
}

output "action_urns" {
  value = sort([for action in viettelcloud_project_custom_role.this.actions : action.urn])
}

output "created_at" {
  value = viettelcloud_project_custom_role.this.created_at
}

output "updated_at" {
  value = viettelcloud_project_custom_role.this.updated_at
}
