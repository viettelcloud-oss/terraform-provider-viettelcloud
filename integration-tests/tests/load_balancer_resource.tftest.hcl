# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Load Balancer quota: at most three Load Balancers exist at once. Each of the
# chains mutable_fields, association_uuid, and import owns one Load Balancer,
# so the three chains can run together. A public IP has no separate quota: it
# comes with its Load Balancer, at most one each.
#
# The suite reads an existing network instead of creating one, so it costs no
# VPC or subnet quota. Set vpc_id, subnet_id, and replacement_vpc_id, for
# example as TF_VAR_vpc_id, TF_VAR_subnet_id, and TF_VAR_replacement_vpc_id.
#
# Chain network (shared lookup that creates nothing; state_key = "network"):
#   run "lookupNetwork_existingVpcsAndSubnet_networkResolved" {
#
# Chain mutable_fields (state_key = "mutable_fields"):
#   run "createLoadBalancer_requiredValuesOnly_completeStateReturned" {
#   run "changeMutableFields_existingLoadBalancer_updatedInPlace" {
#   run "omitOptionalFields_loadBalancerWithConfiguredValues_valuesPreservedInPlace" {
#   run "clearDescriptionAndSetUntrimmedName_loadBalancerWithOptionalValues_emptyDescriptionAndNamePreserved" {
#   run "planAgain_loadBalancerWithNormalizedName_stateStable" {
#   run "enablePublicIp_existingLoadBalancer_publicAddressAssigned" {
#   run "padPublicIpMode_publicIpEnabled_spellingPreservedWithoutChange" {
#   run "planAgain_paddedPublicIpMode_stateStable" {
#   run "omitPublicIpMode_publicIpEnabled_publicAddressRemovedInPlace" {
#   run "planAgain_publicIpModeOmitted_stateStable" {
#   run "changePackage_existingLoadBalancer_idPreserved" {
#   run "changeVpc_sameRegion_loadBalancerReplaced" {
#   run "replaceLoadBalancer_paddedPublicIpMode_createdWithTrimmedMode" {
#
# Chain association_uuid (state_key = "association_uuid"):
#   run "createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved" {
#   run "planAgain_uppercasePrivateIpId_stateStable" {
#   run "omitPrivateIpId_existingAssociation_backendValuePreservedInPlace" {
#   run "planAgain_omittedPrivateIp_stateStable" {
#   run "configurePrivateIp_associatedPrivateIp_referenceAddedInPlace" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import):
#   run "createLoadBalancer_importFixture_loadBalancerCreated" {
#   run "planImport_existingLoadBalancerId_completeStateRestored" {

variable "api_endpoint" {
  type = string
}

variable "token" {
  type      = string
  sensitive = true
}

variable "project_id" {
  type = string
}

variable "vpc_id" {
  type        = string
  description = "Existing VPC in vn-central-1 that holds subnet_id. Every chain creates its Load Balancer here."
}

variable "subnet_id" {
  type        = string
  description = "Existing subnet of vpc_id with a free address for the Private IP of chain association_uuid."
}

