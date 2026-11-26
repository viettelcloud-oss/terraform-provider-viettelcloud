# Test index. Copy a run declaration below to find its block.
# Chain import_create (state_key member_import_create):
#   run "createImportSubject_requiredInputsOnly_memberActive" {
# Chain import_plan (state_key member_import_plan; depends on import_create):
#   run "planImport_existingMember_refreshRestoresComputedValues" {
# Chain cross_group (state_key member_cross_group; depends on import_create):
#   run "createMember_sameAddressPortInDifferentGroups_bothCreated" {
# Chain lifecycle (state_key member_lifecycle):
#   run "createMember_requiredInputsOnly_providerDefaultsReturned" {
#   run "changeMember_nameWeightAndBackup_identityPreserved" {
#   run "planAgain_untrimmedMemberName_noDrift" {
#   run "omitMember_weightAndBackup_providerDefaultsRestored" {
#   run "changeAddress_secondBackend_memberReplaced" {
#   run "changePort_secondBackendPort_memberReplaced" {
#   run "changeServerGroup_secondParent_memberReplaced" {
#   run "planAgain_replacedMember_noDrift" {
#
# Every run is serial, in file order, because all chains mutate the same two
# server groups. The backend refuses a concurrent mutation of one server group
# as busy. The provider waits for the idle state and does not send a refused
# request again, so parallel chains on one server group can still fail. In
# run 20261023T085007Z-all-1d07f8, three parallel chains on the first server
# group kept refusing each other. Section 9.2 of
# design/loadbalancer/server_group_member.md asks for serial chains in this
# case.
#
# The lifecycle chain is last in the file because its replacement runs are the
# only runs that delete a member before teardown. In run
# 20261105T081243Z-tests-lb-member-resource-tftest-hcl-1b7dc4, the first server
# group stayed PENDING_UPDATE after a member delete (cmp-backend#1933), and every
# later run on that group failed or was skipped. With this order, the import and
# cross-group runs finish before the first delete.
#
# The package of a Load Balancer limits the members across all its server
# groups. Load Balancers of package SMALL on the test project accept two, and
# run 20261105T101026Z-tests-lb-member-resource-tftest-hcl-40c68d was refused
# with "Server Group Member requested: 3, limit: 2".
# createMember_sameAddressPortInDifferentGroups_bothCreated therefore adds its
# member to the second server group. It reuses the address and port of the
# import subject instead of a member of its own in the first server group. The
# suite then holds at most three members at once, and at most
# two in each server group, also when the lifecycle member moves to the second
# one.
#
# The no-drift runs do not compare updated_at with an earlier run, because the
# backend rewrites updated_at about every 10 seconds without a change
# (provider#26). updated_at is Computed without UseStateForUnknown, so a planned
# update makes it unknown, and an assertion that reads it cannot be evaluated.
# A planned update therefore still fails these runs.

variable "lb_server_group_ids" {
  type        = list(string)
  description = "UUIDs of two existing, ACTIVE server groups that the suite adds members to. The server_group replacement moves a member from the first to the second."
  validation {
    condition     = length(var.lb_server_group_ids) == 2
    error_message = "Provide exactly two server group UUIDs."
  }
}
variable "lb_member_addresses" {
  type        = list(string)
  default     = ["10.20.0.11", "10.20.0.12", "10.20.0.13"]
  description = "Three backend IP addresses that the server groups accept as members. Members do not need a running backend."
  validation {
    condition     = length(var.lb_member_addresses) == 3
    error_message = "Provide exactly three backend IP addresses."
  }
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

run "createImportSubject_requiredInputsOnly_memberActive" {
  command   = apply
  state_key = "member_import_create"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = "tf-test-member-import"
    address         = var.lb_member_addresses[2]
    protocol_port   = 80
  }
  assert {
    condition     = output.member.provisioning_status == "ACTIVE"
    error_message = "The import subject did not become ACTIVE."
  }
}

run "planImport_existingMember_refreshRestoresComputedValues" {
  command   = plan
  state_key = "member_import_plan"
  module { source = "./fixtures/lb_member/import" }
  variables {
    id              = run.createImportSubject_requiredInputsOnly_memberActive.member.id
    server_group_id = run.createImportSubject_requiredInputsOnly_memberActive.member.server_group.id
    name            = run.createImportSubject_requiredInputsOnly_memberActive.member.name
    address         = run.createImportSubject_requiredInputsOnly_memberActive.member.address
    protocol_port   = run.createImportSubject_requiredInputsOnly_memberActive.member.protocol_port
  }
  assert {
    condition = (
      output.member.id == run.createImportSubject_requiredInputsOnly_memberActive.member.id &&
      output.member.weight == 1 &&
      !output.member.backup &&
      output.member.provisioning_status == "ACTIVE" &&
      output.member.created_at == run.createImportSubject_requiredInputsOnly_memberActive.member.created_at
    )
    error_message = "Import did not refresh the member fields."
  }
}

