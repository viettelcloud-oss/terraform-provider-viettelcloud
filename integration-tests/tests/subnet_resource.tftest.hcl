# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain lifecycle (shared fixture state):
#   run "createSubnet_descriptionOmitted_completeSubnetStateReturned" {
#   run "changeNameAndDescription_existingSubnet_subnetUpdatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "setUntrimmedName_existingSubnet_configuredNamePreserved" {
#   run "planAgain_untrimmedName_stateStable" {
#   run "changeCidr_existingSubnet_subnetReplaced" {
#   run "changeVpc_existingSubnet_subnetReplacedAndParentRefreshed" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Create with an omitted optional description and verify every backend field.
run "createSubnet_descriptionOmitted_completeSubnetStateReturned" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region      = "vn-central-1"
    vpc_name    = "tf-test-subnet-resource-vpc"
    vpc_cidr    = "10.70.0.0/16"
    subnet_name = "tf-test-subnet-before"
    subnet_cidr = "10.70.1.0/24"
  }

  assert {
    condition = (
      length(viettelcloud_subnet.subnet.id) > 0 &&
      viettelcloud_subnet.subnet.name == "tf-test-subnet-before" &&
      viettelcloud_subnet.subnet.description == "" &&
      viettelcloud_subnet.subnet.cidr == "10.70.1.0/24" &&
      viettelcloud_subnet.subnet.vpc.id == viettelcloud_vpc.vpc.id &&
      viettelcloud_subnet.subnet.vpc_name == "tf-test-subnet-resource-vpc" &&
      viettelcloud_subnet.subnet.region == "vn-central-1" &&
      length(viettelcloud_subnet.subnet.display_name) > 0 &&
      length(viettelcloud_subnet.subnet.created_at) > 0 &&
      length(viettelcloud_subnet.subnet.updated_at) > 0
    )
    error_message = "Subnet was not created with the expected state and platform metadata."
  }
}

# Name and description are mutable and must update without replacement.
run "changeNameAndDescription_existingSubnet_subnetUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region             = "vn-central-1"
    vpc_name           = "tf-test-subnet-resource-vpc"
    vpc_cidr           = "10.70.0.0/16"
    subnet_name        = "tf-test-subnet-after"
    subnet_description = "Subnet after update"
    subnet_cidr        = "10.70.1.0/24"
  }

  assert {
    condition = (
      viettelcloud_subnet.subnet.id == run.createSubnet_descriptionOmitted_completeSubnetStateReturned.subnet_id &&
      viettelcloud_subnet.subnet.name == "tf-test-subnet-after" &&
      viettelcloud_subnet.subnet.description == "Subnet after update" &&
      viettelcloud_subnet.subnet.cidr == "10.70.1.0/24"
    )
    error_message = "Subnet name or description was not updated in place."
  }
}

# Removing description from configuration preserves the backend value.
run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region      = "vn-central-1"
    vpc_name    = "tf-test-subnet-resource-vpc"
    vpc_cidr    = "10.70.0.0/16"
    subnet_name = "tf-test-subnet-after"
    subnet_cidr = "10.70.1.0/24"
  }

  assert {
    condition = (
      viettelcloud_subnet.subnet.id == run.createSubnet_descriptionOmitted_completeSubnetStateReturned.subnet_id &&
      viettelcloud_subnet.subnet.description == "Subnet after update"
    )
    error_message = "Omitting description changed the backend value or replaced the subnet."
  }
}

# An explicit empty description clears the backend value in place.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region             = "vn-central-1"
    vpc_name           = "tf-test-subnet-resource-vpc"
    vpc_cidr           = "10.70.0.0/16"
    subnet_name        = "tf-test-subnet-after"
    subnet_description = ""
    subnet_cidr        = "10.70.1.0/24"
  }

  assert {
    condition = (
      viettelcloud_subnet.subnet.id == run.createSubnet_descriptionOmitted_completeSubnetStateReturned.subnet_id &&
      viettelcloud_subnet.subnet.description == ""
    )
    error_message = "Explicitly clearing description replaced the subnet or retained the old value."
  }
}

# The request is trimmed while the configured representation remains stable in state.
run "setUntrimmedName_existingSubnet_configuredNamePreserved" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region             = "vn-central-1"
    vpc_name           = "tf-test-subnet-resource-vpc"
    vpc_cidr           = "10.70.0.0/16"
    subnet_name        = "  tf-test-subnet-after  "
    subnet_description = ""
    subnet_cidr        = "10.70.1.0/24"
  }

  assert {
    condition = (
      viettelcloud_subnet.subnet.id == run.createSubnet_descriptionOmitted_completeSubnetStateReturned.subnet_id &&
      viettelcloud_subnet.subnet.name == "  tf-test-subnet-after  "
    )
    error_message = "The configured subnet name representation was not preserved."
  }
}

# Replanning the semantically equivalent name must remain stable.
run "planAgain_untrimmedName_stateStable" {
  command = plan

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region             = "vn-central-1"
    vpc_name           = "tf-test-subnet-resource-vpc"
    vpc_cidr           = "10.70.0.0/16"
    subnet_name        = "  tf-test-subnet-after  "
    subnet_description = ""
    subnet_cidr        = "10.70.1.0/24"
  }

  assert {
    condition = (
      viettelcloud_subnet.subnet.id == run.createSubnet_descriptionOmitted_completeSubnetStateReturned.subnet_id &&
      viettelcloud_subnet.subnet.name == "  tf-test-subnet-after  "
    )
    error_message = "The normalized subnet name caused drift or plan flapping."
  }
}

# CIDR is immutable and must replace the subnet while retaining its parent VPC.
run "changeCidr_existingSubnet_subnetReplaced" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region             = "vn-central-1"
    vpc_name           = "tf-test-subnet-resource-vpc"
    vpc_cidr           = "10.70.0.0/16"
    subnet_name        = "tf-test-subnet-after"
    subnet_description = ""
    subnet_cidr        = "10.70.2.0/24"
  }

  assert {
    condition = (
      viettelcloud_subnet.subnet.id != run.createSubnet_descriptionOmitted_completeSubnetStateReturned.subnet_id &&
      viettelcloud_subnet.subnet.cidr == "10.70.2.0/24" &&
      viettelcloud_subnet.subnet.vpc.id == run.createSubnet_descriptionOmitted_completeSubnetStateReturned.vpc_id
    )
    error_message = "Changing CIDR did not replace the subnet with the expected state."
  }
}

# Replacing the parent VPC must also replace the subnet and refresh references.
run "changeVpc_existingSubnet_subnetReplacedAndParentRefreshed" {
  command = apply

  module {
    source = "./fixtures/subnet/resource"
  }

  variables {
    region             = "vn-central-1"
    vpc_name           = "tf-test-subnet-resource-vpc"
    vpc_cidr           = "10.70.0.0/17"
    subnet_name        = "tf-test-subnet-after"
    subnet_description = ""
    subnet_cidr        = "10.70.2.0/24"
  }

  assert {
    condition = (
      viettelcloud_vpc.vpc.id != run.createSubnet_descriptionOmitted_completeSubnetStateReturned.vpc_id &&
      viettelcloud_subnet.subnet.id != run.changeCidr_existingSubnet_subnetReplaced.subnet_id &&
      viettelcloud_subnet.subnet.vpc.id == viettelcloud_vpc.vpc.id &&
      viettelcloud_subnet.subnet.vpc_name == "tf-test-subnet-resource-vpc" &&
      viettelcloud_subnet.subnet.region == "vn-central-1"
    )
    error_message = "Replacing the parent VPC did not replace and refresh the subnet."
  }
}