variable "replacement_vpc_id" {
  type        = string
  description = "Second existing VPC in vn-central-1, other than vpc_id, with a subnet. Run changeVpc_sameRegion_loadBalancerReplaced moves its Load Balancer here."
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Reads the existing VPC and subnet that every chain uses, and the second VPC
# that run "changeVpc_sameRegion_loadBalancerReplaced" moves its Load Balancer
# to. The run creates nothing, so teardown leaves the network in place.
run "lookupNetwork_existingVpcsAndSubnet_networkResolved" {
  command   = apply
  state_key = "network"

  module {
    source = "./fixtures/load_balancer/network"
  }

  variables {
    vpc_id             = var.vpc_id
    subnet_id          = var.subnet_id
    replacement_vpc_id = var.replacement_vpc_id
  }

  assert {
    condition = (
      lower(data.viettelcloud_subnet.shared.vpc_id) == lower(data.viettelcloud_vpc.shared.id) &&
      lower(data.viettelcloud_vpc.replacement.id) != lower(data.viettelcloud_vpc.shared.id) &&
      data.viettelcloud_vpc.shared.region == "vn-central-1" &&
      data.viettelcloud_vpc.replacement.region == "vn-central-1"
    )
    error_message = "vpc_id must hold subnet_id, and replacement_vpc_id must be a second VPC in vn-central-1."
  }
}

# Chain mutable_fields.

# Creates a Load Balancer with the required attributes and verifies the
# complete resource state, including the VPC association and computed fields.
# The type is omitted, so the backend default "appliance" must reach state.
# public_ip_mode is omitted, so the Load Balancer must have no public address.
run "createLoadBalancer_requiredValuesOnly_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "tf-test-lb-mutable-before"
    description = null
    package     = "MEDIUM"
    topology    = "SINGLE"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      length(viettelcloud_lb_loadbalancer.load_balancer.id) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.name == "tf-test-lb-mutable-before" &&
      viettelcloud_lb_loadbalancer.load_balancer.package == "MEDIUM" &&
      viettelcloud_lb_loadbalancer.load_balancer.topology == "SINGLE" &&
      viettelcloud_lb_loadbalancer.load_balancer.region.name == "  vn-central-1  " &&
      length(viettelcloud_lb_loadbalancer.load_balancer.region_id) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc.id == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc_name == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_name &&
      length(viettelcloud_lb_loadbalancer.load_balancer.private_ip_id) > 0 &&
      length(viettelcloud_lb_loadbalancer.load_balancer.vip_address) > 0 &&
      length(viettelcloud_lb_loadbalancer.load_balancer.status) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE" &&
      viettelcloud_lb_loadbalancer.load_balancer.type == "appliance" &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == null &&
      viettelcloud_lb_loadbalancer.load_balancer.public_address == null &&
      viettelcloud_lb_loadbalancer.load_balancer.access_log_enabled != null &&
      length(viettelcloud_lb_loadbalancer.load_balancer.created_at) > 0 &&
      length(viettelcloud_lb_loadbalancer.load_balancer.updated_at) > 0
    )
    error_message = "Load Balancer was not created with the expected values."
  }
}

# Updates name, description, package, and topology on the existing Load
# Balancer and verifies they change in place without replacement. Configuring
# the backend type "appliance" after its omission keeps the Load Balancer.
run "changeMutableFields_existingLoadBalancer_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "tf-test-lb-mutable-after"
    description = "Load Balancer after update"
    package     = "MEDIUM"
    topology    = "ACTIVE_STANDBY"
    type        = "appliance"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.name == "tf-test-lb-mutable-after" &&
      viettelcloud_lb_loadbalancer.load_balancer.description == "Load Balancer after update" &&
      viettelcloud_lb_loadbalancer.load_balancer.package == "MEDIUM" &&
      viettelcloud_lb_loadbalancer.load_balancer.topology == "ACTIVE_STANDBY" &&
      viettelcloud_lb_loadbalancer.load_balancer.type == "appliance" &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "Mutable Load Balancer fields were not updated in place."
  }
}

# Omitting description and type preserves their backend values without
# replacing the Load Balancer.
run "omitOptionalFields_loadBalancerWithConfiguredValues_valuesPreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "tf-test-lb-mutable-after"
    description = null
    package     = "MEDIUM"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.description == "Load Balancer after update" &&
      viettelcloud_lb_loadbalancer.load_balancer.type == "appliance"
    )
    error_message = "Omitting optional values unexpectedly changed or replaced the Load Balancer."
  }
}

# An explicit empty description clears it without replacement. An untrimmed
# name is preserved in state as configured; the provider trims before the
# request so the backend never receives the padding.
run "clearDescriptionAndSetUntrimmedName_loadBalancerWithOptionalValues_emptyDescriptionAndNamePreserved" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "  tf-test-lb-mutable-after  "
    description = ""
    package     = "MEDIUM"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.name == "  tf-test-lb-mutable-after  " &&
      viettelcloud_lb_loadbalancer.load_balancer.description == ""
    )
    error_message = "Load Balancer clearing or name normalization did not converge in place."
  }
}

# Replanning the untrimmed name must remain stable and produce no drift.
run "planAgain_loadBalancerWithNormalizedName_stateStable" {
  command   = plan
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "  tf-test-lb-mutable-after  "
    description = ""
    package     = "MEDIUM"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition     = viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id
    error_message = "A normalized Load Balancer name produced drift."
  }
}

