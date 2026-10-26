terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "region" {
  type = string
}

variable "name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

variable "policy" {
  type = string
}

resource "viettelcloud_placement_group" "pg" {
  name        = var.name
  description = var.description
  policy      = var.policy
  region = {
    name = var.region
  }
}

output "placement_group_id" {
  value = viettelcloud_placement_group.pg.id
}

output "placement_group_name" {
  value = viettelcloud_placement_group.pg.name
}

output "placement_group_description" {
  value = viettelcloud_placement_group.pg.description
}

output "placement_group_policy" {
  value = viettelcloud_placement_group.pg.policy
}

output "placement_group_region" {
  value = viettelcloud_placement_group.pg.region.name
}

output "placement_group_region_id" {
  value = viettelcloud_placement_group.pg.region_id
}

output "placement_group_project" {
  value = viettelcloud_placement_group.pg.project
}

output "placement_group_server_count" {
  value = viettelcloud_placement_group.pg.server_count
}

output "placement_group_created_at" {
  value = viettelcloud_placement_group.pg.created_at
}

output "placement_group_updated_at" {
  value = viettelcloud_placement_group.pg.updated_at
}
