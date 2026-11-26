# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (state_key = "lifecycle"; VPCs a, b, and c stay in place
# while the peering moves from a->b to a->c and then to b->c):
#   run "createVpcPeering_twoVpcsSameProject_peeringActive" {
#   run "planAgain_activePeering_stateStable" {
#   run "changeDescription_existingPeering_updatedInPlace" {
#   run "omitDescription_existingPeering_descriptionKept" {
#   run "setEmptyDescription_existingPeering_descriptionCleared" {
#   run "planAgain_emptyDescription_stateStable" {
#   run "changeAccepterVpc_existingPeering_replaced" {
#   run "changeRequesterVpc_existingPeering_replaced" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of
# the VPC peering that this chain creates for itself):
#   run "createVpcPeering_importChainDescriptionOmitted_peeringActive" {
#   run "importVpcPeering_existingPeering_noChanges" {

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

variable "region" {
  description = "Region of every VPC that this test creates."
  type        = string
  default     = "vn-central-1"
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

# A peering between two VPCs of the provider project is active in the create
# response, and every computed attribute is known.
run "createVpcPeering_twoVpcsSameProject_peeringActive" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "b"
    description       = "Peering before update"
  }

  assert {
    condition = (
      length(viettelcloud_vpc_peering.peering.id) > 0 &&
      startswith(viettelcloud_vpc_peering.peering.name, "peer-") &&
      viettelcloud_vpc_peering.peering.description == "Peering before update" &&
      viettelcloud_vpc_peering.peering.status == "active" &&
      viettelcloud_vpc_peering.peering.vpc_peering_type == "intra_region" &&
      length(viettelcloud_vpc_peering.peering.accepter_project_id) > 0 &&
      length(viettelcloud_vpc_peering.peering.created_at) > 0 &&
      length(viettelcloud_vpc_peering.peering.updated_at) > 0 &&
      viettelcloud_vpc_peering.peering.requester_vpc.id == viettelcloud_vpc.vpc["a"].id &&
      viettelcloud_vpc_peering.peering.accepter_vpc.id == viettelcloud_vpc.vpc["b"].id
    )
    error_message = "The VPC peering was not created active with the expected state."
  }
}

# Replanning the created peering plans no change. Any planned change makes the
# computed updated_at unknown, so the length assertion fails.
run "planAgain_activePeering_stateStable" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "b"
    description       = "Peering before update"
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id == run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_id &&
      viettelcloud_vpc_peering.peering.description == "Peering before update" &&
      length(viettelcloud_vpc_peering.peering.updated_at) > 0
    )
    error_message = "Replanning the created peering planned a change."
  }
}

# description is mutable and updates without replacement.
run "changeDescription_existingPeering_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "b"
    description       = "Peering after update"
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id == run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_id &&
      viettelcloud_vpc_peering.peering.description == "Peering after update" &&
      viettelcloud_vpc_peering.peering.created_at == run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_created_at
    )
    error_message = "The description was not updated in place."
  }
}

# Removing description from configuration keeps the current value.
run "omitDescription_existingPeering_descriptionKept" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "b"
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id == run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_id &&
      viettelcloud_vpc_peering.peering.description == "Peering after update"
    )
    error_message = "Omitting description changed the value or replaced the peering."
  }
}

# An explicit empty description clears the value in place.
run "setEmptyDescription_existingPeering_descriptionCleared" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "b"
    description       = ""
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id == run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_id &&
      viettelcloud_vpc_peering.peering.description == ""
    )
    error_message = "An empty description replaced the peering or kept the old value."
  }
}

# Replanning after the empty description plans no change.
run "planAgain_emptyDescription_stateStable" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "b"
    description       = ""
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id == run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_id &&
      viettelcloud_vpc_peering.peering.description == "" &&
      length(viettelcloud_vpc_peering.peering.updated_at) > 0
    )
    error_message = "Replanning the peering with an empty description planned a change."
  }
}

