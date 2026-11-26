# Test index. Copy a run declaration to find its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain subject (shared setup; state_key = "subject"; assigns the roles that
# the lookups read to member member_username):
#   run "createMemberRoles_lookupSubject_rolesAssigned" {
#
# Chain lookup (state_key = "lookup"):
#   run "lookupMemberRoles_byUsername_found" {
#
# Chain lookup_untrimmed (state_key = "lookup_untrimmed"):
#   run "lookupMemberRoles_untrimmedUsername_configuredValuePreserved" {
#
# Not automated: a missing username, a whitespace-only username, and a username
# that matches no member exactly. These are provider diagnostics, which
# expect_failures cannot catch; unit tests cover them.

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

variable "predefined_role_urn" {
  description = "URN of an enabled predefined project role."
  type        = string
}

variable "second_predefined_role_urn" {
  description = "URN of a second enabled predefined project role."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain subject.

# Gives the member one enabled and one disabled assignment, so the lookups can
# tell the two apart.
run "createMemberRoles_lookupSubject_rolesAssigned" {
  command   = apply
  parallel  = true
  state_key = "subject"

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
    condition     = length(viettelcloud_project_member_roles.this[0].assignments) == 2
    error_message = "The lookup subject did not receive its roles."
  }
}


# Chain lookup.

# The lookup by username returns the member with every attribute equal to the
# managed role set.
run "lookupMemberRoles_byUsername_found" {
  command   = apply
  parallel  = true
  state_key = "lookup"

  module {
    source = "./fixtures/member_roles/datasource"
  }

  variables {
    username = run.createMemberRoles_lookupSubject_rolesAssigned.username
  }

  assert {
    condition = (
      data.viettelcloud_project_member_roles.this.username == run.createMemberRoles_lookupSubject_rolesAssigned.username &&
      data.viettelcloud_project_member_roles.this.id == run.createMemberRoles_lookupSubject_rolesAssigned.member_roles_id &&
      data.viettelcloud_project_member_roles.this.member_user_id == run.createMemberRoles_lookupSubject_rolesAssigned.member_user_id &&
      data.viettelcloud_project_member_roles.this.member_email == run.createMemberRoles_lookupSubject_rolesAssigned.member_email &&
      data.viettelcloud_project_member_roles.this.member_is_admin == run.createMemberRoles_lookupSubject_rolesAssigned.member_is_admin &&
      data.viettelcloud_project_member_roles.this.created_at == run.createMemberRoles_lookupSubject_rolesAssigned.created_at &&
      length(data.viettelcloud_project_member_roles.this.updated_at) > 0 &&
      data.viettelcloud_project_member_roles.this.assignments == run.createMemberRoles_lookupSubject_rolesAssigned.assignments &&
      tomap({ for role in data.viettelcloud_project_member_roles.this.roles : role.role_urn => role.enabled }) == tomap({
        (var.predefined_role_urn)        = true
        (var.second_predefined_role_urn) = false
      })
    )
    error_message = "The lookup by username did not return the member with the attributes of the managed role set."
  }
}


# Chain lookup_untrimmed.

# The provider trims the username before the lookup and keeps the configured
# form in state, so an untrimmed username finds the same member.
run "lookupMemberRoles_untrimmedUsername_configuredValuePreserved" {
  command   = apply
  parallel  = true
  state_key = "lookup_untrimmed"

  module {
    source = "./fixtures/member_roles/datasource"
  }

  variables {
    username = "  ${run.createMemberRoles_lookupSubject_rolesAssigned.username}  "
  }

  assert {
    condition = (
      data.viettelcloud_project_member_roles.this.username == "  ${run.createMemberRoles_lookupSubject_rolesAssigned.username}  " &&
      data.viettelcloud_project_member_roles.this.id == run.createMemberRoles_lookupSubject_rolesAssigned.member_roles_id
    )
    error_message = "The untrimmed username did not find the member or was not preserved in state."
  }
}
