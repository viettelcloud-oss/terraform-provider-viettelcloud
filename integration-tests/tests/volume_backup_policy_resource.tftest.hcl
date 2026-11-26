# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (state_key = "lifecycle"; required inputs only, then every
# in-place update, ending with the switch to days_of_month):
#   run "createVolumeBackupPolicy_periodicRequiredOnly_policyCreated" {
#   run "planVolumeBackupPolicy_periodicRequiredOnlyApplied_noChanges" {
#   run "changeNameStatusAndRetention_existingPolicy_updatedInPlace" {
#   run "changeFrequencyAndUnit_existingPeriodicPolicy_updatedInPlace" {
#   run "changeScheduleToDaysOfMonth_existingPolicy_updatedInPlace" {
#
# Chain full (state_key = "full"; optional inputs set, then omitted and
# cleared):
#   run "createVolumeBackupPolicy_daysOfMonthWithDescriptionAndRetention_policyCreated" {
#   run "omitDescriptionAndRetention_existingPolicy_valuesKept" {
#   run "planVolumeBackupPolicy_optionalsOmitted_noChanges" {
#   run "setEmptyDescription_existingPolicy_descriptionCleared" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# policy this chain creates for itself):
#   run "createVolumeBackupPolicy_importChainFixture_policyCreated" {
#   run "importVolumeBackupPolicy_existingPolicy_noChanges" {
#
# Chain normalized (state_key = "normalized"; untrimmed name and region, and an
# HH:MM time for the whole life of its own policy):
#   run "createVolumeBackupPolicy_untrimmedNameRegionAndTime_configuredValuesPreserved" {
#   run "planVolumeBackupPolicy_untrimmedInputs_noChanges" {
#
# Chain replace (state_key = "replace"; region change replaces the policy):
#   run "createVolumeBackupPolicy_replaceChainFixture_policyCreated" {
#   run "changeRegion_existingPolicy_replaced" {
#
# Destroy: Terraform destroys every state_key at the end of the file, and a
# destroy that fails makes the file fail. That teardown covers
# destroyVolumeBackupPolicy_existingPolicy_policyDeleted (AC9).
#
# AC7 (deleted outside Terraform), AC10, AC13, and AC14 are N/A here: unit
# tests cover them, because they are provider diagnostics or a state removal.

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

variable "first_execution" {
  description = "RFC 3339 timestamp in UTC (suffix Z) for the periodic policies. It must fall on the hour and lie in the future when the test creates a policy. Pass it with -var. Do not compute it with timestamp() or plantimestamp(), which change on every plan."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# AC1. Creates a periodic policy from the required inputs only. The backend
# defaults retention to 1 and description to an empty string, and every
# computed attribute must be known after apply.
run "createVolumeBackupPolicy_periodicRequiredOnly_policyCreated" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "tf-test-vbp-life"
    region = var.region
    status = "enabled"
    schedule = {
      execution_strategy = "periodic"
      frequency          = 2
      frequency_unit     = "day"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume_backup_policy.policy.id) > 0 &&
      viettelcloud_volume_backup_policy.policy.name == "tf-test-vbp-life" &&
      viettelcloud_volume_backup_policy.policy.region.name == var.region &&
      viettelcloud_volume_backup_policy.policy.status == "enabled" &&
      viettelcloud_volume_backup_policy.policy.retention == 1 &&
      viettelcloud_volume_backup_policy.policy.schedule.execution_strategy == "periodic" &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency == 2 &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency_unit == "day" &&
      viettelcloud_volume_backup_policy.policy.schedule.first_execution == var.first_execution &&
      viettelcloud_volume_backup_policy.policy.schedule.days_of_month == null &&
      viettelcloud_volume_backup_policy.policy.schedule.time == null &&
      viettelcloud_volume_backup_policy.policy.volume_count == 0 &&
      length(viettelcloud_volume_backup_policy.policy.state) > 0 &&
      length(viettelcloud_volume_backup_policy.policy.created_at) > 0 &&
      length(viettelcloud_volume_backup_policy.policy.updated_at) > 0
    )
    error_message = "The periodic policy was not created from the required inputs, or a computed value is missing."
  }
}

# AC1. Replanning the same configuration plans no change. A planned update
# would leave updated_at unknown and fail the assertion.
run "planVolumeBackupPolicy_periodicRequiredOnlyApplied_noChanges" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "tf-test-vbp-life"
    region = var.region
    status = "enabled"
    schedule = {
      execution_strategy = "periodic"
      frequency          = 2
      frequency_unit     = "day"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_periodicRequiredOnly_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.updated_at == run.createVolumeBackupPolicy_periodicRequiredOnly_policyCreated.policy_updated_at &&
      viettelcloud_volume_backup_policy.policy.retention == 1
    )
    error_message = "Replanning the periodic policy planned a change."
  }
}

