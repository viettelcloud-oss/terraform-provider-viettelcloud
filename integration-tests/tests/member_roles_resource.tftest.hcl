# Test index. Copy a run declaration to find its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Terraform cannot add or remove project members, so every chain manages the
# roles of a member that the test project already has. Two chains that use the
# same member must not overlap, because each role set has one owner. Only the
# chain whose member no other chain uses sets parallel = true. Every other run
# is a synchronization point and runs in file order after it.
#
# Chain legacy_admin (state_key = "legacy_admin"; parallel; member
# legacy_admin_username):
#   run "createMemberRoles_legacyAdmin_noEffect" {
#
# Chain lifecycle (state_key = "lifecycle"; member member_username, then
# second_member_username after the member change). Two runs write or read
# outside that state: "lifecycle_outside" changes the role set out of band, and
# "lifecycle_previous_member" and "lifecycle_destroyed" only look members up:
#   run "createMemberRoles_predefinedRole_assigned" {
#   run "planAgain_predefinedRoleAssigned_noChanges" {
#   run "updateMemberRoles_addRoleAndDisable_inPlace" {
#   run "assignRolesOutsideTerraform_lifecycleMember_roleSetChanged" {
#   run "refreshMemberRoles_changedOutside_restored" {
#   run "omitEnabled_disabledAssignments_assignmentsEnabledInPlace" {
#   run "updateMemberRoles_changeMember_replaced" {
#   run "lookupMemberRoles_previousMember_rolesRemoved" {
#   run "destroyMemberRoles_existingAssignments_memberKept" {
#   run "lookupMemberRoles_destroyedRoleSet_memberKeptWithoutRoles" {
#
# Chain existing_roles (state_key = "existing_roles"; member member_username;
# the setup run assigns a role out of band under "existing_roles_outside"):
#   run "assignRolesOutsideTerraform_existingRolesMember_roleAssigned" {
#   run "createMemberRoles_existingRoles_replaced" {
#   run "updateMemberRoles_emptyRoles_cleared" {
#
# Chain disabled_role (state_key = "disabled_role"; member
# second_member_username):
#   run "createMemberRoles_disabledRole_assigned" {
#
# Chain import (state_key = "import" -> "import_target"; member
# member_username; plan-only import of the role set this chain creates):
#   run "createMemberRoles_importChainFixture_rolesAssigned" {
#   run "importMemberRoles_byMemberID_noChange" {
#
# Not automated:
# - A member who leaves the project is removed from state on refresh.
#   Terraform cannot remove a project member, and the provider never does.
#   The unit test TestMemberRolesResourceReadRemovesMissingMember covers it.
# - The roles of a legacy project admin grant no extra permission, and new
#   roles take effect on a member's permissions. The backend caches computed
#   permissions for up to 300 seconds, so the suite asserts the stored
#   assignments, not the permissions.
# - Backend and lookup errors: an unknown role, a member who is not a human
#   member of the project, and the member that the token belongs to. These are
#   provider diagnostics, which expect_failures cannot catch; unit tests cover
#   them.

variable "token" {
  description = "Viettel Cloud API access token used by this test."
  type        = string
  sensitive   = true
}

variable "project_id" {
  description = "IAM-enabled project UUID or slug used by this test."
  type        = string
}

variable "api_endpoint" {
  description = "Viettel Cloud API endpoint used by this test."
  type        = string
}

variable "member_username" {
  description = "Username of a human member of project_id who is not a project admin and is not the token's identity."
  type        = string
}

variable "second_member_username" {
  description = "Username of a second human member of project_id who is not a project admin and is not the token's identity."
  type        = string
}

variable "legacy_admin_username" {
  description = "Username of a human member of project_id who is a project admin and is not the token's identity."
  type        = string
}

variable "predefined_role_urn" {
  description = "URN of an enabled predefined project role."
  type        = string
}

variable "second_predefined_role_urn" {
  description = "URN of a second enabled predefined project role."
  type        = string
}

variable "disabled_custom_role_urn" {
  description = "URN of a custom role of project_id whose enabled flag is false."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain legacy_admin.

# Assigning a role to a project admin succeeds and leaves the admin flag set.
# The provider never sends the admin flag, so the role set and the admin flag
# stay independent.
run "createMemberRoles_legacyAdmin_noEffect" {
  command   = apply
  parallel  = true
  state_key = "legacy_admin"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.legacy_admin_username
    roles    = [{ urn = var.predefined_role_urn }]
  }

  assert {
    condition = (
      length(viettelcloud_project_member_roles.this[0].id) > 0 &&
      viettelcloud_project_member_roles.this[0].member_is_admin == true &&
      length(viettelcloud_project_member_roles.this[0].assignments) == 1 &&
      viettelcloud_project_member_roles.this[0].assignments[0].urn == var.predefined_role_urn &&
      viettelcloud_project_member_roles.this[0].assignments[0].enabled == true
    )
    error_message = "The project admin did not keep the admin flag or did not receive the role."
  }
}


