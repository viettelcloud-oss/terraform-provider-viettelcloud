# This fixture looks up the roles of one project member by username. It
# creates nothing, so the resource suite also uses it to observe a member
# after another run changed or destroyed that member's role set.

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

data "viettelcloud_project_member_roles" "this" {
  username = var.username
}

output "member_roles_id" {
  value = data.viettelcloud_project_member_roles.this.id
}

output "username" {
  value = data.viettelcloud_project_member_roles.this.username
}

output "member_user_id" {
  value = data.viettelcloud_project_member_roles.this.member_user_id
}

output "member_is_admin" {
  value = data.viettelcloud_project_member_roles.this.member_is_admin
}

output "assignments" {
  value = data.viettelcloud_project_member_roles.this.assignments
}