# AC3. Changes name, status, and retention in place with one PATCH. The
# schedule stays as it is, so the request must not carry it.
run "changeNameStatusAndRetention_existingPolicy_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name      = "tf-test-vbp-renamed"
    region    = var.region
    status    = "disabled"
    retention = 3
    schedule = {
      execution_strategy = "periodic"
      frequency          = 2
      frequency_unit     = "day"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_periodicRequiredOnly_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.name == "tf-test-vbp-renamed" &&
      viettelcloud_volume_backup_policy.policy.status == "disabled" &&
      viettelcloud_volume_backup_policy.policy.retention == 3 &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency == 2
    )
    error_message = "Changing name, status, or retention replaced the policy or did not apply the values."
  }
}

# AC3. Changes one periodic field of schedule. The provider sends the whole
# schedule, including the unchanged first_execution, which the backend does
# not check again.
run "changeFrequencyAndUnit_existingPeriodicPolicy_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name      = "tf-test-vbp-renamed"
    region    = var.region
    status    = "disabled"
    retention = 3
    schedule = {
      execution_strategy = "periodic"
      frequency          = 1
      frequency_unit     = "week"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_periodicRequiredOnly_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.schedule.execution_strategy == "periodic" &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency == 1 &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency_unit == "week" &&
      viettelcloud_volume_backup_policy.policy.schedule.first_execution == var.first_execution
    )
    error_message = "Changing frequency or frequency_unit replaced the policy or did not apply the values."
  }
}

# AC3. Switches the schedule from periodic to days_of_month in place. The
# backend drops the periodic fields, and the state keeps the configured
# spelling 07:30 although the backend reports 07:30:00. This run ends the chain.
run "changeScheduleToDaysOfMonth_existingPolicy_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name      = "tf-test-vbp-renamed"
    region    = var.region
    status    = "disabled"
    retention = 3
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [1, 15]
      time               = "07:30"
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_periodicRequiredOnly_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.schedule.execution_strategy == "days_of_month" &&
      viettelcloud_volume_backup_policy.policy.schedule.days_of_month == toset([1, 15]) &&
      viettelcloud_volume_backup_policy.policy.schedule.time == "07:30" &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency == null &&
      viettelcloud_volume_backup_policy.policy.schedule.frequency_unit == null &&
      viettelcloud_volume_backup_policy.policy.schedule.first_execution == null
    )
    error_message = "Switching the execution strategy replaced the policy or did not drop the periodic fields."
  }
}

# AC2. Creates a days_of_month policy with description and retention. The
# state keeps the configured time 07:30 although the backend reports 07:30:00.
run "createVolumeBackupPolicy_daysOfMonthWithDescriptionAndRetention_policyCreated" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name        = "tf-test-vbp-full"
    region      = var.region
    status      = "enabled"
    description = "Monthly policy"
    retention   = 5
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [1, 15]
      time               = "07:30"
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume_backup_policy.policy.id) > 0 &&
      viettelcloud_volume_backup_policy.policy.description == "Monthly policy" &&
      viettelcloud_volume_backup_policy.policy.retention == 5 &&
      viettelcloud_volume_backup_policy.policy.schedule.execution_strategy == "days_of_month" &&
      viettelcloud_volume_backup_policy.policy.schedule.days_of_month == toset([1, 15]) &&
      viettelcloud_volume_backup_policy.policy.schedule.time == "07:30" &&
      viettelcloud_volume_backup_policy.policy.schedule.first_execution == null
    )
    error_message = "The state does not match the configured description, retention, or days_of_month schedule."
  }
}

# AC5. Omitting description and retention keeps the backend values. The
# PATCH omits both fields, so the policy keeps them.
run "omitDescriptionAndRetention_existingPolicy_valuesKept" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "tf-test-vbp-full"
    region = var.region
    status = "enabled"
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [1, 15]
      time               = "07:30"
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_daysOfMonthWithDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.description == "Monthly policy" &&
      viettelcloud_volume_backup_policy.policy.retention == 5
    )
    error_message = "Omitting description and retention changed the policy identity or the stored values."
  }
}

# AC5. Replanning with description and retention omitted plans no change.
run "planVolumeBackupPolicy_optionalsOmitted_noChanges" {
  command   = plan
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "tf-test-vbp-full"
    region = var.region
    status = "enabled"
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [1, 15]
      time               = "07:30"
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_daysOfMonthWithDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.updated_at == run.omitDescriptionAndRetention_existingPolicy_valuesKept.policy_updated_at &&
      viettelcloud_volume_backup_policy.policy.retention == 5
    )
    error_message = "Replanning with omitted optional inputs planned a change."
  }
}

# AC6. An explicit empty description clears it. This run ends the chain.
run "setEmptyDescription_existingPolicy_descriptionCleared" {
  command   = apply
  parallel  = true
  state_key = "full"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name        = "tf-test-vbp-full"
    region      = var.region
    status      = "enabled"
    description = ""
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [1, 15]
      time               = "07:30"
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_daysOfMonthWithDescriptionAndRetention_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.description == "" &&
      viettelcloud_volume_backup_policy.policy.retention == 5
    )
    error_message = "Clearing description replaced the policy or did not persist an empty value."
  }
}