# Chain lifecycle.

# Creates the role set with one default and one disabled assignment, and
# checks every computed attribute.
run "createMemberRoles_predefinedRole_assigned" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles = [
      { urn = var.predefined_role_urn },
      { urn = var.second_predefined_role_urn, enabled = false },
    ]
  }

  assert {
    condition = (
      length(viettelcloud_project_member_roles.this[0].id) > 0 &&
      length(viettelcloud_project_member_roles.this[0].member_user_id) > 0 &&
      viettelcloud_project_member_roles.this[0].member_email != null &&
      viettelcloud_project_member_roles.this[0].member_is_admin == false &&
      length(viettelcloud_project_member_roles.this[0].created_at) > 0 &&
      length(viettelcloud_project_member_roles.this[0].updated_at) > 0 &&
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.predefined_role_urn)        = true
        (var.second_predefined_role_urn) = false
      }) &&
      tolist([for a in viettelcloud_project_member_roles.this[0].assignments : a.urn]) == sort([var.predefined_role_urn, var.second_predefined_role_urn]) &&
      alltrue([
        for a in viettelcloud_project_member_roles.this[0].assignments :
        length(a.id) > 0 && length(a.display_name) > 0 && a.description != null && length(a.type) > 0 && a.role_enabled == true
      ]) &&
      tomap({ for role in viettelcloud_project_member_roles.this[0].roles : role.role.urn => role.enabled }) == tomap({
        (var.predefined_role_urn)        = true
        (var.second_predefined_role_urn) = false
      })
    )
    error_message = "The role set was not created with the configured assignments, or a computed attribute is missing."
  }
}

# Planning the same configuration again proposes no change. A planned change
# would leave assignments unknown, and this assertion would then fail.
run "planAgain_predefinedRoleAssigned_noChanges" {
  command   = plan
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles = [
      { urn = var.predefined_role_urn },
      { urn = var.second_predefined_role_urn, enabled = false },
    ]
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.this[0].id == run.createMemberRoles_predefinedRole_assigned.member_roles_id &&
      viettelcloud_project_member_roles.this[0].assignments == run.createMemberRoles_predefinedRole_assigned.assignments
    )
    error_message = "Planning the unchanged role set proposed a change."
  }
}

# Adds the disabled custom role and disables the first predefined role in
# place. The assignments of the roles that stay keep their IDs.
run "updateMemberRoles_addRoleAndDisable_inPlace" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles = [
      { urn = var.predefined_role_urn, enabled = false },
      { urn = var.second_predefined_role_urn, enabled = false },
      { urn = var.disabled_custom_role_urn },
    ]
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.this[0].id == run.createMemberRoles_predefinedRole_assigned.member_roles_id &&
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.predefined_role_urn)        = false
        (var.second_predefined_role_urn) = false
        (var.disabled_custom_role_urn)   = true
      }) &&
      { for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.id }[var.predefined_role_urn] == { for a in run.createMemberRoles_predefinedRole_assigned.assignments : a.urn => a.id }[var.predefined_role_urn] &&
      { for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.id }[var.second_predefined_role_urn] == { for a in run.createMemberRoles_predefinedRole_assigned.assignments : a.urn => a.id }[var.second_predefined_role_urn]
    )
    error_message = "The role set was replaced, did not change in place, or changed the IDs of the assignments that stay."
  }
}

# Setup for the next run: a second Terraform state writes the same member's
# role set through the API, as a change made outside the lifecycle state. It
# removes two roles and enables the remaining one.
run "assignRolesOutsideTerraform_lifecycleMember_roleSetChanged" {
  command   = apply
  state_key = "lifecycle_outside"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = run.updateMemberRoles_addRoleAndDisable_inPlace.username
    roles    = [{ urn = var.second_predefined_role_urn }]
  }

  assert {
    condition = (
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.second_predefined_role_urn) = true
      })
    )
    error_message = "The out-of-band change of the role set did not apply."
  }
}

