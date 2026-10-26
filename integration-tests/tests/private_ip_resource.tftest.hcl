# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain lifecycle (shared fixture state):
#   run "createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured" {
#   run "omitAllowedVips_attachedVips_attachmentsPreservedInPlace" {
#   run "plan_allowedVipsOmitted_stateStable" {
#   run "clearAllowedVips_attachedVips_attachmentsEmptyInPlace" {
#   run "updateDescriptionAndVips_clearedVips_descriptionAndVipsUpdatedInPlace" {
#   run "clearAllowedVips_updatedVips_attachmentsEmptyInPlace" {
#   run "updateDescription_existingPrivateIp_descriptionUpdatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "configureIpAddress_allocatedPrivateIp_privateIpReplaced" {
#   run "configureMacAddress_existingPrivateIp_privateIpReplaced" {
#   run "plan_equivalentSubnetIdAndMacAddress_stateStable" {
#   run "changeSubnet_existingPrivateIp_privateIpReplacedAndParentsRefreshed" {
#   run "attachVipWithNormalizedId_existingPrivateIp_vipIdAndAddressPreserved" {
#   run "plan_equivalentSubnetAndVipIds_stateStable" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Allocate an address and MAC automatically, with the optional description omitted.
run "createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region            = "vn-central-1"
    vpc_name          = "tf-test-private-ip-resource-vpc"
    vpc_cidr          = "10.80.0.0/16"
    allowed_vips_mode = "fixture"
  }

  assert {
    condition = (
      length(viettelcloud_private_ip.private_ip.id) > 0 &&
      try(length(viettelcloud_private_ip.private_ip.description), 0) == 0 &&
      length(viettelcloud_private_ip.private_ip.ip_address) > 0 &&
      length(viettelcloud_private_ip.private_ip.mac_address) > 0 &&
      viettelcloud_private_ip.private_ip.subnet.id == viettelcloud_subnet.subnet[0].id &&
      viettelcloud_private_ip.private_ip.subnet_name == "tf-test-private-ip-subnet-a" &&
      viettelcloud_private_ip.private_ip.subnet_cidr == "10.80.1.0/24" &&
      viettelcloud_private_ip.private_ip.vpc_id == viettelcloud_vpc.vpc.id &&
      viettelcloud_private_ip.private_ip.vpc_name == "tf-test-private-ip-resource-vpc" &&
      viettelcloud_private_ip.private_ip.region == "vn-central-1" &&
      viettelcloud_private_ip.private_ip.server_id == null &&
      viettelcloud_private_ip.private_ip.server_name == null &&
      viettelcloud_private_ip.private_ip.device_owner == "user" &&
      length(viettelcloud_private_ip.private_ip.display_name) > 0 &&
      viettelcloud_private_ip.private_ip.port_security &&
      (viettelcloud_private_ip.private_ip.allowed_cidrs == null || can(toset(viettelcloud_private_ip.private_ip.allowed_cidrs))) &&
      toset(viettelcloud_private_ip.private_ip.allowed_vips[*].id) == toset([viettelcloud_private_ip.vip.id]) &&
      try(length(viettelcloud_private_ip.private_ip.attached_vips), 0) == 1 &&
      tolist(viettelcloud_private_ip.private_ip.attached_vips)[0].id == viettelcloud_private_ip.vip.id &&
      tolist(viettelcloud_private_ip.private_ip.attached_vips)[0].ip_address == viettelcloud_private_ip.vip.ip_address &&
      length(viettelcloud_private_ip.private_ip.created_at) > 0 &&
      length(viettelcloud_private_ip.private_ip.updated_at) > 0
    )
    error_message = "Private IP was not created with the expected address, parent references, and platform metadata."
  }

  assert {
    condition = (
      length(viettelcloud_private_ip.without_allowed_vips.id) > 0 &&
      try(length(viettelcloud_private_ip.without_allowed_vips.allowed_vips), 0) == 0 &&
      try(length(viettelcloud_private_ip.without_allowed_vips.attached_vips), 0) == 0
    )
    error_message = "The omitted allowed VIP create path unexpectedly configured an attachment."
  }
}

# Removing allowed VIPs from configuration preserves the non-empty backend set.
run "omitAllowedVips_attachedVips_attachmentsPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region   = "vn-central-1"
    vpc_name = "tf-test-private-ip-resource-vpc"
    vpc_cidr = "10.80.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      toset(viettelcloud_private_ip.private_ip.allowed_vips[*].id) == toset([viettelcloud_private_ip.vip.id]) &&
      length(viettelcloud_private_ip.private_ip.attached_vips) == 1 &&
      tolist(viettelcloud_private_ip.private_ip.attached_vips)[0].id == viettelcloud_private_ip.vip.id
    )
    error_message = "Omitting allowed VIPs changed the backend set or replaced the private IP."
  }
}