# Creates the policy that the next run imports.
run "createVolumeBackupPolicy_importChainFixture_policyCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name        = "tf-test-vbp-import"
    region      = var.region
    status      = "enabled"
    description = "Policy for import"
    retention   = 4
    schedule = {
      execution_strategy = "periodic"
      frequency          = 3
      frequency_unit     = "day"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume_backup_policy.policy.id) > 0 &&
      viettelcloud_volume_backup_policy.policy.retention == 4
    )
    error_message = "The import chain policy was not created with the expected values."
  }
}

# AC8. Importing the policy restores its state from the API and plans no
# change. The run stays plan-only under its own state key, so the policy that
# the previous run owns does not gain a second owner. Every input comes from
# the creating run's state. The assertions check the attributes that only the
# import refresh supplies; any planned change would make the computed values
# unknown and fail the run.
run "importVolumeBackupPolicy_existingPolicy_noChanges" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/volume_backup_policy/import"
  }

  variables {
    policy_id = run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_id
    name      = run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_name
    region    = run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_region
    status    = run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_status
    schedule  = run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_schedule
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.imported.id == run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.imported.description == run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_description &&
      viettelcloud_volume_backup_policy.imported.retention == run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_retention &&
      viettelcloud_volume_backup_policy.imported.volume_count == run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_volume_count &&
      viettelcloud_volume_backup_policy.imported.state == run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_state &&
      viettelcloud_volume_backup_policy.imported.created_at == run.createVolumeBackupPolicy_importChainFixture_policyCreated.policy_created_at &&
      length(viettelcloud_volume_backup_policy.imported.updated_at) > 0
    )
    error_message = "Importing the policy did not restore the state the API reports, or it planned a change."
  }
}

# The provider trims name and region before each request, and the backend
# reports time as HH:MM:SS. State keeps the configured spelling. A change of
# only outer whitespace in region does not replace the policy.
run "createVolumeBackupPolicy_untrimmedNameRegionAndTime_configuredValuesPreserved" {
  command   = apply
  parallel  = true
  state_key = "normalized"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "  tf-test-vbp-norm  "
    region = "  ${var.region}  "
    status = "enabled"
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [28, 2]
      time               = "05:00"
    }
  }

  # In this block var.region is the padded value that variables sets above.
  assert {
    condition = (
      length(viettelcloud_volume_backup_policy.policy.id) > 0 &&
      viettelcloud_volume_backup_policy.policy.name == "  tf-test-vbp-norm  " &&
      viettelcloud_volume_backup_policy.policy.region.name == var.region &&
      viettelcloud_volume_backup_policy.policy.schedule.time == "05:00" &&
      viettelcloud_volume_backup_policy.policy.schedule.days_of_month == toset([2, 28])
    )
    error_message = "The configured spelling of name, region, or time was not preserved."
  }
}

# Replanning the configured spelling plans no change and no replacement.
run "planVolumeBackupPolicy_untrimmedInputs_noChanges" {
  command   = plan
  parallel  = true
  state_key = "normalized"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "  tf-test-vbp-norm  "
    region = "  ${var.region}  "
    status = "enabled"
    schedule = {
      execution_strategy = "days_of_month"
      days_of_month      = [28, 2]
      time               = "05:00"
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id == run.createVolumeBackupPolicy_untrimmedNameRegionAndTime_configuredValuesPreserved.policy_id &&
      viettelcloud_volume_backup_policy.policy.updated_at == run.createVolumeBackupPolicy_untrimmedNameRegionAndTime_configuredValuesPreserved.policy_updated_at &&
      viettelcloud_volume_backup_policy.policy.name == "  tf-test-vbp-norm  "
    )
    error_message = "The untrimmed inputs caused drift or a replacement."
  }
}

# Creates the policy that the next run moves to another region.
run "createVolumeBackupPolicy_replaceChainFixture_policyCreated" {
  command   = apply
  parallel  = true
  state_key = "replace"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "tf-test-vbp-replace"
    region = var.region
    status = "enabled"
    schedule = {
      execution_strategy = "periodic"
      frequency          = 2
      frequency_unit     = "day"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume_backup_policy.policy.id) > 0 &&
      viettelcloud_volume_backup_policy.policy.region.name == var.region
    )
    error_message = "The policy of the replace chain was not created in the first region."
  }
}

# AC4. Changing region.name replaces the policy: the id changes and the new
# policy lives in the second region. first_execution must still lie in the
# future, because the backend checks it again at creation.
run "changeRegion_existingPolicy_replaced" {
  command   = apply
  parallel  = true
  state_key = "replace"

  module {
    source = "./fixtures/volume_backup_policy/resource"
  }

  variables {
    name   = "tf-test-vbp-replace"
    region = var.second_region
    status = "enabled"
    schedule = {
      execution_strategy = "periodic"
      frequency          = 2
      frequency_unit     = "day"
      first_execution    = var.first_execution
    }
  }

  assert {
    condition = (
      viettelcloud_volume_backup_policy.policy.id != run.createVolumeBackupPolicy_replaceChainFixture_policyCreated.policy_id &&
      viettelcloud_volume_backup_policy.policy.region.name == var.second_region
    )
    error_message = "Changing region.name did not replace the policy in the second region."
  }
}
