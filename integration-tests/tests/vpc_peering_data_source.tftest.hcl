# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lookup (shared fixture state; the first run creates two VPCs and the
# peering, and each run checks one lookup of the same peering):
#   run "lookupVpcPeering_byID_peeringFound" {
#   run "lookupVpcPeering_byRequesterAndAccepter_peeringFound" {
#   run "lookupVpcPeering_byRequesterAccepterAndActiveStatus_peeringFound" {
#   run "lookupVpcPeering_byName_peeringFound" {

variable "token" {
  description = "Viettel Cloud API access token used by this test."
  type        = string
  sensitive   = true
}

variable "project_id" {
  description = "Project UUID or slug used by this test."
  type        = string
}

variable "api_endpoint" {
  description = "Viettel Cloud API endpoint used by this test."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variables {
  region             = "vn-central-1"
  requester_vpc_name = "tf-test-peering-ds-requester"
  requester_vpc_cidr = "10.56.0.0/16"
  accepter_vpc_name  = "tf-test-peering-ds-accepter"
  accepter_vpc_cidr  = "10.57.0.0/16"
}

# The direct ID lookup returns every attribute of the managed peering.
run "lookupVpcPeering_byID_peeringFound" {
  command = apply

  module {
    source = "./fixtures/vpc_peering/datasource"
  }

  assert {
    condition = (
      data.viettelcloud_vpc_peering.by_id.id == viettelcloud_vpc_peering.subject.id &&
      data.viettelcloud_vpc_peering.by_id.name == viettelcloud_vpc_peering.subject.name &&
      data.viettelcloud_vpc_peering.by_id.description == viettelcloud_vpc_peering.subject.description &&
      data.viettelcloud_vpc_peering.by_id.status == "active" &&
      data.viettelcloud_vpc_peering.by_id.requester_vpc_id == viettelcloud_vpc.requester.id &&
      data.viettelcloud_vpc_peering.by_id.accepter_vpc_id == viettelcloud_vpc.accepter.id &&
      data.viettelcloud_vpc_peering.by_id.vpc_peering_type == viettelcloud_vpc_peering.subject.vpc_peering_type &&
      data.viettelcloud_vpc_peering.by_id.accepter_project_id == viettelcloud_vpc_peering.subject.accepter_project_id &&
      data.viettelcloud_vpc_peering.by_id.created_at == viettelcloud_vpc_peering.subject.created_at &&
      length(data.viettelcloud_vpc_peering.by_id.updated_at) > 0
    )
    error_message = "The ID lookup did not return the full state of the peering."
  }
}

# The requester and accepter pair finds the one pending or active peering and
# keeps the configured upper-case requester ID.
run "lookupVpcPeering_byRequesterAndAccepter_peeringFound" {
  command = apply

  module {
    source = "./fixtures/vpc_peering/datasource"
  }

  assert {
    condition = (
      data.viettelcloud_vpc_peering.by_requester_and_accepter.id == viettelcloud_vpc_peering.subject.id &&
      data.viettelcloud_vpc_peering.by_requester_and_accepter.requester_vpc_id == upper(viettelcloud_vpc.requester.id) &&
      data.viettelcloud_vpc_peering.by_requester_and_accepter.accepter_vpc_id == viettelcloud_vpc.accepter.id &&
      data.viettelcloud_vpc_peering.by_requester_and_accepter.status == "active" &&
      data.viettelcloud_vpc_peering.by_requester_and_accepter.description == viettelcloud_vpc_peering.subject.description
    )
    error_message = "The requester and accepter lookup did not return the peering."
  }
}

# Adding the active status to the pair returns the same peering and keeps the
# configured padded status.
run "lookupVpcPeering_byRequesterAccepterAndActiveStatus_peeringFound" {
  command = apply

  module {
    source = "./fixtures/vpc_peering/datasource"
  }

  assert {
    condition = (
      data.viettelcloud_vpc_peering.by_requester_accepter_and_active_status.id == viettelcloud_vpc_peering.subject.id &&
      data.viettelcloud_vpc_peering.by_requester_accepter_and_active_status.status == " active " &&
      data.viettelcloud_vpc_peering.by_requester_accepter_and_active_status.name == viettelcloud_vpc_peering.subject.name
    )
    error_message = "The requester, accepter, and status lookup did not return the peering."
  }
}

# The generated name finds the peering.
run "lookupVpcPeering_byName_peeringFound" {
  command = apply

  module {
    source = "./fixtures/vpc_peering/datasource"
  }

  assert {
    condition = (
      data.viettelcloud_vpc_peering.by_name.id == viettelcloud_vpc_peering.subject.id &&
      data.viettelcloud_vpc_peering.by_name.name == viettelcloud_vpc_peering.subject.name &&
      data.viettelcloud_vpc_peering.by_name.requester_vpc_id == viettelcloud_vpc.requester.id &&
      data.viettelcloud_vpc_peering.by_name.accepter_vpc_id == viettelcloud_vpc.accepter.id
    )
    error_message = "The name lookup did not return the peering."
  }
}
