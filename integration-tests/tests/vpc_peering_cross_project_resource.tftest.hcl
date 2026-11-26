# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain crossproject (state_key = "crossproject"; the requester VPC lives in
# the provider project and the accepter VPC in a second project, so the
# peering stays pending):
#   run "createVpcPeering_accepterVpcInOtherProject_peeringPending" {
#   run "planAgain_pendingCrossProjectPeering_stateStable" {

variable "token" {
  description = "Viettel Cloud API access token used by this test."
  type        = string
  sensitive   = true
}

variable "project_id" {
  description = "Project UUID or slug used by this test for the requester VPC."
  type        = string
}

variable "api_endpoint" {
  description = "Viettel Cloud API endpoint used by this test."
  type        = string
}

variable "accepter_project_id" {
  description = "UUID of a second project that owns the accepter VPC. The token must be allowed to create and delete VPCs there."
  type        = string
}

variable "region" {
  description = "Region of both VPCs that this test creates."
  type        = string
  default     = "vn-central-1"
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

provider "viettelcloud" {
  alias      = "accepter"
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.accepter_project_id
}


# Chain crossproject.

# A peering with a VPC of another project is pending in the create response.
# The provider does not wait for the other project to accept it.
run "createVpcPeering_accepterVpcInOtherProject_peeringPending" {
  command   = apply
  parallel  = true
  state_key = "crossproject"

  module {
    source = "./fixtures/vpc_peering/crossproject"
  }

  providers = {
    viettelcloud          = viettelcloud
    viettelcloud.accepter = viettelcloud.accepter
  }

  variables {
    region = var.region
  }

  assert {
    condition = (
      length(viettelcloud_vpc_peering.peering.id) > 0 &&
      viettelcloud_vpc_peering.peering.status == "pending" &&
      viettelcloud_vpc_peering.peering.accepter_project_id == var.accepter_project_id &&
      viettelcloud_vpc_peering.peering.requester_vpc.id == viettelcloud_vpc.requester.id &&
      viettelcloud_vpc_peering.peering.accepter_vpc.id == viettelcloud_vpc.accepter.id
    )
    error_message = "The cross-project VPC peering was not created pending."
  }
}

# Replanning the pending peering plans no change. Any planned change makes the
# computed updated_at unknown, so the length assertion fails.
run "planAgain_pendingCrossProjectPeering_stateStable" {
  command   = plan
  parallel  = true
  state_key = "crossproject"

  module {
    source = "./fixtures/vpc_peering/crossproject"
  }

  providers = {
    viettelcloud          = viettelcloud
    viettelcloud.accepter = viettelcloud.accepter
  }

  variables {
    region = var.region
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id == run.createVpcPeering_accepterVpcInOtherProject_peeringPending.peering_id &&
      viettelcloud_vpc_peering.peering.status == "pending" &&
      length(viettelcloud_vpc_peering.peering.updated_at) > 0
    )
    error_message = "Replanning the pending cross-project peering planned a change."
  }
}
