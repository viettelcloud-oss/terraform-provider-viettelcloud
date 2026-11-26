# Creates two VPCs and one VPC peering between them, then resolves the peering
# through every supported data-source lookup.

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

variable "requester_vpc_name" {
  type = string
}

variable "requester_vpc_cidr" {
  type = string
}

variable "accepter_vpc_name" {
  type = string
}

variable "accepter_vpc_cidr" {
  type = string
}

resource "viettelcloud_vpc" "requester" {
  name   = var.requester_vpc_name
  cidr   = var.requester_vpc_cidr
  region = { name = var.region }
}

resource "viettelcloud_vpc" "accepter" {
  name   = var.accepter_vpc_name
  cidr   = var.accepter_vpc_cidr
  region = { name = var.region }
}

resource "viettelcloud_vpc_peering" "subject" {
  requester_vpc = { id = viettelcloud_vpc.requester.id }
  accepter_vpc  = { id = viettelcloud_vpc.accepter.id }
  description   = "VPC peering data-source integration subject"
}

data "viettelcloud_vpc_peering" "by_id" {
  id = viettelcloud_vpc_peering.subject.id
}

# The upper-case requester ID checks that the configured spelling is kept.
data "viettelcloud_vpc_peering" "by_requester_and_accepter" {
  requester_vpc_id = upper(viettelcloud_vpc.requester.id)
  accepter_vpc_id  = viettelcloud_vpc.accepter.id
  depends_on       = [viettelcloud_vpc_peering.subject]
}

# The padded status checks that the configured spelling is kept.
data "viettelcloud_vpc_peering" "by_requester_accepter_and_active_status" {
  requester_vpc_id = viettelcloud_vpc.requester.id
  accepter_vpc_id  = viettelcloud_vpc.accepter.id
  status           = " active "
  depends_on       = [viettelcloud_vpc_peering.subject]
}

data "viettelcloud_vpc_peering" "by_name" {
  name = viettelcloud_vpc_peering.subject.name
}
