# This fixture looks up one Custom Role by ID, by name, or by both. It
# creates nothing.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "id" {
  type    = string
  default = null
}

variable "name" {
  type    = string
  default = null
}

data "viettelcloud_project_custom_role" "this" {
  id   = var.id
  name = var.name
}

output "custom_role_id" {
  value = data.viettelcloud_project_custom_role.this.id
}