# accepter_vpc is immutable, so a new accepter VPC replaces the peering.
run "changeAccepterVpc_existingPeering_replaced" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "a"
    accepter_vpc_key  = "c"
    description       = ""
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id != run.createVpcPeering_twoVpcsSameProject_peeringActive.peering_id &&
      viettelcloud_vpc_peering.peering.status == "active" &&
      viettelcloud_vpc_peering.peering.requester_vpc.id == viettelcloud_vpc.vpc["a"].id &&
      viettelcloud_vpc_peering.peering.accepter_vpc.id == viettelcloud_vpc.vpc["c"].id
    )
    error_message = "Changing accepter_vpc did not replace the peering."
  }
}

# requester_vpc is immutable, so a new requester VPC replaces the peering.
run "changeRequesterVpc_existingPeering_replaced" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      a = { name = "tf-test-peering-lifecycle-a", cidr = "10.51.0.0/16" }
      b = { name = "tf-test-peering-lifecycle-b", cidr = "10.52.0.0/16" }
      c = { name = "tf-test-peering-lifecycle-c", cidr = "10.53.0.0/16" }
    }
    requester_vpc_key = "b"
    accepter_vpc_key  = "c"
    description       = ""
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.peering.id != run.changeAccepterVpc_existingPeering_replaced.peering_id &&
      viettelcloud_vpc_peering.peering.status == "active" &&
      viettelcloud_vpc_peering.peering.requester_vpc.id == viettelcloud_vpc.vpc["b"].id &&
      viettelcloud_vpc_peering.peering.accepter_vpc.id == viettelcloud_vpc.vpc["c"].id
    )
    error_message = "Changing requester_vpc did not replace the peering."
  }
}


# Chain import.

# The import chain creates the VPC peering that it later imports. description
# is omitted, so this run also shows the empty default at create.
run "createVpcPeering_importChainDescriptionOmitted_peeringActive" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/vpc_peering/resource"
  }

  variables {
    region = var.region
    vpcs = {
      requester = { name = "tf-test-peering-import-requester", cidr = "10.54.0.0/16" }
      accepter  = { name = "tf-test-peering-import-accepter", cidr = "10.55.0.0/16" }
    }
    requester_vpc_key = "requester"
    accepter_vpc_key  = "accepter"
  }

  assert {
    condition = (
      length(viettelcloud_vpc_peering.peering.id) > 0 &&
      viettelcloud_vpc_peering.peering.description == "" &&
      viettelcloud_vpc_peering.peering.status == "active"
    )
    error_message = "The import chain's VPC peering was not created active with an empty description."
  }
}

# Importing the VPC peering restores its state and plans no change. The run
# stays plan-only and uses its own state key, so that the peering keeps one
# owner at teardown. requester_vpc and accepter_vpc are configured from the
# creating run, so the assertions check the attributes that only the import
# refresh supplies. Any planned change makes these computed values unknown.
run "importVpcPeering_existingPeering_noChanges" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/vpc_peering/import"
  }

  variables {
    peering_id       = run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_id
    requester_vpc_id = run.createVpcPeering_importChainDescriptionOmitted_peeringActive.requester_vpc_id
    accepter_vpc_id  = run.createVpcPeering_importChainDescriptionOmitted_peeringActive.accepter_vpc_id
  }

  assert {
    condition = (
      viettelcloud_vpc_peering.imported.id == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_id &&
      viettelcloud_vpc_peering.imported.name == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_name &&
      viettelcloud_vpc_peering.imported.description == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_description &&
      viettelcloud_vpc_peering.imported.status == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_status &&
      viettelcloud_vpc_peering.imported.vpc_peering_type == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_vpc_peering_type &&
      viettelcloud_vpc_peering.imported.accepter_project_id == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_accepter_project_id &&
      viettelcloud_vpc_peering.imported.created_at == run.createVpcPeering_importChainDescriptionOmitted_peeringActive.peering_created_at &&
      length(viettelcloud_vpc_peering.imported.updated_at) > 0
    )
    error_message = "Importing the VPC peering did not restore its state without changes."
  }
}
