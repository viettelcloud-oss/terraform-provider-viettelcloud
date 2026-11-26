# Plan-only fixture for the import chain of
# tests/vpc_peering_resource.tftest.hcl. It imports the VPC peering that run
# "createVpcPeering_importChainDescriptionOmitted_peeringActive" created,
# rather than creating one of its own.
#
# Never apply it. That VPC peering already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "peering_id" {
  type = string
}

variable "requester_vpc_id" {
  type = string
}

variable "accepter_vpc_id" {
  type = string
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_vpc_peering.imported
  id = var.peering_id
}

# Only the required references are set, fed from the state of the run that
# created this VPC peering. description is optional and computed, so leaving
# it unset keeps whatever the import refresh read.
resource "viettelcloud_vpc_peering" "imported" {
  requester_vpc = { id = var.requester_vpc_id }
  accepter_vpc  = { id = var.accepter_vpc_id }
}
