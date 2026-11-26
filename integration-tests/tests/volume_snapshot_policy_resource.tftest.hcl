# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain required (state_key = "required"; required inputs only):
#   run "createVolumeSnapshotPolicy_requiredOnly_policyCreated" {
#   run "planVolumeSnapshotPolicy_requiredOnlyApplied_noChanges" {
#
# Chain optional (state_key = "optional"; optional inputs set, omitted, cleared,
# then set with spaces around them):
#   run "createVolumeSnapshotPolicy_withDescriptionAndRetention_policyCreated" {
#   run "omitDescriptionAndRetention_existingPolicy_valuesKept" {
#   run "setEmptyDescription_existingPolicy_descriptionCleared" {
#   run "setPaddedDescription_existingPolicy_configuredSpellingKept" {
#   run "planAgain_paddedDescription_noChanges" {
#
# Chain update (state_key = "update"; every in-place update):
#   run "createVolumeSnapshotPolicy_updateChainFixture_created" {
#   run "changeNameStatusAndRetention_existingPolicy_updatedInPlace" {
#   run "changeDaysOfWeekAndTime_existingPolicy_updatedInPlace" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# policy this chain creates for itself):
#   run "createVolumeSnapshotPolicy_importChainFixture_created" {
#   run "importVolumeSnapshotPolicy_existingPolicy_noChanges" {
#
# Chain replace (state_key = "replace"; region change replaces the policy):
#   run "createVolumeSnapshotPolicy_replaceChainFixture_created" {
#   run "changeRegion_existingPolicy_replaced" {
#
# Chain destroy (state_key = "destroy"; removing the policy from the
# configuration deletes it):
#   run "createVolumeSnapshotPolicy_destroyChainFixture_created" {
#   run "destroyVolumeSnapshotPolicy_existingPolicy_policyDeleted" {
#
# Teardown of every chain also deletes its policy. Deletion outside Terraform is
# N/A here: the unit test of Read not-found covers it. The provider diagnostics
# for an invalid schedule are N/A too, because expect_failures cannot catch
# them; unit tests cover them.

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

# Chain required.

# Creates a policy from the required inputs only (AC1). The API defaults retention
# to 1, the description is not configured, and every computed attribute must be
# known after apply.
run "createVolumeSnapshotPolicy_requiredOnly_policyCreated" {
  command   = apply
  parallel  = true
  state_key = "required"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-req"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_volume_snapshot_policy.policy.id) > 0 &&
      viettelcloud_volume_snapshot_policy.policy.name == "tf-test-vsp-req" &&
      viettelcloud_volume_snapshot_policy.policy.region.name == var.region &&
      viettelcloud_volume_snapshot_policy.policy.status == "enabled" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 1 &&
      viettelcloud_volume_snapshot_policy.policy.schedule.days_of_week == toset(["mon", "fri"]) &&
      viettelcloud_volume_snapshot_policy.policy.schedule.time == "02:00" &&
      viettelcloud_volume_snapshot_policy.policy.volume_count == 0 &&
      length(viettelcloud_volume_snapshot_policy.policy.state) > 0 &&
      length(viettelcloud_volume_snapshot_policy.policy.created_at) > 0 &&
      length(viettelcloud_volume_snapshot_policy.policy.updated_at) > 0
    )
    error_message = "The required-only policy was not created with every computed value."
  }
}

# Replanning the same configuration plans no change (AC1). A planned update would
# leave updated_at unknown and fail the assertion.
run "planVolumeSnapshotPolicy_requiredOnlyApplied_noChanges" {
  command   = plan
  parallel  = true
  state_key = "required"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-req"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_requiredOnly_policyCreated.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.updated_at == run.createVolumeSnapshotPolicy_requiredOnly_policyCreated.policy_updated_at &&
      viettelcloud_volume_snapshot_policy.policy.retention == 1
    )
    error_message = "Replanning the required-only policy planned a change."
  }
}


# Chain optional.

# Creates a policy with description and retention set (AC2). The state must match
# the configuration.
run "createVolumeSnapshotPolicy_withDescriptionAndRetention_policyCreated" {
  command   = apply
  parallel  = true
  state_key = "optional"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-opt"
    region       = var.region
    days_of_week = ["tue", "thu"]
    time         = "03:30"
    status       = "enabled"
    description  = "Policy with optional values"
    retention    = 5
  }

  assert {
    condition = (
      length(viettelcloud_volume_snapshot_policy.policy.id) > 0 &&
      viettelcloud_volume_snapshot_policy.policy.description == "Policy with optional values" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 5 &&
      viettelcloud_volume_snapshot_policy.policy.schedule.time == "03:30"
    )
    error_message = "The policy did not keep the configured description and retention."
  }
}