# Configuring public_ip_mode "ipv4-only" on the existing Load Balancer assigns
# a public IPv4 address in place. Read derives the mode from that address, so
# the derived mode must equal the configured one.
run "enablePublicIp_existingLoadBalancer_publicAddressAssigned" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id         = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name           = "  tf-test-lb-mutable-after  "
    description    = ""
    package        = "MEDIUM"
    topology       = "ACTIVE_STANDBY"
    region         = "  vn-central-1  "
    public_ip_mode = "ipv4-only"
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == "ipv4-only" &&
      length(viettelcloud_lb_loadbalancer.load_balancer.public_address) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.v6_public_address == null &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "Configuring public_ip_mode ipv4-only did not assign a public IPv4 address in place."
  }
}

# public_ip_mode ignores outer whitespace, so " ipv4-only " names the mode that
# the Load Balancer already has. The apply must send no request and keep the
# public address, and state keeps the configured spelling.
run "padPublicIpMode_publicIpEnabled_spellingPreservedWithoutChange" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id         = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name           = "  tf-test-lb-mutable-after  "
    description    = ""
    package        = "MEDIUM"
    topology       = "ACTIVE_STANDBY"
    region         = "  vn-central-1  "
    public_ip_mode = " ipv4-only "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == " ipv4-only " &&
      viettelcloud_lb_loadbalancer.load_balancer.public_address == run.enablePublicIp_existingLoadBalancer_publicAddressAssigned.load_balancer_public_address &&
      viettelcloud_lb_loadbalancer.load_balancer.v6_public_address == null &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "A padded public_ip_mode changed the public address or lost the configured spelling."
  }
}

# A plan with the padded mode must be empty. An in-place update would make the
# planned updated_at unknown, so the length check cannot evaluate and fails. The
# backend can rewrite updated_at on its own, so the run never compares its value.
run "planAgain_paddedPublicIpMode_stateStable" {
  command   = plan
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id         = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name           = "  tf-test-lb-mutable-after  "
    description    = ""
    package        = "MEDIUM"
    topology       = "ACTIVE_STANDBY"
    region         = "  vn-central-1  "
    public_ip_mode = " ipv4-only "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.padPublicIpMode_publicIpEnabled_spellingPreservedWithoutChange.load_balancer_id &&
      length(viettelcloud_lb_loadbalancer.load_balancer.updated_at) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == " ipv4-only "
    )
    error_message = "A padded public_ip_mode planned a change on a Load Balancer that already has that mode."
  }
}

# Omitting public_ip_mode turns public IP off in place, unlike the omitted
# description and type, which keep their backend values.
run "omitPublicIpMode_publicIpEnabled_publicAddressRemovedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "  tf-test-lb-mutable-after  "
    description = ""
    package     = "MEDIUM"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == null &&
      viettelcloud_lb_loadbalancer.load_balancer.public_address == null &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "Omitting public_ip_mode did not release the public IPv4 address in place."
  }
}

# A plan after the omission must be empty, which proves that Read derives a
# null public_ip_mode from the missing public_address. An in-place update would
# make the planned updated_at unknown, so the length check cannot evaluate and
# fails. The backend can rewrite updated_at on its own, so the run never
# compares its value.
run "planAgain_publicIpModeOmitted_stateStable" {
  command   = plan
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "  tf-test-lb-mutable-after  "
    description = ""
    package     = "MEDIUM"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.omitPublicIpMode_publicIpEnabled_publicAddressRemovedInPlace.load_balancer_id &&
      length(viettelcloud_lb_loadbalancer.load_balancer.updated_at) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == null
    )
    error_message = "An omitted public_ip_mode planned a change after public IP was turned off."
  }
}

# Changes only package and verifies that the PATCH keeps the Load Balancer ID.
run "changePackage_existingLoadBalancer_idPreserved" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name        = "  tf-test-lb-mutable-after  "
    description = ""
    package     = "LARGE"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_requiredValuesOnly_completeStateReturned.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.package == "LARGE" &&
      viettelcloud_lb_loadbalancer.load_balancer.topology == "ACTIVE_STANDBY" &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "Changing package did not update the Load Balancer in place."
  }
}

# Moving the Load Balancer to the second VPC replaces it, so the ID changes.
# Terraform destroys the old Load Balancer before it creates the new one, so
# the suite still holds at most three Load Balancers at once.
run "changeVpc_sameRegion_loadBalancerReplaced" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id      = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.replacement_vpc_id
    name        = "  tf-test-lb-mutable-after  "
    description = ""
    package     = "LARGE"
    topology    = "ACTIVE_STANDBY"
    region      = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id != run.changePackage_existingLoadBalancer_idPreserved.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc.id == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.replacement_vpc_id &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc_name == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.replacement_vpc_name &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "Changing vpc did not replace the Load Balancer in the second VPC."
  }
}

