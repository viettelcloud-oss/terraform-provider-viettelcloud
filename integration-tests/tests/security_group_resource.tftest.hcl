# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (state_key = "lifecycle"; one security group patched in place
# for the whole chain, so every run compares its ID against the create run):
#   run "createSecurityGroup_allAttributesConfigured_completeStateReturned" {
#   run "planAgain_allAttributesConfigured_stateStable" {
#   run "changeDescription_existingSecurityGroup_updatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "changeName_existingSecurityGroup_updatedInPlace" {
#
# Chain omitted_description (state_key = "omitted_description"; creates its own
# security group with description omitted from the create request):
#   run "createSecurityGroup_descriptionOmitted_emptyDescriptionReturned" {
#   run "planAgain_descriptionOmitted_stateStable" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# security group this chain creates for itself):
#   run "createSecurityGroup_importChainFixture_securityGroupCreated" {
#   run "planImport_existingSecurityGroupId_completeStateRestored" {
#
# Replacement on region is not covered. region is the only attribute that forces
# replacement, and every fixture in this directory targets vn-central-1, so the
# test project is not known to have a second region to move a group into.

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

run "createSecurityGroup_allAttributesConfigured_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name        = "tf-int-sg-lifecycle"
    description = "Terraform integration test"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_security_group.security_group.id) > 0 &&
      viettelcloud_security_group.security_group.name == "tf-int-sg-lifecycle" &&
      viettelcloud_security_group.security_group.description == "Terraform integration test" &&
      viettelcloud_security_group.security_group.region.name == "vn-central-1" &&
      length(viettelcloud_security_group.security_group.region_id) > 0 &&
      length(viettelcloud_security_group.security_group.project_id) > 0 &&
      length(viettelcloud_security_group.security_group.display_name) > 0 &&
      !viettelcloud_security_group.security_group.is_default &&
      length(viettelcloud_security_group.security_group.created_at) > 0 &&
      length(viettelcloud_security_group.security_group.updated_at) > 0
    )
    error_message = "Security group resource did not expose the expected values."
  }
}

run "planAgain_allAttributesConfigured_stateStable" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name        = "tf-int-sg-lifecycle"
    description = "Terraform integration test"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_security_group.security_group.id == run.createSecurityGroup_allAttributesConfigured_completeStateReturned.security_group_id &&
      viettelcloud_security_group.security_group.name == "tf-int-sg-lifecycle" &&
      viettelcloud_security_group.security_group.description == "Terraform integration test" &&
      viettelcloud_security_group.security_group.region.name == "vn-central-1"
    )
    error_message = "Security group state was not preserved during refresh."
  }
}

run "changeDescription_existingSecurityGroup_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name        = "tf-int-sg-lifecycle"
    description = "Terraform integration test updated"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_security_group.security_group.id == run.createSecurityGroup_allAttributesConfigured_completeStateReturned.security_group_id &&
      viettelcloud_security_group.security_group.description == "Terraform integration test updated"
    )
    error_message = "The security group description was not updated in place."
  }
}

# Dropping description from configuration preserves the backend value, because
# the attribute is optional and computed and the patch omits what it does not
# send.
run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name   = "tf-int-sg-lifecycle"
    region = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_security_group.security_group.id == run.createSecurityGroup_allAttributesConfigured_completeStateReturned.security_group_id &&
      viettelcloud_security_group.security_group.description == "Terraform integration test updated"
    )
    error_message = "Omitting description changed the security group description."
  }
}

# An explicit empty string clears the description, which is the contract that
# omitting it does not express.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name        = "tf-int-sg-lifecycle"
    description = ""
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_security_group.security_group.id == run.createSecurityGroup_allAttributesConfigured_completeStateReturned.security_group_id &&
      viettelcloud_security_group.security_group.description == ""
    )
    error_message = "The security group description was not cleared in place."
  }
}

# The name attribute carries no RequiresReplace modifier, so the ID must
# survive the rename.
run "changeName_existingSecurityGroup_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name        = "tf-int-sg-lifecycle-renamed"
    description = ""
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_security_group.security_group.id == run.createSecurityGroup_allAttributesConfigured_completeStateReturned.security_group_id &&
      viettelcloud_security_group.security_group.name == "tf-int-sg-lifecycle-renamed" &&
      viettelcloud_security_group.security_group.display_name == "tf-int-sg-lifecycle-renamed"
    )
    error_message = "Renaming the security group replaced it or did not take effect."
  }
}


# Chain omitted_description.

# Creating a group with description omitted is a different contract from
# omitting it after it was configured: this chain proves the create request left
# the field out, and it pins what the backend puts there instead. Confirmed
# against the real API: an omitted description comes back as "", not null.
run "createSecurityGroup_descriptionOmitted_emptyDescriptionReturned" {
  command   = apply
  parallel  = true
  state_key = "omitted_description"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name   = "tf-int-sg-omitted-description"
    region = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_security_group.security_group.id) > 0 &&
      viettelcloud_security_group.security_group.name == "tf-int-sg-omitted-description" &&
      viettelcloud_security_group.security_group.description == "" &&
      viettelcloud_security_group.security_group.region.name == "vn-central-1" &&
      !viettelcloud_security_group.security_group.is_default
    )
    error_message = "Creating a security group without a description did not leave the backend's empty description in state."
  }
}

# The empty description the backend substituted must survive a replan against a
# configuration that still omits it. Otherwise every plan would show a perpetual
# diff on an attribute the practitioner never configured.
run "planAgain_descriptionOmitted_stateStable" {
  command   = plan
  parallel  = true
  state_key = "omitted_description"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name   = "tf-int-sg-omitted-description"
    region = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_security_group.security_group.id == run.createSecurityGroup_descriptionOmitted_emptyDescriptionReturned.security_group_id &&
      viettelcloud_security_group.security_group.description == ""
    )
    error_message = "An omitted description drifted or the group was replaced on replan."
  }
}


# Chain import.

# The import chain creates the security group it later imports so no other chain
# has to hand it over. The description is deliberately non-empty: the import
# fixture leaves description unconfigured, so the value the plan reports can
# only come from the import refresh.
run "createSecurityGroup_importChainFixture_securityGroupCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/security_group/resource"
  }

  variables {
    name        = "tf-int-sg-import"
    description = "Security group for import"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_security_group.security_group.id) > 0 &&
      viettelcloud_security_group.security_group.name == "tf-int-sg-import" &&
      viettelcloud_security_group.security_group.description == "Security group for import"
    )
    error_message = "The import chain's security group was not created with the expected values."
  }
}

# Plan-only import under a separate state key to prevent double deletion during
# teardown. Assertions cover the unconfigured description and computed fields
# that only the backend refresh supplies.
run "planImport_existingSecurityGroupId_completeStateRestored" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/security_group/import"
  }

  variables {
    security_group_id = run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_id
    name              = run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_name
    region            = run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_region
  }

  assert {
    condition = (
      viettelcloud_security_group.imported.id == run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_id &&
      viettelcloud_security_group.imported.description == run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_description &&
      viettelcloud_security_group.imported.display_name == run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_display_name &&
      viettelcloud_security_group.imported.region_id == run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_region_id &&
      viettelcloud_security_group.imported.is_default == run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_is_default &&
      !viettelcloud_security_group.imported.is_default &&
      viettelcloud_security_group.imported.created_at == run.createSecurityGroup_importChainFixture_securityGroupCreated.security_group_created_at &&
      length(viettelcloud_security_group.imported.updated_at) > 0
    )
    error_message = "Importing the security group did not restore the state the backend reports, or it planned a change."
  }
}
