# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (state_key = "lifecycle"; required inputs only, then every
# in-place update):
#   run "createSnapshotPolicy_minimal_created" {
#   run "planSnapshotPolicy_minimalApplied_noChanges" {
#   run "updateSnapshotPolicy_name_inPlace" {
#   run "updateSnapshotPolicy_daysOfWeek_inPlace" {
#   run "updateSnapshotPolicy_time_inPlace" {
#   run "updateSnapshotPolicy_status_inPlace" {
#   run "updateSnapshotPolicy_retention_inPlace" {
#
# Chain full (state_key = "full"; optional inputs set, then omitted and
# cleared):
#   run "createSnapshotPolicy_withAll_stateMatches" {
#   run "updateSnapshotPolicy_omitDescription_backendKeepsValue" {
#   run "updateSnapshotPolicy_omitRetention_backendKeepsValue" {
#   run "planSnapshotPolicy_optionalsOmitted_noChanges" {
#   run "updateSnapshotPolicy_clearDescription_backendClears" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# policy this chain creates for itself):
#   run "createSnapshotPolicy_importChainFixture_created" {
#   run "importSnapshotPolicy_byId_noChanges" {
#
# Chain normalized (state_key = "normalized"; untrimmed name and region, days
# out of calendar order, and HH:MM time for the whole life of its own policy):
#   run "createSnapshotPolicy_untrimmedInputs_configuredValuesPreserved" {
#   run "planSnapshotPolicy_untrimmedInputs_noChanges" {
#
# Chain drift (state_key = "drift", with "drift_deleter" deleting the policy
# outside the state that created it):
#   run "createSnapshotPolicy_driftChainFixture_created" {
#   run "adoptSnapshotPolicy_secondState_policyAdopted" {
#   run "destroySnapshotPolicy_allResources_confirmed" {
#   run "deleteSnapshotPolicy_outsideTerraform_driftDetected" {
#
# Chain replace (state_key = "replace"; region change replaces the policy):
#   run "createSnapshotPolicy_replaceChainFixture_created" {
#   run "updateSnapshotPolicy_regionName_replacement" {

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

variable "region" {
  description = "Project region where the policies are created."
  type        = string
  default     = "vn-central-1"
}

variable "second_region" {
  description = "A second region of the same project; the replace chain moves its policy there."
  type        = string
  default     = "vn-north-1"
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

# Creates a policy from the required inputs only. The backend defaults
# retention to 1, and every computed attribute must be known after apply.
run "createSnapshotPolicy_minimal_created" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-min"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.name == "tf-test-fsp-min" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region.name == var.region &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.days_of_week == tolist(["mon", "fri"]) &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.time == "02:00" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.status == "enabled" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 1 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.filesystem_count == 0 &&
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.state) > 0 &&
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.region_id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region_name == var.region &&
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.project_id) > 0 &&
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.created_at) > 0 &&
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.updated_at) > 0
    )
    error_message = "The minimal policy was not created with every computed value."
  }
}

# Replanning the same configuration plans no change. A planned update would
# leave updated_at unknown and fail the assertion.
run "planSnapshotPolicy_minimalApplied_noChanges" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-min"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_minimal_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.updated_at == run.createSnapshotPolicy_minimal_created.policy_updated_at &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 1 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.time == "02:00"
    )
    error_message = "Replanning the minimal policy planned a change."
  }
}

# Renames the policy without replacing it.
run "updateSnapshotPolicy_name_inPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-renamed"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_minimal_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.name == "tf-test-fsp-renamed" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 1
    )
    error_message = "Renaming the policy replaced it or did not apply the name."
  }
}

# Changes the schedule days without replacing the policy.
run "updateSnapshotPolicy_daysOfWeek_inPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-renamed"
    region       = var.region
    days_of_week = ["tue", "thu", "sat"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_minimal_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.days_of_week == tolist(["tue", "thu", "sat"])
    )
    error_message = "Changing days_of_week replaced the policy or did not apply the days."
  }
}

# Changes the schedule time without replacing the policy.
run "updateSnapshotPolicy_time_inPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-renamed"
    region       = var.region
    days_of_week = ["tue", "thu", "sat"]
    time         = "22:45"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_minimal_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.time == "22:45"
    )
    error_message = "Changing time replaced the policy or did not apply the time."
  }
}

# Disables the policy without replacing it.
run "updateSnapshotPolicy_status_inPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-renamed"
    region       = var.region
    days_of_week = ["tue", "thu", "sat"]
    time         = "22:45"
    status       = "disabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_minimal_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.status == "disabled"
    )
    error_message = "Disabling the policy replaced it or did not apply the status."
  }
}

# Raises retention without replacing the policy. This run ends the chain.
run "updateSnapshotPolicy_retention_inPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-renamed"
    region       = var.region
    days_of_week = ["tue", "thu", "sat"]
    time         = "22:45"
    status       = "disabled"
    retention    = 3
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_minimal_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 3 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.status == "disabled"
    )
    error_message = "Changing retention replaced the policy or did not apply the retention."
  }
}


