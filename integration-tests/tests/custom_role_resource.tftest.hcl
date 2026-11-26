# Test index. Copy a run declaration to find its block.
# Chains and runs are listed in file order.
#
# Every chain creates its own Custom Role with a display name that no other
# chain uses, so the chains run in parallel. The backend derives the role name
# from the display name and rejects a name that already exists in the project.
#
# Chain lifecycle (state_key = "lifecycle"):
#   run "createCustomRole_requiredOnly_roleCreated" {
#   run "planAgain_requiredOnly_noChanges" {
#   run "changeDisplayNameDescriptionEnabledAndActions_existingRole_updatedInPlace" {
#   run "omitDescriptionAndEnabled_existingRole_descriptionKeptEnabledTrue" {
#   run "clearDescriptionAndActions_existingRole_cleared" {
#
# Chain configured (state_key = "configured"; creates a role with every
# optional attribute set):
#   run "createCustomRole_descriptionAndDisabled_stateMatchesConfiguration" {
#
# Chain vietnamese (state_key = "vietnamese"):
#   run "createCustomRole_vietnameseDisplayName_asciiName" {
#
# Chain untrimmed (state_key = "untrimmed" -> "untrimmed_lookup"; the lookup
# reads the backend values of the role that this chain creates):
#   run "createCustomRole_untrimmedDisplayNameAndDescription_configuredValuePreserved" {
#   run "planAgain_untrimmedDisplayNameAndDescription_noChanges" {
#   run "lookupCustomRole_untrimmedRole_backendValuesTrimmed" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of
# the Custom Role this chain creates for itself):
#   run "createCustomRole_importChainFixture_roleCreated" {
#   run "importCustomRole_existingRole_noChanges" {
#
# Teardown deletes every Custom Role that the chains created, which covers
# destroy of a role that no member holds.
#
# Not automated:
# - Replacement: no attribute requires replacement.
# - A role deleted outside Terraform leaves state on refresh. A run cannot
#   delete the role that another state owns; a unit test covers it.
# - Backend errors: a display name without a letter or digit, a duplicate
#   name, an unknown action URN, and deleting a role that a member holds.
#   These are provider diagnostics, which expect_failures cannot catch; unit
#   tests cover them.

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

variable "action_urn" {
  description = "URN of an IAM action that is allowed on projects."
  type        = string
}

variable "second_action_urn" {
  description = "URN of a second IAM action that is allowed on projects."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

# Creates a role from display_name and actions only. The backend derives name
# from display_name, enables the role, and stores an empty description.
run "createCustomRole_requiredOnly_roleCreated" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lifecycle"
    action_urns  = [var.action_urn]
  }

  assert {
    condition = (
      length(viettelcloud_project_custom_role.this.id) > 0 &&
      viettelcloud_project_custom_role.this.name == "tf-test-cr-lifecycle" &&
      viettelcloud_project_custom_role.this.display_name == "TF test CR lifecycle" &&
      viettelcloud_project_custom_role.this.description == "" &&
      viettelcloud_project_custom_role.this.enabled == true &&
      endswith(viettelcloud_project_custom_role.this.urn, ":tf-test-cr-lifecycle") &&
      startswith(viettelcloud_project_custom_role.this.urn, "urn:role:prj-") &&
      tolist([for action in viettelcloud_project_custom_role.this.actions : action.urn]) == tolist([var.action_urn]) &&
      length(viettelcloud_project_custom_role.this.created_at) > 0 &&
      length(viettelcloud_project_custom_role.this.updated_at) > 0
    )
    error_message = "The Custom Role was not created with the derived name, the defaults, and every computed attribute."
  }
}

# Planning the same configuration again proposes no change. A planned change
# would leave updated_at unknown, and this assertion would then fail.
run "planAgain_requiredOnly_noChanges" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lifecycle"
    action_urns  = [var.action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.id == run.createCustomRole_requiredOnly_roleCreated.custom_role_id &&
      viettelcloud_project_custom_role.this.updated_at == run.createCustomRole_requiredOnly_roleCreated.updated_at
    )
    error_message = "Planning the unchanged Custom Role proposed a change."
  }
}

