# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Volume snapshot policy data-source lookup chain (shared fixture state):
#   run "lookupVolumeSnapshotPolicy_byID_policyFound" {
#   run "lookupVolumeSnapshotPolicy_byNameAndRegion_policyFound" {
#   run "lookupVolumeSnapshotPolicy_byNameRegionAndStatus_policyFound" {
#   run "lookupVolumeSnapshotPolicy_nonCanonicalFilters_configuredValuesPreserved" {
#
# Zero, ambiguous, missing, and invalid criteria are provider diagnostics, so
# unit tests cover them.

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

variable "policy_name" {
  description = "Name of the policy created for data-source lookups. It must be unique in policy_region."
  type        = string
  default     = "tf-test-vsp-ds"
}

variable "policy_region" {
  description = "Region where the policy is created."
  type        = string
  default     = "vn-central-1"
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Creates the subject and reads it back by ID (AC12). Every attribute must match the
# managed policy.
run "lookupVolumeSnapshotPolicy_byID_policyFound" {
  command = apply

  module {
    source = "./fixtures/volume_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_volume_snapshot_policy.by_id.id == viettelcloud_volume_snapshot_policy.subject.id &&
      data.viettelcloud_volume_snapshot_policy.by_id.description == viettelcloud_volume_snapshot_policy.subject.description &&
      data.viettelcloud_volume_snapshot_policy.by_id.retention == viettelcloud_volume_snapshot_policy.subject.retention &&
      data.viettelcloud_volume_snapshot_policy.by_id.schedule.days_of_week == viettelcloud_volume_snapshot_policy.subject.schedule.days_of_week &&
      data.viettelcloud_volume_snapshot_policy.by_id.schedule.time == viettelcloud_volume_snapshot_policy.subject.schedule.time &&
      data.viettelcloud_volume_snapshot_policy.by_id.state == viettelcloud_volume_snapshot_policy.subject.state &&
      data.viettelcloud_volume_snapshot_policy.by_id.volume_count == viettelcloud_volume_snapshot_policy.subject.volume_count &&
      data.viettelcloud_volume_snapshot_policy.by_id.last_execution == viettelcloud_volume_snapshot_policy.subject.last_execution &&
      data.viettelcloud_volume_snapshot_policy.by_id.created_at == viettelcloud_volume_snapshot_policy.subject.created_at &&
      data.viettelcloud_volume_snapshot_policy.by_id.updated_at == viettelcloud_volume_snapshot_policy.subject.updated_at &&
      data.viettelcloud_volume_snapshot_policy.by_id.name == var.policy_name &&
      data.viettelcloud_volume_snapshot_policy.by_id.region == var.policy_region &&
      data.viettelcloud_volume_snapshot_policy.by_id.status == "disabled" &&
      data.viettelcloud_volume_snapshot_policy.by_id.retention == 2 &&
      data.viettelcloud_volume_snapshot_policy.by_id.schedule.time == "03:15"
    )
    error_message = "The lookup by ID did not return the managed policy."
  }
}

# Reads the same subject by name and region, which this test keeps unique (AC13).
run "lookupVolumeSnapshotPolicy_byNameAndRegion_policyFound" {
  command = apply

  module {
    source = "./fixtures/volume_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.id == viettelcloud_volume_snapshot_policy.subject.id &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.description == viettelcloud_volume_snapshot_policy.subject.description &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.retention == viettelcloud_volume_snapshot_policy.subject.retention &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.schedule.days_of_week == viettelcloud_volume_snapshot_policy.subject.schedule.days_of_week &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.schedule.time == viettelcloud_volume_snapshot_policy.subject.schedule.time &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.state == viettelcloud_volume_snapshot_policy.subject.state &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.volume_count == viettelcloud_volume_snapshot_policy.subject.volume_count &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.last_execution == viettelcloud_volume_snapshot_policy.subject.last_execution &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.created_at == viettelcloud_volume_snapshot_policy.subject.created_at &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.updated_at == viettelcloud_volume_snapshot_policy.subject.updated_at &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.name == var.policy_name &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.region == var.policy_region &&
      data.viettelcloud_volume_snapshot_policy.by_name_and_region.status == viettelcloud_volume_snapshot_policy.subject.status
    )
    error_message = "The lookup by name and region did not return the managed policy."
  }
}

# Reads the subject by name, region, and status (AC16). The status is sent to the API
# and matched exactly. A policy with another status would not match.
run "lookupVolumeSnapshotPolicy_byNameRegionAndStatus_policyFound" {
  command = apply

  module {
    source = "./fixtures/volume_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_volume_snapshot_policy.by_name_region_and_status.id == viettelcloud_volume_snapshot_policy.subject.id &&
      data.viettelcloud_volume_snapshot_policy.by_name_region_and_status.status == "disabled" &&
      data.viettelcloud_volume_snapshot_policy.by_name_region_and_status.created_at == viettelcloud_volume_snapshot_policy.subject.created_at
    )
    error_message = "The lookup by name, region, and status did not return the managed policy."
  }
}

# Looks the subject up by an uppercase ID and by name, region, and status with
# outer whitespace. The lookup matches the trimmed values, and state keeps the
# configured spelling so the data source shows no drift.
run "lookupVolumeSnapshotPolicy_nonCanonicalFilters_configuredValuesPreserved" {
  command = apply

  module {
    source = "./fixtures/volume_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_volume_snapshot_policy.by_all_filters.id == upper(viettelcloud_volume_snapshot_policy.subject.id) &&
      data.viettelcloud_volume_snapshot_policy.by_all_filters.name == " ${var.policy_name} " &&
      data.viettelcloud_volume_snapshot_policy.by_all_filters.region == " ${var.policy_region} " &&
      data.viettelcloud_volume_snapshot_policy.by_all_filters.status == " ${viettelcloud_volume_snapshot_policy.subject.status} " &&
      data.viettelcloud_volume_snapshot_policy.by_all_filters.retention == viettelcloud_volume_snapshot_policy.subject.retention &&
      data.viettelcloud_volume_snapshot_policy.by_all_filters.created_at == viettelcloud_volume_snapshot_policy.subject.created_at
    )
    error_message = "The lookup by non-canonical criteria returned another policy or did not keep the configured spelling."
  }
}