# Omitted allowed VIPs keep their prior set in the next plan. Any planned change
# makes attached_vips unknown, so this plan-only run fails on a perpetual diff.
run "plan_allowedVipsOmitted_stateStable" {
  command = plan

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region   = "vn-central-1"
    vpc_name = "tf-test-private-ip-resource-vpc"
    vpc_cidr = "10.80.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      toset(viettelcloud_private_ip.private_ip.allowed_vips[*].id) == toset([viettelcloud_private_ip.vip.id]) &&
      length(viettelcloud_private_ip.private_ip.attached_vips) == 1
    )
    error_message = "Re-planning with allowed VIPs omitted proposed a change."
  }
}

# An explicit empty set clears every allowed VIP in place.
run "clearAllowedVips_attachedVips_attachmentsEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region            = "vn-central-1"
    vpc_name          = "tf-test-private-ip-resource-vpc"
    vpc_cidr          = "10.80.0.0/16"
    allowed_vips_mode = "empty"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      try(length(viettelcloud_private_ip.private_ip.allowed_vips), 0) == 0 &&
      try(length(viettelcloud_private_ip.private_ip.attached_vips), 0) == 0
    )
    error_message = "Explicitly clearing allowed VIPs replaced the private IP or retained an attachment."
  }
}

# Description and allowed VIPs use separate APIs and update together without replacement.
run "updateDescriptionAndVips_clearedVips_descriptionAndVipsUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                 = "vn-central-1"
    vpc_name               = "tf-test-private-ip-resource-vpc"
    vpc_cidr               = "10.80.0.0/16"
    private_ip_description = ""
    allowed_vips_mode      = "fixture"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      try(length(viettelcloud_private_ip.private_ip.description), 0) == 0 &&
      toset(viettelcloud_private_ip.private_ip.allowed_vips[*].id) == toset([viettelcloud_private_ip.vip.id]) &&
      try(length(viettelcloud_private_ip.private_ip.attached_vips), 0) == 1 &&
      tolist(viettelcloud_private_ip.private_ip.attached_vips)[0].id == viettelcloud_private_ip.vip.id &&
      tolist(viettelcloud_private_ip.private_ip.attached_vips)[0].ip_address == viettelcloud_private_ip.vip.ip_address
    )
    error_message = "Description and allowed VIPs were not updated in place with complete attachment metadata."
  }
}

run "clearAllowedVips_updatedVips_attachmentsEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                 = "vn-central-1"
    vpc_name               = "tf-test-private-ip-resource-vpc"
    vpc_cidr               = "10.80.0.0/16"
    private_ip_description = ""
    allowed_vips_mode      = "empty"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      try(length(viettelcloud_private_ip.private_ip.allowed_vips), 0) == 0 &&
      try(length(viettelcloud_private_ip.private_ip.attached_vips), 0) == 0
    )
    error_message = "Allowed VIPs were not cleared after the combined update."
  }
}

# Description is mutable and must update without replacing the private IP.
run "updateDescription_existingPrivateIp_descriptionUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                 = "vn-central-1"
    vpc_name               = "tf-test-private-ip-resource-vpc"
    vpc_cidr               = "10.80.0.0/16"
    private_ip_description = "Private IP after update"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      viettelcloud_private_ip.private_ip.description == "Private IP after update"
    )
    error_message = "Description was not updated in place."
  }
}

# Removing description from configuration preserves the backend value.
run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region   = "vn-central-1"
    vpc_name = "tf-test-private-ip-resource-vpc"
    vpc_cidr = "10.80.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      viettelcloud_private_ip.private_ip.description == "Private IP after update"
    )
    error_message = "Omitting description changed the backend value or replaced the private IP."
  }
}

# An explicit empty description clears the value in place.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                 = "vn-central-1"
    vpc_name               = "tf-test-private-ip-resource-vpc"
    vpc_cidr               = "10.80.0.0/16"
    private_ip_description = ""
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      try(length(viettelcloud_private_ip.private_ip.description), 0) == 0
    )
    error_message = "Explicitly clearing description replaced the private IP or retained the old value."
  }
}

# Selecting a static address is immutable and replaces the private IP.
run "configureIpAddress_allocatedPrivateIp_privateIpReplaced" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                 = "vn-central-1"
    vpc_name               = "tf-test-private-ip-resource-vpc"
    vpc_cidr               = "10.80.0.0/16"
    private_ip_description = ""
    private_ip_address     = "10.80.1.100"
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id != run.createPrivateIp_ipAndMacOmitted_addressesAllocatedAndVipsConfigured.private_ip_id &&
      viettelcloud_private_ip.private_ip.ip_address == "10.80.1.100"
    )
    error_message = "Changing the configured IP address did not replace the private IP."
  }
}

