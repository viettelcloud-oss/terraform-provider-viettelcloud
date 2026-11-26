# Test index. Copy a run declaration to find its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain subject (shared setup; state_key = "subject"; the role that the lookups
# read):
#   run "createCustomRole_lookupSubject_roleCreated" {
#
# Chain subject_prefixed (shared setup; state_key = "subject_prefixed"; a role
# whose name starts with the subject's name, so a lookup by name must still
# pick the subject):
#   run "createCustomRole_lookupSubjectPrefixed_roleCreated" {
#
# Chain lookup_by_id (state_key = "lookup_by_id"):
#   run "lookupCustomRole_byID_found" {
#
# Chain lookup_by_name (state_key = "lookup_by_name"):
#   run "lookupCustomRole_byName_found" {
#
# Chain lookup_by_id_and_name (state_key = "lookup_by_id_and_name"):
#   run "lookupCustomRole_byIDAndName_found" {
#
# Chain lookup_untrimmed (state_key = "lookup_untrimmed"):
#   run "lookupCustomRole_untrimmedNameAndUppercaseID_configuredValuePreserved" {
#
# Not automated: missing criteria, whitespace-only criteria, an invalid UUID,
# an ID and a name of different roles, and a name that matches no role. These
# are provider diagnostics, which expect_failures cannot catch; unit tests
# cover them.

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


# Chain subject.

# Creates the role that every lookup reads. It has a description, a disabled
# flag, and two actions, so each attribute differs from its default.
run "createCustomRole_lookupSubject_roleCreated" {
  command   = apply
  parallel  = true
  state_key = "subject"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lookup"
    description  = "Custom Role for lookups"
    enabled      = false
    action_urns  = [var.action_urn, var.second_action_urn]
  }

  assert {
    condition     = viettelcloud_project_custom_role.this.name == "tf-test-cr-lookup"
    error_message = "The lookup subject was not created with the expected name."
  }
}


# Chain subject_prefixed.

# Creates a second role whose name contains the subject's name. The backend
# name filter matches partially, so it returns both roles for the subject's
# name.
run "createCustomRole_lookupSubjectPrefixed_roleCreated" {
  command   = apply
  parallel  = true
  state_key = "subject_prefixed"

  module {
    source = "./fixtures/custom_role/resource"
  }

  variables {
    display_name = "TF test CR lookup admin"
    action_urns  = [var.action_urn]
  }

  assert {
    condition     = viettelcloud_project_custom_role.this.name == "tf-test-cr-lookup-admin"
    error_message = "The prefixed role was not created with the expected name."
  }
}


# Chain lookup_by_id.

# The lookup by ID returns every attribute of the subject.
run "lookupCustomRole_byID_found" {
  command   = apply
  parallel  = true
  state_key = "lookup_by_id"

  module {
    source = "./fixtures/custom_role/datasource"
  }

  variables {
    id = run.createCustomRole_lookupSubject_roleCreated.custom_role_id
  }

  assert {
    condition = (
      data.viettelcloud_project_custom_role.this.id == run.createCustomRole_lookupSubject_roleCreated.custom_role_id &&
      data.viettelcloud_project_custom_role.this.name == run.createCustomRole_lookupSubject_roleCreated.name &&
      data.viettelcloud_project_custom_role.this.display_name == run.createCustomRole_lookupSubject_roleCreated.display_name &&
      data.viettelcloud_project_custom_role.this.description == run.createCustomRole_lookupSubject_roleCreated.description &&
      data.viettelcloud_project_custom_role.this.enabled == run.createCustomRole_lookupSubject_roleCreated.enabled &&
      data.viettelcloud_project_custom_role.this.urn == run.createCustomRole_lookupSubject_roleCreated.urn &&
      sort([for action in data.viettelcloud_project_custom_role.this.actions : action.urn]) == run.createCustomRole_lookupSubject_roleCreated.action_urns &&
      data.viettelcloud_project_custom_role.this.created_at == run.createCustomRole_lookupSubject_roleCreated.created_at &&
      data.viettelcloud_project_custom_role.this.updated_at == run.createCustomRole_lookupSubject_roleCreated.updated_at
    )
    error_message = "The lookup by ID did not return every attribute of the Custom Role."
  }
}


# Chain lookup_by_name.

# The lookup by name picks the role with exactly that name, not the prefixed
# role that the backend filter also returns. The actions come from the role
# detail, because the role list carries none.
run "lookupCustomRole_byName_found" {
  command   = apply
  parallel  = true
  state_key = "lookup_by_name"

  module {
    source = "./fixtures/custom_role/datasource"
  }

  variables {
    name = run.createCustomRole_lookupSubject_roleCreated.name
  }

  assert {
    condition = (
      data.viettelcloud_project_custom_role.this.id == run.createCustomRole_lookupSubject_roleCreated.custom_role_id &&
      data.viettelcloud_project_custom_role.this.id != run.createCustomRole_lookupSubjectPrefixed_roleCreated.custom_role_id &&
      data.viettelcloud_project_custom_role.this.name == run.createCustomRole_lookupSubject_roleCreated.name &&
      data.viettelcloud_project_custom_role.this.description == run.createCustomRole_lookupSubject_roleCreated.description &&
      data.viettelcloud_project_custom_role.this.enabled == run.createCustomRole_lookupSubject_roleCreated.enabled &&
      sort([for action in data.viettelcloud_project_custom_role.this.actions : action.urn]) == run.createCustomRole_lookupSubject_roleCreated.action_urns
    )
    error_message = "The lookup by name did not pick the exact role, or did not read its actions."
  }
}


# Chain lookup_by_id_and_name.

# With both criteria, the lookup reads the role by ID and accepts it because
# the name matches.
run "lookupCustomRole_byIDAndName_found" {
  command   = apply
  parallel  = true
  state_key = "lookup_by_id_and_name"

  module {
    source = "./fixtures/custom_role/datasource"
  }

  variables {
    id   = run.createCustomRole_lookupSubject_roleCreated.custom_role_id
    name = run.createCustomRole_lookupSubject_roleCreated.name
  }

  assert {
    condition = (
      data.viettelcloud_project_custom_role.this.id == run.createCustomRole_lookupSubject_roleCreated.custom_role_id &&
      data.viettelcloud_project_custom_role.this.name == run.createCustomRole_lookupSubject_roleCreated.name
    )
    error_message = "The lookup by ID and name did not return the Custom Role."
  }
}

# A name with outer whitespace and an uppercase ID identify the same role as
# the backend values, so state keeps both as configured.
run "lookupCustomRole_untrimmedNameAndUppercaseID_configuredValuePreserved" {
  command   = apply
  parallel  = true
  state_key = "lookup_untrimmed"

  module {
    source = "./fixtures/custom_role/datasource"
  }

  variables {
    id   = upper(run.createCustomRole_lookupSubject_roleCreated.custom_role_id)
    name = "  ${run.createCustomRole_lookupSubject_roleCreated.name}  "
  }

  assert {
    condition = (
      data.viettelcloud_project_custom_role.this.id == upper(run.createCustomRole_lookupSubject_roleCreated.custom_role_id) &&
      data.viettelcloud_project_custom_role.this.name == "  ${run.createCustomRole_lookupSubject_roleCreated.name}  " &&
      data.viettelcloud_project_custom_role.this.urn == run.createCustomRole_lookupSubject_roleCreated.urn
    )
    error_message = "The lookup did not keep the configured id and name, or it returned a different Custom Role."
  }
}
