# This fixture creates the shared security group for the security group rule
# resource test.
#
# The setup run owns the group so teardown removes it after every rule chain.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "name" {
  type = string
}

variable "region" {
  type = string
}

resource "viettelcloud_security_group" "shared" {
  name   = var.name
  region = { name = var.region }
}

output "security_group_id" {
  value = viettelcloud_security_group.shared.id
}

output "security_group_name" {
  value = viettelcloud_security_group.shared.name
}