# Selecting a static MAC is immutable and replaces the private IP.
run "configureMacAddress_existingPrivateIp_privateIpReplaced" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                        = "vn-central-1"
    vpc_name                      = "tf-test-private-ip-resource-vpc"
    vpc_cidr                      = "10.80.0.0/16"
    private_ip_description        = ""
    private_ip_address            = "10.80.1.100"
    private_ip_mac_address        = "02:ab:cd:00:80:10"
    use_uppercase_representations = true
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id != run.configureIpAddress_allocatedPrivateIp_privateIpReplaced.private_ip_id &&
      viettelcloud_private_ip.private_ip.subnet.id == upper(viettelcloud_subnet.subnet[0].id) &&
      viettelcloud_private_ip.private_ip.mac_address == "02:AB:CD:00:80:10"
    )
    error_message = "Changing the configured MAC address did not replace the private IP."
  }
}

# Semantically equivalent UUID and MAC representations must not cause plan flapping.
run "plan_equivalentSubnetIdAndMacAddress_stateStable" {
  command = plan

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                        = "vn-central-1"
    vpc_name                      = "tf-test-private-ip-resource-vpc"
    vpc_cidr                      = "10.80.0.0/16"
    private_ip_description        = ""
    private_ip_address            = "10.80.1.100"
    private_ip_mac_address        = "02:ab:cd:00:80:10"
    use_uppercase_representations = true
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.configureMacAddress_existingPrivateIp_privateIpReplaced.private_ip_id &&
      viettelcloud_private_ip.private_ip.subnet.id == upper(viettelcloud_subnet.subnet[0].id) &&
      viettelcloud_private_ip.private_ip.mac_address == "02:AB:CD:00:80:10"
    )
    error_message = "Equivalent UUID or MAC representations caused drift."
  }
}

# Moving to another existing subnet replaces the private IP and refreshes all parent fields.
run "changeSubnet_existingPrivateIp_privateIpReplacedAndParentsRefreshed" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                        = "vn-central-1"
    vpc_name                      = "tf-test-private-ip-resource-vpc"
    vpc_cidr                      = "10.80.0.0/16"
    subnet_index                  = 1
    private_ip_description        = ""
    use_uppercase_representations = true
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id != run.configureMacAddress_existingPrivateIp_privateIpReplaced.private_ip_id &&
      viettelcloud_private_ip.private_ip.subnet.id == upper(viettelcloud_subnet.subnet[1].id) &&
      viettelcloud_private_ip.private_ip.subnet_name == "tf-test-private-ip-subnet-b" &&
      viettelcloud_private_ip.private_ip.subnet_cidr == "10.80.2.0/24" &&
      viettelcloud_private_ip.private_ip.vpc_id == viettelcloud_vpc.vpc.id &&
      viettelcloud_private_ip.private_ip.vpc_name == "tf-test-private-ip-resource-vpc" &&
      viettelcloud_private_ip.private_ip.region == "vn-central-1"
    )
    error_message = "Changing subnet did not replace the private IP and refresh its parent references."
  }
}

# Leave an allowed VIP attached so teardown also exercises deletion with the relation present.
run "attachVipWithNormalizedId_existingPrivateIp_vipIdAndAddressPreserved" {
  command = apply

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                        = "vn-central-1"
    vpc_name                      = "tf-test-private-ip-resource-vpc"
    vpc_cidr                      = "10.80.0.0/16"
    subnet_index                  = 1
    private_ip_description        = ""
    allowed_vips_mode             = "fixture"
    use_uppercase_representations = true
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.changeSubnet_existingPrivateIp_privateIpReplacedAndParentsRefreshed.private_ip_id &&
      toset(viettelcloud_private_ip.private_ip.allowed_vips[*].id) == toset([upper(viettelcloud_private_ip.vip.id)]) &&
      try(length(viettelcloud_private_ip.private_ip.attached_vips), 0) == 1 &&
      tolist(viettelcloud_private_ip.private_ip.attached_vips)[0].ip_address == viettelcloud_private_ip.vip.ip_address
    )
    error_message = "Allowed VIP normalization changed identity or lost attachment metadata."
  }
}

run "plan_equivalentSubnetAndVipIds_stateStable" {
  command = plan

  module {
    source = "./fixtures/private_ip/resource"
  }

  variables {
    region                        = "vn-central-1"
    vpc_name                      = "tf-test-private-ip-resource-vpc"
    vpc_cidr                      = "10.80.0.0/16"
    subnet_index                  = 1
    private_ip_description        = ""
    allowed_vips_mode             = "fixture"
    use_uppercase_representations = true
  }

  assert {
    condition = (
      viettelcloud_private_ip.private_ip.id == run.changeSubnet_existingPrivateIp_privateIpReplacedAndParentsRefreshed.private_ip_id &&
      viettelcloud_private_ip.private_ip.subnet.id == upper(viettelcloud_subnet.subnet[1].id) &&
      toset(viettelcloud_private_ip.private_ip.allowed_vips[*].id) == toset([upper(viettelcloud_private_ip.vip.id)])
    )
    error_message = "Equivalent subnet or allowed VIP UUID representations caused drift."
  }
}