run "createMember_sameAddressPortInDifferentGroups_bothCreated" {
  command   = apply
  state_key = "member_cross_group"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[1]
    name            = "tf-test-member-cross-group-second"
    address         = run.createImportSubject_requiredInputsOnly_memberActive.member.address
    protocol_port   = run.createImportSubject_requiredInputsOnly_memberActive.member.protocol_port
  }
  assert {
    condition = (
      output.member.id != run.createImportSubject_requiredInputsOnly_memberActive.member.id &&
      output.member.server_group.id == var.lb_server_group_ids[1] &&
      output.member.address == run.createImportSubject_requiredInputsOnly_memberActive.member.address &&
      output.member.protocol_port == run.createImportSubject_requiredInputsOnly_memberActive.member.protocol_port &&
      output.member.provisioning_status == "ACTIVE"
    )
    error_message = "The second server group did not accept a member with the address and port of a member in the first group."
  }
}

run "createMember_requiredInputsOnly_providerDefaultsReturned" {
  command   = apply
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = "tf-test-member-lifecycle"
    address         = var.lb_member_addresses[0]
    protocol_port   = 80
  }
  assert {
    condition = (
      length(output.member.id) > 0 &&
      output.member.server_group.id == var.lb_server_group_ids[0] &&
      output.member.name == var.name &&
      output.member.address == var.address &&
      output.member.protocol_port == 80 &&
      output.member.weight == 1 &&
      !output.member.backup &&
      output.member.provisioning_status == "ACTIVE" &&
      length(output.member.status) > 0 &&
      length(output.member.created_at) > 0 &&
      length(output.member.updated_at) > 0
    )
    error_message = "The member did not become ACTIVE with complete state and provider defaults."
  }
}

run "changeMember_nameWeightAndBackup_identityPreserved" {
  command   = apply
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[0]
    protocol_port   = 80
    weight          = 5
    backup          = true
  }
  assert {
    condition = (
      output.member.id == run.createMember_requiredInputsOnly_providerDefaultsReturned.member.id &&
      output.member.name == var.name &&
      output.member.weight == 5 &&
      output.member.backup &&
      output.member.provisioning_status == "ACTIVE"
    )
    error_message = "The in-place update changed the member ID or did not apply name, weight, and backup."
  }
}

run "planAgain_untrimmedMemberName_noDrift" {
  command   = plan
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[0]
    protocol_port   = 80
    weight          = 5
    backup          = true
  }
  assert {
    condition = (
      output.member.id == run.changeMember_nameWeightAndBackup_identityPreserved.member.id &&
      output.member.name == var.name &&
      output.member.weight == 5 &&
      output.member.backup &&
      length(output.member.updated_at) > 0
    )
    error_message = "The normalized member name caused drift in an unchanged plan."
  }
}

run "omitMember_weightAndBackup_providerDefaultsRestored" {
  command   = apply
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[0]
    protocol_port   = 80
  }
  assert {
    condition = (
      output.member.id == run.createMember_requiredInputsOnly_providerDefaultsReturned.member.id &&
      output.member.weight == 1 &&
      !output.member.backup
    )
    error_message = "Omitting weight and backup did not restore the provider defaults in place."
  }
}

run "changeAddress_secondBackend_memberReplaced" {
  command   = apply
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[1]
    protocol_port   = 80
  }
  assert {
    condition = (
      output.member.id != run.omitMember_weightAndBackup_providerDefaultsRestored.member.id &&
      output.member.address == var.address &&
      output.member.provisioning_status == "ACTIVE"
    )
    error_message = "Changing the address did not replace the member."
  }
}

run "changePort_secondBackendPort_memberReplaced" {
  command   = apply
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[1]
    protocol_port   = 8080
  }
  assert {
    condition = (
      output.member.id != run.changeAddress_secondBackend_memberReplaced.member.id &&
      output.member.protocol_port == 8080 &&
      output.member.provisioning_status == "ACTIVE"
    )
    error_message = "Changing the protocol port did not replace the member."
  }
}

run "changeServerGroup_secondParent_memberReplaced" {
  command   = apply
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[1]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[1]
    protocol_port   = 8080
  }
  assert {
    condition = (
      output.member.id != run.changePort_secondBackendPort_memberReplaced.member.id &&
      output.member.server_group.id == var.lb_server_group_ids[1] &&
      output.member.provisioning_status == "ACTIVE"
    )
    error_message = "Changing the server group did not replace the member."
  }
}

run "planAgain_replacedMember_noDrift" {
  command   = plan
  state_key = "member_lifecycle"
  module { source = "./fixtures/lb_member/resource" }
  variables {
    server_group_id = var.lb_server_group_ids[1]
    name            = " tf-test-member-renamed "
    address         = var.lb_member_addresses[1]
    protocol_port   = 8080
  }
  assert {
    condition = (
      output.member.id == run.changeServerGroup_secondParent_memberReplaced.member.id &&
      output.member.server_group.id == var.lb_server_group_ids[1] &&
      length(output.member.updated_at) > 0
    )
    error_message = "The unchanged member configuration planned a state change."
  }
}