# Changes every mutable attribute in place: display_name, description,
# enabled, and the action set, which gains one action and loses the other.
# id, name, and urn stay the same.
run "changeDisplayNameDescriptionEnabledAndActions_existingRole_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lifecycle renamed"
    description  = "Custom Role after update"
    enabled      = false
    action_urns  = [var.second_action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.id == run.createCustomRole_requiredOnly_roleCreated.custom_role_id &&
      viettelcloud_project_custom_role.this.name == run.createCustomRole_requiredOnly_roleCreated.name &&
      viettelcloud_project_custom_role.this.urn == run.createCustomRole_requiredOnly_roleCreated.urn &&
      viettelcloud_project_custom_role.this.display_name == "TF test CR lifecycle renamed" &&
      viettelcloud_project_custom_role.this.description == "Custom Role after update" &&
      viettelcloud_project_custom_role.this.enabled == false &&
      tolist([for action in viettelcloud_project_custom_role.this.actions : action.urn]) == tolist([var.second_action_urn])
    )
    error_message = "The Custom Role was replaced, changed its name or URN, or did not take the new values."
  }
}

# Omitting description keeps the backend value. Omitting enabled applies the
# default, so the role is enabled again.
run "omitDescriptionAndEnabled_existingRole_descriptionKeptEnabledTrue" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lifecycle renamed"
    action_urns  = [var.second_action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.id == run.createCustomRole_requiredOnly_roleCreated.custom_role_id &&
      viettelcloud_project_custom_role.this.description == "Custom Role after update" &&
      viettelcloud_project_custom_role.this.enabled == true
    )
    error_message = "Omitting description did not keep it, or omitting enabled did not enable the role."
  }
}

# An empty description and an empty action set clear both in place.
run "clearDescriptionAndActions_existingRole_cleared" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lifecycle renamed"
    description  = ""
    action_urns  = []
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.id == run.createCustomRole_requiredOnly_roleCreated.custom_role_id &&
      viettelcloud_project_custom_role.this.description == "" &&
      length(viettelcloud_project_custom_role.this.actions) == 0
    )
    error_message = "The description or the action set was not cleared in place."
  }
}


# Chain configured.

# Creates a role with a description and enabled = false, so state and backend
# carry the configured values instead of the defaults.
run "createCustomRole_descriptionAndDisabled_stateMatchesConfiguration" {
  command   = apply
  parallel  = true
  state_key = "configured"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR configured"
    description  = "Custom Role with every optional attribute"
    enabled      = false
    action_urns  = [var.action_urn, var.second_action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.name == "tf-test-cr-configured" &&
      viettelcloud_project_custom_role.this.description == "Custom Role with every optional attribute" &&
      viettelcloud_project_custom_role.this.enabled == false &&
      sort([for action in viettelcloud_project_custom_role.this.actions : action.urn]) == sort([var.action_urn, var.second_action_urn])
    )
    error_message = "The Custom Role did not keep the configured description, enabled flag, or actions."
  }
}


# Chain vietnamese.

# The backend transliterates a Vietnamese display name, including the letter
# đ, into an ASCII name.
run "createCustomRole_vietnameseDisplayName_asciiName" {
  command   = apply
  parallel  = true
  state_key = "vietnamese"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "Đội kiểm thử TF test CR"
    action_urns  = [var.action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.display_name == "Đội kiểm thử TF test CR" &&
      viettelcloud_project_custom_role.this.name == "doi-kiem-thu-tf-test-cr" &&
      endswith(viettelcloud_project_custom_role.this.urn, ":doi-kiem-thu-tf-test-cr")
    )
    error_message = "The Vietnamese display name did not produce the expected ASCII name."
  }
}


# Chain untrimmed.

# The backend strips outer whitespace from display_name and description. State
# keeps the configured values, and the name comes from the trimmed display name.
run "createCustomRole_untrimmedDisplayNameAndDescription_configuredValuePreserved" {
  command   = apply
  parallel  = true
  state_key = "untrimmed"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "  TF test CR untrimmed  "
    description  = "  Custom Role with padded description  "
    action_urns  = [var.action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.display_name == "  TF test CR untrimmed  " &&
      viettelcloud_project_custom_role.this.description == "  Custom Role with padded description  " &&
      viettelcloud_project_custom_role.this.name == "tf-test-cr-untrimmed"
    )
    error_message = "State did not keep the configured display_name and description, or the name was not derived from the trimmed display name."
  }
}

