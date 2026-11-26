# Creates the VPCs in var.vpcs and one VPC peering between two of them. A run
# changes requester_vpc_key or accepter_vpc_key to replace the peering while
# every VPC stays in place.

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

variable "vpcs" {
  type = map(object({
    name = string
    cidr = string
  }))
}

variable "requester_vpc_key" {
  type = string
}

variable "accepter_vpc_key" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

resource "viettelcloud_vpc" "vpc" {
  for_each = var.vpcs

  name   = each.value.name
  cidr   = each.value.cidr
  region = { name = var.region }
}

resource "viettelcloud_vpc_peering" "peering" {
  requester_vpc = { id = viettelcloud_vpc.vpc[var.requester_vpc_key].id }
  accepter_vpc  = { id = viettelcloud_vpc.vpc[var.accepter_vpc_key].id }
  description   = var.description
}

output "vpc_ids" {
  value = { for key, vpc in viettelcloud_vpc.vpc : key => vpc.id }
}

output "peering_id" {
  value = viettelcloud_vpc_peering.peering.id
}

output "peering_name" {
  value = viettelcloud_vpc_peering.peering.name
}

output "peering_description" {
  value = viettelcloud_vpc_peering.peering.description
}

output "peering_status" {
  value = viettelcloud_vpc_peering.peering.status
}

output "peering_vpc_peering_type" {
  value = viettelcloud_vpc_peering.peering.vpc_peering_type
}

output "peering_accepter_project_id" {
  value = viettelcloud_vpc_peering.peering.accepter_project_id
}

output "peering_created_at" {
  value = viettelcloud_vpc_peering.peering.created_at
}

output "requester_vpc_id" {
  value = viettelcloud_vpc_peering.peering.requester_vpc.id
}

output "accepter_vpc_id" {
  value = viettelcloud_vpc_peering.peering.accepter_vpc.id
}
