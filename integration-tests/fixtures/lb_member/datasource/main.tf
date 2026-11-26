# This fixture creates one Server Group Member and resolves it through the
# direct ID, name, and combined data-source lookups.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "server_group_id" { type = string }
variable "address" { type = string }

resource "viettelcloud_lb_member" "subject" {
  server_group  = { id = var.server_group_id }
  name          = "tf-test-member-datasource"
  address       = var.address
  protocol_port = 80
  weight        = 3
  backup        = true
}

data "viettelcloud_lb_member" "by_id" {
  server_group_id = var.server_group_id
  id              = viettelcloud_lb_member.subject.id
}

data "viettelcloud_lb_member" "by_name" {
  server_group_id = var.server_group_id
  name            = viettelcloud_lb_member.subject.name
}

data "viettelcloud_lb_member" "by_all" {
  server_group_id = var.server_group_id
  id              = upper(viettelcloud_lb_member.subject.id)
  name            = " ${viettelcloud_lb_member.subject.name} "
}

# The data source scopes the lookup by a flat server_group_id, while the
# resource references the group as an object, so the subject is compared in the
# data-source shape.
output "subject_lookup_view" {
  value = merge(
    { for key, value in viettelcloud_lb_member.subject : key => value if key != "server_group" },
    { server_group_id = viettelcloud_lb_member.subject.server_group.id },
  )
}
output "by_id" { value = data.viettelcloud_lb_member.by_id }
output "by_name" { value = data.viettelcloud_lb_member.by_name }
output "by_all" { value = data.viettelcloud_lb_member.by_all }