# Moves the Load Balancer back to the first VPC with a padded public_ip_mode.
# The VPC change replaces the Load Balancer, so Create runs with the padded
# mode and assigns the public IPv4 address. State keeps the configured spelling
# and the backend derives the same mode. The destroy-then-create order keeps the
# suite at three Load Balancers at most.
run "replaceLoadBalancer_paddedPublicIpMode_createdWithTrimmedMode" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id         = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name           = "  tf-test-lb-mutable-after  "
    description    = ""
    package        = "LARGE"
    topology       = "ACTIVE_STANDBY"
    region         = "  vn-central-1  "
    public_ip_mode = " ipv4-only "
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id != run.changeVpc_sameRegion_loadBalancerReplaced.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc.id == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == " ipv4-only " &&
      length(viettelcloud_lb_loadbalancer.load_balancer.public_address) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.v6_public_address == null &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "Creating a Load Balancer with a padded public_ip_mode did not assign a public IPv4 address or lost the configured spelling."
  }
}

# Chain association_uuid.

# Creates a compatible Private IP and supplies its UUID in uppercase so the
# resource must preserve the configured representation in private_ip.id. The
# flat private_ip_id output carries the backend representation.
run "createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved" {
  command   = apply
  parallel  = true
  state_key = "association_uuid"

  module {
    source = "./fixtures/load_balancer/private_ip"
  }

  variables {
    vpc_id    = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    subnet_id = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.subnet_id
    name      = "tf-test-lb-association-uuid"
    region    = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_lb_loadbalancer.load_balancer.id) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.private_ip.id == upper(viettelcloud_private_ip.load_balancer.id) &&
      lower(viettelcloud_lb_loadbalancer.load_balancer.private_ip_id) == lower(viettelcloud_private_ip.load_balancer.id)
    )
    error_message = "Load Balancer did not preserve the configured uppercase Private IP UUID."
  }
}

# A second plan with the same uppercase association must keep the Load
# Balancer identity stable and produce no UUID normalization drift.
run "planAgain_uppercasePrivateIpId_stateStable" {
  command   = plan
  parallel  = true
  state_key = "association_uuid"

  module {
    source = "./fixtures/load_balancer/private_ip"
  }

  variables {
    vpc_id    = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    subnet_id = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.subnet_id
    name      = "tf-test-lb-association-uuid"
    region    = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.private_ip.id == run.createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved.configured_private_ip_id
    )
    error_message = "An equivalent uppercase Private IP UUID produced drift."
  }
}

# Removing the configured Private IP must retain the backend association and
# the Load Balancer ID. The private_ip reference becomes null, and the flat
# private_ip_id output keeps the backend association.
run "omitPrivateIpId_existingAssociation_backendValuePreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "association_uuid"

  module {
    source = "./fixtures/load_balancer/private_ip"
  }

  variables {
    vpc_id               = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    subnet_id            = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.subnet_id
    name                 = "tf-test-lb-association-uuid"
    region               = "vn-central-1"
    configure_private_ip = false
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.private_ip == null &&
      lower(viettelcloud_lb_loadbalancer.load_balancer.private_ip_id) == lower(run.createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved.configured_private_ip_id)
    )
    error_message = "Omitting private_ip changed the Load Balancer or its backend association."
  }
}

# A plan after the omission must be empty. An in-place update would make the
# planned updated_at unknown, and a replacement would make the planned id
# unknown, so the assertion fails in both cases.
run "planAgain_omittedPrivateIp_stateStable" {
  command   = plan
  parallel  = true
  state_key = "association_uuid"

  module {
    source = "./fixtures/load_balancer/private_ip"
  }

  variables {
    vpc_id               = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    subnet_id            = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.subnet_id
    name                 = "tf-test-lb-association-uuid"
    region               = "vn-central-1"
    configure_private_ip = false
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.omitPrivateIpId_existingAssociation_backendValuePreservedInPlace.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.updated_at == run.omitPrivateIpId_existingAssociation_backendValuePreservedInPlace.load_balancer_updated_at &&
      viettelcloud_lb_loadbalancer.load_balancer.private_ip == null &&
      viettelcloud_lb_loadbalancer.load_balancer.private_ip_id == run.omitPrivateIpId_existingAssociation_backendValuePreservedInPlace.load_balancer_private_ip_id
    )
    error_message = "An omitted private_ip planned a change after the backend kept its association."
  }
}