# Chain full.

# Creates a policy with every optional input set.
run "createSnapshotPolicy_withAll_stateMatches" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-full"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
    description  = "Nightly policy"
    retention    = 5
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.description == "Nightly policy" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 5 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.days_of_week == tolist(["mon", "fri"]) &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.time == "02:00"
    )
    error_message = "The policy was not created with the configured description and retention."
  }
}

# Omitting description keeps the backend value.
run "updateSnapshotPolicy_omitDescription_backendKeepsValue" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-full"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
    retention    = 5
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_withAll_stateMatches.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.description == "Nightly policy" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 5
    )
    error_message = "Omitting description changed the policy identity or description."
  }
}

# Omitting retention keeps the backend value. The provider sends retention
# from state in every PUT because the backend rejects a PUT without it.
run "updateSnapshotPolicy_omitRetention_backendKeepsValue" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-full"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_withAll_stateMatches.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 5 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.description == "Nightly policy"
    )
    error_message = "Omitting retention changed the policy identity or retention."
  }
}

# Replanning with description and retention omitted plans no change.
run "planSnapshotPolicy_optionalsOmitted_noChanges" {
  command   = plan
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-full"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_withAll_stateMatches.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.updated_at == run.updateSnapshotPolicy_omitRetention_backendKeepsValue.policy_updated_at &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 5
    )
    error_message = "Replanning with omitted optional inputs planned a change."
  }
}

# An explicit empty description clears it. This run ends the chain.
run "updateSnapshotPolicy_clearDescription_backendClears" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-full"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
    description  = ""
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_withAll_stateMatches.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.description == "" &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 5
    )
    error_message = "Clearing description replaced the policy or did not persist an empty value."
  }
}


# Chain import.

# Creates the policy that the next run imports. The provider reports time as
# HH:MM, so the imported state matches the configuration with no plan change.
run "createSnapshotPolicy_importChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-import"
    region       = var.region
    days_of_week = ["sat", "sun"]
    time         = "04:30"
    status       = "enabled"
    description  = "Policy for import"
    retention    = 4
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.retention == 4
    )
    error_message = "The import chain's policy was not created with the expected values."
  }
}

# Importing the policy restores its state from the backend and plans no
# change. The run stays plan-only under its own state key, so the policy that
# the previous run owns does not gain a second owner. Every input comes from
# the creating run's state. The assertions check the attributes that only the
# import refresh supplies; any planned change would make the computed values
# unknown and fail the run.
run "importSnapshotPolicy_byId_noChanges" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/filesystem_snapshot_policy/import"
  }

  variables {
    policy_id    = run.createSnapshotPolicy_importChainFixture_created.policy_id
    name         = run.createSnapshotPolicy_importChainFixture_created.policy_name
    region       = run.createSnapshotPolicy_importChainFixture_created.policy_region
    days_of_week = run.createSnapshotPolicy_importChainFixture_created.policy_days_of_week
    time         = run.createSnapshotPolicy_importChainFixture_created.policy_time
    status       = run.createSnapshotPolicy_importChainFixture_created.policy_status
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.imported.id == run.createSnapshotPolicy_importChainFixture_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.description == run.createSnapshotPolicy_importChainFixture_created.policy_description &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.retention == run.createSnapshotPolicy_importChainFixture_created.policy_retention &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.filesystem_count == run.createSnapshotPolicy_importChainFixture_created.policy_filesystem_count &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.region_id == run.createSnapshotPolicy_importChainFixture_created.policy_region_id &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.region_name == run.createSnapshotPolicy_importChainFixture_created.policy_region_name &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.project_id == run.createSnapshotPolicy_importChainFixture_created.policy_project_id &&
      viettelcloud_fs_filesystem_snapshot_policy.imported.created_at == run.createSnapshotPolicy_importChainFixture_created.policy_created_at &&
      length(viettelcloud_fs_filesystem_snapshot_policy.imported.updated_at) > 0
    )
    error_message = "Importing the policy did not restore the state the backend reports, or it planned a change."
  }
}

# Chain normalized.

# The provider trims name and region before each request, and the backend may
# report days in calendar order. State keeps the
# configured spelling. region carries RequiresReplace, so this chain keeps the
# padded region for the whole life of its own policy.
run "createSnapshotPolicy_untrimmedInputs_configuredValuesPreserved" {
  command   = apply
  parallel  = true
  state_key = "normalized"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "  tf-test-fsp-norm  "
    region       = "  ${var.region}  "
    days_of_week = ["sun", "mon"]
    time         = "05:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.name == "  tf-test-fsp-norm  " &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region.name == var.region &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region_name == trimspace(var.region) &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.days_of_week == tolist(["sun", "mon"]) &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.time == "05:00"
    )
    error_message = "The configured spelling of name, region, days_of_week, or time was not preserved."
  }
}