# Refresh reads the changed role set, and apply restores the configuration.
# Without drift detection the backend would keep the out-of-band role set.
run "refreshMemberRoles_changedOutside_restored" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = run.assignRolesOutsideTerraform_lifecycleMember_roleSetChanged.username
    roles = [
      { urn = var.predefined_role_urn, enabled = false },
      { urn = var.second_predefined_role_urn, enabled = false },
      { urn = var.disabled_custom_role_urn },
    ]
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.this[0].id == run.createMemberRoles_predefinedRole_assigned.member_roles_id &&
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.predefined_role_urn)        = false
        (var.second_predefined_role_urn) = false
        (var.disabled_custom_role_urn)   = true
      })
    )
    error_message = "Apply did not restore the configured role set after the out-of-band change."
  }
}

# Omitting enabled on assignments that were disabled enables them.
run "omitEnabled_disabledAssignments_assignmentsEnabledInPlace" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles = [
      { urn = var.predefined_role_urn },
      { urn = var.second_predefined_role_urn },
      { urn = var.disabled_custom_role_urn },
    ]
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.this[0].id == run.createMemberRoles_predefinedRole_assigned.member_roles_id &&
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.predefined_role_urn)        = true
        (var.second_predefined_role_urn) = true
        (var.disabled_custom_role_urn)   = true
      }) &&
      alltrue([for role in viettelcloud_project_member_roles.this[0].roles : role.enabled == true])
    )
    error_message = "Omitting enabled did not enable the assignments in place."
  }
}

# Changing the member replaces the resource: the previous member loses every
# role and the new member receives the configured set.
run "updateMemberRoles_changeMember_replaced" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.second_member_username
    roles = [
      { urn = var.predefined_role_urn },
      { urn = var.second_predefined_role_urn },
    ]
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.this[0].id != run.createMemberRoles_predefinedRole_assigned.member_roles_id &&
      viettelcloud_project_member_roles.this[0].member_user_id != run.createMemberRoles_predefinedRole_assigned.member_user_id &&
      viettelcloud_project_member_roles.this[0].member_is_admin == false &&
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.predefined_role_urn)        = true
        (var.second_predefined_role_urn) = true
      })
    )
    error_message = "Changing the member did not replace the resource with the configured role set."
  }
}

# The previous member stays in the project with no role.
run "lookupMemberRoles_previousMember_rolesRemoved" {
  command   = apply
  state_key = "lifecycle_previous_member"

  module {
    source = "./fixtures/member_roles/datasource"
  }

  variables {
    username = run.createMemberRoles_predefinedRole_assigned.username
  }

  assert {
    condition = (
      data.viettelcloud_project_member_roles.this.id == run.createMemberRoles_predefinedRole_assigned.member_roles_id &&
      data.viettelcloud_project_member_roles.this.member_user_id != run.updateMemberRoles_changeMember_replaced.member_user_id &&
      length(data.viettelcloud_project_member_roles.this.assignments) == 0 &&
      length(data.viettelcloud_project_member_roles.this.roles) == 0
    )
    error_message = "The previous member left the project or kept a role after the member change."
  }
}

# Removing the role set from the configuration destroys it. The next run
# checks the member, because a data source in this run would be read before
# the destroy.
run "destroyMemberRoles_existingAssignments_memberKept" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.second_member_username
    assigned = false
    roles    = []
  }

  assert {
    condition     = length(viettelcloud_project_member_roles.this) == 0
    error_message = "The role set is still in state after its configuration was removed."
  }
}

# After the destroy, the member stays in the project with no role and the
# same admin flag.
run "lookupMemberRoles_destroyedRoleSet_memberKeptWithoutRoles" {
  command   = apply
  state_key = "lifecycle_destroyed"

  module {
    source = "./fixtures/member_roles/datasource"
  }

  variables {
    username = run.updateMemberRoles_changeMember_replaced.username
  }

  assert {
    condition = (
      run.destroyMemberRoles_existingAssignments_memberKept.member_roles_id == null &&
      data.viettelcloud_project_member_roles.this.id == run.updateMemberRoles_changeMember_replaced.member_roles_id &&
      data.viettelcloud_project_member_roles.this.member_is_admin == run.updateMemberRoles_changeMember_replaced.member_is_admin &&
      length(data.viettelcloud_project_member_roles.this.assignments) == 0
    )
    error_message = "Destroying the role set removed the member, kept a role, or changed the admin flag."
  }
}


# Chain existing_roles.

