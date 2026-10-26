# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain lifecycle (shared fixture state):
#   run "createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned" {
#   run "changeNameAndDescription_existingVpc_vpcUpdatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "changeCidr_existingVpc_vpcReplaced" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Creates a VPC and verifies the expected resource and data-source fields.
run "createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned" {
  command = apply

  module {
    source = "./fixtures/vpc/resource"
  }

  variables {
    name        = "tf-test-vpc-before"
    description = "VPC before update"
    cidr        = "10.60.0.0/16"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_vpc.vpc.name == "tf-test-vpc-before" &&
      data.viettelcloud_vpc.vpc.name == "tf-test-vpc-before" &&
      viettelcloud_vpc.vpc.cidr == "10.60.0.0/16" &&
      viettelcloud_vpc.vpc.region.name == "  vn-central-1  " &&
      length(viettelcloud_vpc.vpc.id) > 0
    )
    error_message = "VPC was not created with the expected values."
  }
}

# Updates the VPC name and description without replacing the VPC.
run "changeNameAndDescription_existingVpc_vpcUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/vpc/resource"
  }

  variables {
    name        = "tf-test-vpc-after"
    description = "VPC after update"
    cidr        = "10.60.0.0/16"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_vpc.vpc.id == run.createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned.vpc_id &&
      viettelcloud_vpc.vpc.name == "tf-test-vpc-after" &&
      data.viettelcloud_vpc.vpc.name == "tf-test-vpc-after" &&
      viettelcloud_vpc.vpc.description == "VPC after update" &&
      data.viettelcloud_vpc.vpc.description == "VPC after update"
    )
    error_message = "VPC was replaced or was not updated."
  }
}

# Omitting description preserves the backend value without replacing the VPC.
run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/vpc/resource"
  }

  variables {
    name   = "tf-test-vpc-after"
    cidr   = "10.60.0.0/16"
    region = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_vpc.vpc.id == run.createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned.vpc_id &&
      viettelcloud_vpc.vpc.description == "VPC after update" &&
      data.viettelcloud_vpc.vpc.description == "VPC after update"
    )
    error_message = "Omitting description unexpectedly changed the VPC description or replaced the VPC."
  }
}

# An explicit empty description clears the backend value in place.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/vpc/resource"
  }

  variables {
    name        = "tf-test-vpc-after"
    description = ""
    cidr        = "10.60.0.0/16"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_vpc.vpc.id == run.createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned.vpc_id &&
      viettelcloud_vpc.vpc.description == "" &&
      data.viettelcloud_vpc.vpc.description == ""
    )
    error_message = "VPC description was not cleared in place."
  }
}

# Changing CIDR requires a replacement VPC with the requested CIDR.
run "changeCidr_existingVpc_vpcReplaced" {
  command = apply

  module {
    source = "./fixtures/vpc/resource"
  }

  variables {
    name        = "tf-test-vpc-after"
    description = ""
    cidr        = "10.62.0.0/16"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_vpc.vpc.id != run.createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned.vpc_id &&
      viettelcloud_vpc.vpc.cidr == "10.62.0.0/16"
    )
    error_message = "VPC was not replaced after changing its CIDR."
  }
}