# The configured values differ from the backend values only by outer
# whitespace, so planning them again proposes no change. A planned change
# would leave updated_at unknown, and this assertion would then fail.
run "planAgain_untrimmedDisplayNameAndDescription_noChanges" {
  command   = plan
  parallel  = true
  state_key = "untrimmed"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "  TF test CR untrimmed  "
    description  = "  Custom Role with padded description  "
    action_urns  = [var.action_urn]
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.this.id == run.createCustomRole_untrimmedDisplayNameAndDescription_configuredValuePreserved.custom_role_id &&
      viettelcloud_project_custom_role.this.updated_at == run.createCustomRole_untrimmedDisplayNameAndDescription_configuredValuePreserved.updated_at
    )
    error_message = "Planning the configured values with outer whitespace proposed a change."
  }
}

# The data source reports what the backend stored, which shows that the
# backend trimmed both values. It uses its own state_key, so it reads the role
# without owning it.
run "lookupCustomRole_untrimmedRole_backendValuesTrimmed" {
  command   = apply
  parallel  = true
  state_key = "untrimmed_lookup"

  module {
    source = "./fixtures/custom_role/datasource"
  }

  variables {
    id = run.createCustomRole_untrimmedDisplayNameAndDescription_configuredValuePreserved.custom_role_id
  }

  assert {
    condition = (
      data.viettelcloud_project_custom_role.this.display_name == "TF test CR untrimmed" &&
      data.viettelcloud_project_custom_role.this.description == "Custom Role with padded description"
    )
    error_message = "The backend did not store display_name and description without their outer whitespace."
  }
}


# Chain import.

# The import chain creates the role it later imports, so no other chain has to
# hand it over. The description is non-empty: the import fixture leaves it
# unconfigured, so the value the plan reports can only come from the import
# refresh.
run "createCustomRole_importChainFixture_roleCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR import"
    description  = "Custom Role for import"
    action_urns  = [var.action_urn, var.second_action_urn]
  }

  assert {
    condition = (
      length(viettelcloud_project_custom_role.this.id) > 0 &&
      viettelcloud_project_custom_role.this.description == "Custom Role for import" &&
      viettelcloud_project_custom_role.this.enabled == true
    )
    error_message = "The import chain's Custom Role was not created with the expected values."
  }
}

# Importing the role by ID restores its state and plans no change. The run
# stays plan-only under its own state key, so the role keeps one owner.
# display_name and actions come from the creating run's state. The assertions
# check the attributes that only the import refresh supplies; any planned
# change would leave updated_at unknown and fail this run.
run "importCustomRole_existingRole_noChanges" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/custom_role/import"
  }

  variables {
    custom_role_id = run.createCustomRole_importChainFixture_roleCreated.custom_role_id
    display_name   = run.createCustomRole_importChainFixture_roleCreated.display_name
    action_urns    = run.createCustomRole_importChainFixture_roleCreated.action_urns
  }

  assert {
    condition = (
      viettelcloud_project_custom_role.imported.id == run.createCustomRole_importChainFixture_roleCreated.custom_role_id &&
      viettelcloud_project_custom_role.imported.name == run.createCustomRole_importChainFixture_roleCreated.name &&
      viettelcloud_project_custom_role.imported.description == run.createCustomRole_importChainFixture_roleCreated.description &&
      viettelcloud_project_custom_role.imported.enabled == run.createCustomRole_importChainFixture_roleCreated.enabled &&
      viettelcloud_project_custom_role.imported.urn == run.createCustomRole_importChainFixture_roleCreated.urn &&
      viettelcloud_project_custom_role.imported.created_at == run.createCustomRole_importChainFixture_roleCreated.created_at &&
      viettelcloud_project_custom_role.imported.updated_at == run.createCustomRole_importChainFixture_roleCreated.updated_at
    )
    error_message = "Importing the Custom Role did not restore the state the backend reports, or it planned a change."
  }
}