# Configuring the Private IP that the Load Balancer already holds only adds the
# reference to state. The Load Balancer must keep its ID.
run "configurePrivateIp_associatedPrivateIp_referenceAddedInPlace" {
  command   = apply
  parallel  = true
  state_key = "association_uuid"

  module {
    source = "./fixtures/load_balancer/private_ip"
  }

  variables {
    vpc_id    = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    subnet_id = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.subnet_id
    name      = "tf-test-lb-association-uuid"
    region    = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.load_balancer.id == run.createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved.load_balancer_id &&
      viettelcloud_lb_loadbalancer.load_balancer.private_ip.id == run.createLoadBalancer_uppercasePrivateIpId_configuredRepresentationPreserved.configured_private_ip_id
    )
    error_message = "Configuring the associated Private IP replaced the Load Balancer."
  }
}

# Chain import.

# Creates the Load Balancer that run
# "planImport_existingLoadBalancerId_completeStateRestored" will restore. The
# description is deliberately empty so the import refresh proves it reads
# the backend value. public_ip_mode "ipv4-only" covers the public IP request
# that follows the create, and gives the import a mode to derive.
run "createLoadBalancer_importFixture_loadBalancerCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/load_balancer/resource"
  }

  variables {
    vpc_id         = run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id
    name           = "tf-test-lb-import"
    description    = ""
    package        = "MEDIUM"
    topology       = "SINGLE"
    region         = "vn-central-1"
    public_ip_mode = "ipv4-only"
  }

  assert {
    condition = (
      length(viettelcloud_lb_loadbalancer.load_balancer.id) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc.id == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_id &&
      viettelcloud_lb_loadbalancer.load_balancer.vpc_name == run.lookupNetwork_existingVpcsAndSubnet_networkResolved.vpc_name &&
      viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode == "ipv4-only" &&
      length(viettelcloud_lb_loadbalancer.load_balancer.public_address) > 0 &&
      viettelcloud_lb_loadbalancer.load_balancer.provisioning_status == "ACTIVE"
    )
    error_message = "The import fixture Load Balancer was not created."
  }
}

# Importing the Load Balancer restores its complete state from the backend
# and plans no change. The configured public_ip_mode "ipv4-only" plans no
# change only when Read derives the same mode from public_address. The run
# stays plan-only with state_key "import_target" so run
# "createLoadBalancer_importFixture_loadBalancerCreated" retains sole ownership
# for teardown. Every input comes from the creating run's observed state so
# nothing is a hardcoded literal.
run "planImport_existingLoadBalancerId_completeStateRestored" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/load_balancer/import"
  }

  variables {
    load_balancer_id = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_id
    name             = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_name
    package          = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_package
    topology         = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_topology
    region           = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_region
    vpc_id           = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_vpc_id
    public_ip_mode   = run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_public_ip_mode
  }

  assert {
    condition = (
      viettelcloud_lb_loadbalancer.imported.id == run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_id &&
      viettelcloud_lb_loadbalancer.imported.name == "tf-test-lb-import" &&
      viettelcloud_lb_loadbalancer.imported.description == "" &&
      viettelcloud_lb_loadbalancer.imported.package == "MEDIUM" &&
      viettelcloud_lb_loadbalancer.imported.topology == "SINGLE" &&
      viettelcloud_lb_loadbalancer.imported.type == "appliance" &&
      viettelcloud_lb_loadbalancer.imported.access_log_enabled != null &&
      viettelcloud_lb_loadbalancer.imported.region.name == "vn-central-1" &&
      viettelcloud_lb_loadbalancer.imported.vpc.id == run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_vpc_id &&
      viettelcloud_lb_loadbalancer.imported.private_ip == null &&
      length(viettelcloud_lb_loadbalancer.imported.private_ip_id) > 0 &&
      length(viettelcloud_lb_loadbalancer.imported.vip_address) > 0 &&
      viettelcloud_lb_loadbalancer.imported.public_address == run.createLoadBalancer_importFixture_loadBalancerCreated.load_balancer_public_address &&
      viettelcloud_lb_loadbalancer.imported.v6_public_address == null &&
      viettelcloud_lb_loadbalancer.imported.provisioning_status == "ACTIVE"
    )
    error_message = "Import did not refresh the complete Load Balancer state."
  }
}