# Replanning the configured spelling plans no change and no replacement.
run "planSnapshotPolicy_untrimmedInputs_noChanges" {
  command   = plan
  parallel  = true
  state_key = "normalized"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "  tf-test-fsp-norm  "
    region       = "  ${var.region}  "
    days_of_week = ["sun", "mon"]
    time         = "05:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id == run.createSnapshotPolicy_untrimmedInputs_configuredValuesPreserved.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.updated_at == run.createSnapshotPolicy_untrimmedInputs_configuredValuesPreserved.policy_updated_at &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.name == "  tf-test-fsp-norm  "
    )
    error_message = "The untrimmed inputs caused drift or a replacement."
  }
}

# Chain drift.

# Creates the policy that the drift_deleter state deletes later in this chain.
run "createSnapshotPolicy_driftChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "drift"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-drift"
    region       = var.region
    days_of_week = ["wed"]
    time         = "06:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0
    )
    error_message = "The drift chain's policy was not created."
  }
}

# Imports the policy into the drift_deleter state. That state can then delete
# the policy while the drift state still records it.
run "adoptSnapshotPolicy_secondState_policyAdopted" {
  command   = apply
  parallel  = true
  state_key = "drift_deleter"

  module {
    source = "./fixtures/filesystem_snapshot_policy/adopt"
  }

  variables {
    adopt        = true
    policy_id    = run.createSnapshotPolicy_driftChainFixture_created.policy_id
    name         = run.createSnapshotPolicy_driftChainFixture_created.policy_name
    region       = run.createSnapshotPolicy_driftChainFixture_created.policy_region
    days_of_week = run.createSnapshotPolicy_driftChainFixture_created.policy_days_of_week
    time         = run.createSnapshotPolicy_driftChainFixture_created.policy_time
    status       = run.createSnapshotPolicy_driftChainFixture_created.policy_status
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.adopted[run.createSnapshotPolicy_driftChainFixture_created.policy_id].id == run.createSnapshotPolicy_driftChainFixture_created.policy_id
    )
    error_message = "The second state did not adopt the drift chain policy."
  }
}

# Removing the policy from the drift_deleter configuration destroys it on the
# backend. The next run proves the deletion: its refresh no longer finds the
# policy.
run "destroySnapshotPolicy_allResources_confirmed" {
  command   = apply
  parallel  = true
  state_key = "drift_deleter"

  module {
    source = "./fixtures/filesystem_snapshot_policy/adopt"
  }

  variables {
    adopt        = false
    policy_id    = run.createSnapshotPolicy_driftChainFixture_created.policy_id
    name         = run.createSnapshotPolicy_driftChainFixture_created.policy_name
    region       = run.createSnapshotPolicy_driftChainFixture_created.policy_region
    days_of_week = run.createSnapshotPolicy_driftChainFixture_created.policy_days_of_week
    time         = run.createSnapshotPolicy_driftChainFixture_created.policy_time
    status       = run.createSnapshotPolicy_driftChainFixture_created.policy_status
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.adopted) == 0
    )
    error_message = "The drift_deleter state still holds the policy after destroy."
  }
}

# The drift state still records the deleted policy. Refresh removes it from
# state and apply creates a new policy, so the ID changes. Teardown deletes the
# new policy. The run is not parallel: it must start after
# destroySnapshotPolicy_allResources_confirmed has deleted the policy.
run "deleteSnapshotPolicy_outsideTerraform_driftDetected" {
  command   = apply
  state_key = "drift"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-drift"
    region       = var.region
    days_of_week = ["wed"]
    time         = "06:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.id != run.createSnapshotPolicy_driftChainFixture_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.name == "tf-test-fsp-drift"
    )
    error_message = "Refresh did not detect the deleted policy, or the policy was not created again."
  }
}


# Chain replace.

# Creates the policy that the next run moves to another region.
run "createSnapshotPolicy_replaceChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "replace"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-replace"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_fs_filesystem_snapshot_policy.policy.id) > 0 &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region_name == var.region
    )
    error_message = "The policy of the replace chain was not created in the first region."
  }
}

# Changing region.name replaces the policy: the id changes and the new policy
# lives in the second region (AC4).
run "updateSnapshotPolicy_regionName_replacement" {
  command   = apply
  parallel  = true
  state_key = "replace"

  module {
    source = "./fixtures/filesystem_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-fsp-replace"
    region       = var.second_region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot_policy.policy.id != run.createSnapshotPolicy_replaceChainFixture_created.policy_id &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region.name == var.second_region &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region_name == var.second_region &&
      viettelcloud_fs_filesystem_snapshot_policy.policy.region_id != run.createSnapshotPolicy_replaceChainFixture_created.policy_region_id
    )
    error_message = "Changing region.name did not replace the policy in the second region."
  }
}