# Removing description and retention from the configuration keeps the values that
# the API holds (AC5), and the same id stays.
run "omitDescriptionAndRetention_existingPolicy_valuesKept" {
  command   = apply
  parallel  = true
  state_key = "optional"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-opt"
    region       = var.region
    days_of_week = ["tue", "thu"]
    time         = "03:30"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_withDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.description == "Policy with optional values" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 5
    )
    error_message = "Omitting description and retention changed or cleared them."
  }
}

# An empty description is configured, not omitted, so it clears the description
# (AC6). retention stays.
run "setEmptyDescription_existingPolicy_descriptionCleared" {
  command   = apply
  parallel  = true
  state_key = "optional"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-opt"
    region       = var.region
    days_of_week = ["tue", "thu"]
    time         = "03:30"
    status       = "enabled"
    description  = ""
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_withDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.description == "" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 5
    )
    error_message = "An empty description did not clear the description."
  }
}

# The API trims description, so state keeps the configured spelling when the
# stored value equals it after trimming. The id stays.
run "setPaddedDescription_existingPolicy_configuredSpellingKept" {
  command   = apply
  parallel  = true
  state_key = "optional"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-opt"
    region       = var.region
    days_of_week = ["tue", "thu"]
    time         = "03:30"
    status       = "enabled"
    description  = "  Policy with spaces\n"
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_withDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.description == "  Policy with spaces\n" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 5
    )
    error_message = "A description with spaces around it did not keep the configured spelling."
  }
}

# Replanning the padded description plans no change. A planned update would
# leave updated_at unknown and fail the assertion.
run "planAgain_paddedDescription_noChanges" {
  command   = plan
  parallel  = true
  state_key = "optional"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-opt"
    region       = var.region
    days_of_week = ["tue", "thu"]
    time         = "03:30"
    status       = "enabled"
    description  = "  Policy with spaces\n"
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_withDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.updated_at == run.setPaddedDescription_existingPolicy_configuredSpellingKept.policy_updated_at &&
      viettelcloud_volume_snapshot_policy.policy.description == "  Policy with spaces\n"
    )
    error_message = "Replanning the padded description planned a change."
  }
}


# Chain update.

# Creates the policy that the next two runs update.
run "createVolumeSnapshotPolicy_updateChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "update"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-upd"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
    retention    = 3
  }

  assert {
    condition = (
      length(viettelcloud_volume_snapshot_policy.policy.id) > 0 &&
      viettelcloud_volume_snapshot_policy.policy.retention == 3
    )
    error_message = "The update chain's policy was not created."
  }
}

# Changes name, status, and retention in one apply (AC3). The id must not change.
run "changeNameStatusAndRetention_existingPolicy_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "update"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-upd-renamed"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "disabled"
    retention    = 6
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_updateChainFixture_created.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.name == "tf-test-vsp-upd-renamed" &&
      viettelcloud_volume_snapshot_policy.policy.status == "disabled" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 6 &&
      viettelcloud_volume_snapshot_policy.policy.schedule.time == "02:00"
    )
    error_message = "Changing name, status, and retention replaced the policy or did not apply them."
  }
}

# Changes both schedule attributes in one apply (AC3). The provider sends the whole
# schedule. The id must not change, and the earlier changes must stay.
run "changeDaysOfWeekAndTime_existingPolicy_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "update"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-upd-renamed"
    region       = var.region
    days_of_week = ["wed", "sat", "sun"]
    time         = "23:30"
    status       = "disabled"
    retention    = 6
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id == run.createVolumeSnapshotPolicy_updateChainFixture_created.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.schedule.days_of_week == toset(["wed", "sat", "sun"]) &&
      viettelcloud_volume_snapshot_policy.policy.schedule.time == "23:30" &&
      viettelcloud_volume_snapshot_policy.policy.name == "tf-test-vsp-upd-renamed" &&
      viettelcloud_volume_snapshot_policy.policy.retention == 6
    )
    error_message = "Changing the schedule replaced the policy or did not apply it."
  }
}


# Chain import.

# Creates the policy that run
# "importVolumeSnapshotPolicy_existingPolicy_noChanges" imports. The provider
# reports time as HH:MM, so the imported state matches the configuration with
# no plan change.
run "createVolumeSnapshotPolicy_importChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-import"
    region       = var.region
    days_of_week = ["sat", "sun"]
    time         = "04:30"
    status       = "enabled"
    description  = "Policy for import"
    retention    = 4
  }

  assert {
    condition = (
      length(viettelcloud_volume_snapshot_policy.policy.id) > 0 &&
      viettelcloud_volume_snapshot_policy.policy.retention == 4
    )
    error_message = "The import chain's policy was not created with the expected values."
  }
}