# Setup for the next run: a second Terraform state assigns a role to the
# member through the API before the existing_roles state manages the member.
run "assignRolesOutsideTerraform_existingRolesMember_roleAssigned" {
  command   = apply
  state_key = "existing_roles_outside"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles    = [{ urn = var.second_predefined_role_urn }]
  }

  assert {
    condition = (
      tolist([for a in viettelcloud_project_member_roles.this[0].assignments : a.urn]) == tolist([var.second_predefined_role_urn])
    )
    error_message = "The role was not assigned before the existing_roles state took over the member."
  }
}

# Create replaces the role assigned before Terraform managed the member.
run "createMemberRoles_existingRoles_replaced" {
  command   = apply
  state_key = "existing_roles"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = run.assignRolesOutsideTerraform_existingRolesMember_roleAssigned.username
    roles    = [{ urn = var.predefined_role_urn }]
  }

  assert {
    condition = (
      tomap({ for a in viettelcloud_project_member_roles.this[0].assignments : a.urn => a.enabled }) == tomap({
        (var.predefined_role_urn) = true
      })
    )
    error_message = "Create kept a role assigned before Terraform managed the member."
  }
}

# An empty role set removes every role in place and keeps the member.
run "updateMemberRoles_emptyRoles_cleared" {
  command   = apply
  state_key = "existing_roles"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles    = []
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.this[0].id == run.createMemberRoles_existingRoles_replaced.member_roles_id &&
      length(viettelcloud_project_member_roles.this[0].roles) == 0 &&
      length(viettelcloud_project_member_roles.this[0].assignments) == 0
    )
    error_message = "An empty role set did not remove every role in place."
  }
}


# Chain disabled_role.

# A disabled role can be assigned. The assignment is enabled, and the role
# itself reports role_enabled = false.
run "createMemberRoles_disabledRole_assigned" {
  command   = apply
  state_key = "disabled_role"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.second_member_username
    roles    = [{ urn = var.disabled_custom_role_urn }]
  }

  assert {
    condition = (
      length(viettelcloud_project_member_roles.this[0].assignments) == 1 &&
      viettelcloud_project_member_roles.this[0].assignments[0].urn == var.disabled_custom_role_urn &&
      viettelcloud_project_member_roles.this[0].assignments[0].enabled == true &&
      viettelcloud_project_member_roles.this[0].assignments[0].role_enabled == false
    )
    error_message = "The disabled role was not assigned, or role_enabled does not report it as disabled."
  }
}


# Chain import.

# The import chain creates the role set it later imports, so no other chain
# has to hand it over.
run "createMemberRoles_importChainFixture_rolesAssigned" {
  command   = apply
  state_key = "import"

  module {
    source = "./fixtures/member_roles/resource"
  }

  variables {
    username = var.member_username
    roles = [
      { urn = var.predefined_role_urn },
      { urn = var.second_predefined_role_urn, enabled = false },
    ]
  }

  assert {
    condition = (
      length(viettelcloud_project_member_roles.this[0].id) > 0 &&
      length(viettelcloud_project_member_roles.this[0].assignments) == 2
    )
    error_message = "The import chain's role set was not created."
  }
}

# Importing the role set by member ID restores its state and plans no change.
# The run stays plan-only under its own state key, so the role set keeps one
# owner. member and roles come from the creating run's state. The assertions
# check computed attributes, which only the import refresh supplies; any
# planned change would leave them unknown and fail this run.
run "importMemberRoles_byMemberID_noChange" {
  command   = plan
  state_key = "import_target"

  module {
    source = "./fixtures/member_roles/import"
  }

  variables {
    member_roles_id = run.createMemberRoles_importChainFixture_rolesAssigned.member_roles_id
    username        = run.createMemberRoles_importChainFixture_rolesAssigned.username
    roles           = run.createMemberRoles_importChainFixture_rolesAssigned.role_settings
  }

  assert {
    condition = (
      viettelcloud_project_member_roles.imported.id == run.createMemberRoles_importChainFixture_rolesAssigned.member_roles_id &&
      viettelcloud_project_member_roles.imported.member_user_id == run.createMemberRoles_importChainFixture_rolesAssigned.member_user_id &&
      viettelcloud_project_member_roles.imported.member_email == run.createMemberRoles_importChainFixture_rolesAssigned.member_email &&
      viettelcloud_project_member_roles.imported.member_is_admin == run.createMemberRoles_importChainFixture_rolesAssigned.member_is_admin &&
      viettelcloud_project_member_roles.imported.created_at == run.createMemberRoles_importChainFixture_rolesAssigned.created_at &&
      viettelcloud_project_member_roles.imported.assignments == run.createMemberRoles_importChainFixture_rolesAssigned.assignments
    )
    error_message = "Importing the role set did not restore the state the backend reports, or it planned a change."
  }
}
