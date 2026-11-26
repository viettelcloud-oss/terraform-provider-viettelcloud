# Creates a requester VPC in the provider project, an accepter VPC in a second
# project, and one VPC peering between them. The peering stays pending until
# the second project accepts it outside Terraform.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source                = "viettelcloud-oss/viettelcloud"
      configuration_aliases = [viettelcloud.accepter]
    }
  }
}

variable "region" {
  type = string
}

resource "viettelcloud_vpc" "requester" {
  name   = "tf-test-peering-xproj-requester"
  cidr   = "10.58.0.0/16"
  region = { name = var.region }
}

resource "viettelcloud_vpc" "accepter" {
  provider = viettelcloud.accepter

  name   = "tf-test-peering-xproj-accepter"
  cidr   = "10.59.0.0/16"
  region = { name = var.region }
}

resource "viettelcloud_vpc_peering" "peering" {
  requester_vpc = { id = viettelcloud_vpc.requester.id }
  accepter_vpc  = { id = viettelcloud_vpc.accepter.id }
}

output "peering_id" {
  value = viettelcloud_vpc_peering.peering.id
}

output "peering_status" {
  value = viettelcloud_vpc_peering.peering.status
}

output "peering_accepter_project_id" {
  value = viettelcloud_vpc_peering.peering.accepter_project_id
}

output "requester_vpc_id" {
  value = viettelcloud_vpc.requester.id
}

output "accepter_vpc_id" {
  value = viettelcloud_vpc.accepter.id
}