# Importing the policy restores its state from the API and plans no change (AC8).
# The run stays plan-only under state_key "import_target", so the policy that
# run "createVolumeSnapshotPolicy_importChainFixture_created" owns does not gain
# a second owner. Every input comes from the state of that run. The assertions
# check the attributes that only the import refresh supplies; any planned change
# would make the computed values unknown and fail the run.
run "importVolumeSnapshotPolicy_existingPolicy_noChanges" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/volume_snapshot_policy/import"
  }

  variables {
    policy_id    = run.createVolumeSnapshotPolicy_importChainFixture_created.policy_id
    name         = run.createVolumeSnapshotPolicy_importChainFixture_created.policy_name
    region       = run.createVolumeSnapshotPolicy_importChainFixture_created.policy_region
    days_of_week = run.createVolumeSnapshotPolicy_importChainFixture_created.policy_schedule_days_of_week
    time         = run.createVolumeSnapshotPolicy_importChainFixture_created.policy_schedule_time
    status       = run.createVolumeSnapshotPolicy_importChainFixture_created.policy_status
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.imported.id == run.createVolumeSnapshotPolicy_importChainFixture_created.policy_id &&
      viettelcloud_volume_snapshot_policy.imported.description == run.createVolumeSnapshotPolicy_importChainFixture_created.policy_description &&
      viettelcloud_volume_snapshot_policy.imported.retention == run.createVolumeSnapshotPolicy_importChainFixture_created.policy_retention &&
      viettelcloud_volume_snapshot_policy.imported.state == run.createVolumeSnapshotPolicy_importChainFixture_created.policy_state &&
      viettelcloud_volume_snapshot_policy.imported.volume_count == run.createVolumeSnapshotPolicy_importChainFixture_created.policy_volume_count &&
      viettelcloud_volume_snapshot_policy.imported.created_at == run.createVolumeSnapshotPolicy_importChainFixture_created.policy_created_at &&
      length(viettelcloud_volume_snapshot_policy.imported.updated_at) > 0
    )
    error_message = "Importing the policy did not restore the state the API reports, or it planned a change."
  }
}


# Chain replace.

# Creates the policy that run "changeRegion_existingPolicy_replaced" moves to
# another region.
run "createVolumeSnapshotPolicy_replaceChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "replace"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-replace"
    region       = var.region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      length(viettelcloud_volume_snapshot_policy.policy.id) > 0 &&
      viettelcloud_volume_snapshot_policy.policy.region.name == var.region
    )
    error_message = "The policy of the replace chain was not created in the first region."
  }
}

# Changing region.name replaces the policy: the id changes and the new policy
# lives in the second region (AC4). This run is N/A when the test project has
# only one region.
run "changeRegion_existingPolicy_replaced" {
  command   = apply
  parallel  = true
  state_key = "replace"

  module {
    source = "./fixtures/volume_snapshot_policy/resource"
  }

  variables {
    name         = "tf-test-vsp-replace"
    region       = var.second_region
    days_of_week = ["mon", "fri"]
    time         = "02:00"
    status       = "enabled"
  }

  assert {
    condition = (
      viettelcloud_volume_snapshot_policy.policy.id != run.createVolumeSnapshotPolicy_replaceChainFixture_created.policy_id &&
      viettelcloud_volume_snapshot_policy.policy.region.name == var.second_region
    )
    error_message = "Changing region.name did not replace the policy in the second region."
  }
}


# Chain destroy.

# Creates the policy that run
# "destroyVolumeSnapshotPolicy_existingPolicy_policyDeleted" deletes.
run "createVolumeSnapshotPolicy_destroyChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "destroy"

  module {
    source = "./fixtures/volume_snapshot_policy/destroy"
  }

  variables {
    enabled = true
    name    = "tf-test-vsp-destroy"
    region  = var.region
  }

  assert {
    condition     = length(viettelcloud_volume_snapshot_policy.policy) == 1 && length(output.policy_id) > 0
    error_message = "The policy of the destroy chain was not created."
  }
}

# Removing the policy from the configuration deletes it (AC9). Apply succeeds
# only when DELETE succeeds, and the policy no longer appears in state.
run "destroyVolumeSnapshotPolicy_existingPolicy_policyDeleted" {
  command   = apply
  parallel  = true
  state_key = "destroy"

  module {
    source = "./fixtures/volume_snapshot_policy/destroy"
  }

  variables {
    enabled = false
    name    = "tf-test-vsp-destroy"
    region  = var.region
  }

  assert {
    condition     = length(viettelcloud_volume_snapshot_policy.policy) == 0 && output.policy_id == null
    error_message = "The policy was not deleted."
  }
}
