# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Filesystem snapshot policy data-source lookup chain (shared fixture state):
#   run "datasourceSnapshotPolicy_byId_returnsCorrect" {
#   run "datasourceSnapshotPolicy_byNameRegion_returnsCorrect" {
#   run "datasourceSnapshotPolicy_nonCanonicalFilters_configuredValuesPreserved" {
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
  default     = "tf-test-fsp-ds"
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

# Creates the subject and reads it back by ID. Every attribute must match the
# managed policy.
run "datasourceSnapshotPolicy_byId_returnsCorrect" {
  command = apply

  module {
    source = "./fixtures/filesystem_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.id == viettelcloud_fs_filesystem_snapshot_policy.subject.id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.name == var.policy_name &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.region == var.policy_region &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.description == viettelcloud_fs_filesystem_snapshot_policy.subject.description &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.days_of_week == viettelcloud_fs_filesystem_snapshot_policy.subject.days_of_week &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.time == viettelcloud_fs_filesystem_snapshot_policy.subject.time &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.retention == 2 &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.status == "disabled" &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.filesystem_count == viettelcloud_fs_filesystem_snapshot_policy.subject.filesystem_count &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.state == viettelcloud_fs_filesystem_snapshot_policy.subject.state &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.region_id == viettelcloud_fs_filesystem_snapshot_policy.subject.region_id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.region_name == viettelcloud_fs_filesystem_snapshot_policy.subject.region_name &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.project_id == viettelcloud_fs_filesystem_snapshot_policy.subject.project_id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.owner_id == viettelcloud_fs_filesystem_snapshot_policy.subject.owner_id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_id.created_at == viettelcloud_fs_filesystem_snapshot_policy.subject.created_at
    )
    error_message = "The lookup by ID did not return the managed policy."
  }
}

# Reads the same subject by name and region, which this test keeps unique.
run "datasourceSnapshotPolicy_byNameRegion_returnsCorrect" {
  command = apply

  module {
    source = "./fixtures/filesystem_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.id == viettelcloud_fs_filesystem_snapshot_policy.subject.id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.description == viettelcloud_fs_filesystem_snapshot_policy.subject.description &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.days_of_week == viettelcloud_fs_filesystem_snapshot_policy.subject.days_of_week &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.time == viettelcloud_fs_filesystem_snapshot_policy.subject.time &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.retention == viettelcloud_fs_filesystem_snapshot_policy.subject.retention &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.status == viettelcloud_fs_filesystem_snapshot_policy.subject.status &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.filesystem_count == viettelcloud_fs_filesystem_snapshot_policy.subject.filesystem_count &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.state == viettelcloud_fs_filesystem_snapshot_policy.subject.state &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.region_id == viettelcloud_fs_filesystem_snapshot_policy.subject.region_id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.region_name == viettelcloud_fs_filesystem_snapshot_policy.subject.region_name &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.project_id == viettelcloud_fs_filesystem_snapshot_policy.subject.project_id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.owner_id == viettelcloud_fs_filesystem_snapshot_policy.subject.owner_id &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.created_at == viettelcloud_fs_filesystem_snapshot_policy.subject.created_at &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.name == var.policy_name &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_name_and_region.region == var.policy_region
    )
    error_message = "The lookup by name and region did not return the managed policy."
  }
}

# Looks the subject up by an uppercase ID and by name and region with outer
# whitespace. The lookup matches the trimmed values, and state keeps the
# configured spelling so the data source shows no drift.
run "datasourceSnapshotPolicy_nonCanonicalFilters_configuredValuesPreserved" {
  command = apply

  module {
    source = "./fixtures/filesystem_snapshot_policy/datasource"
  }

  variables {
    policy_name   = var.policy_name
    policy_region = var.policy_region
  }

  assert {
    condition = (
      data.viettelcloud_fs_filesystem_snapshot_policy.by_all_filters.id == upper(viettelcloud_fs_filesystem_snapshot_policy.subject.id) &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_all_filters.name == " ${var.policy_name} " &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_all_filters.region == " ${var.policy_region} " &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_all_filters.region_name == var.policy_region &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_all_filters.retention == viettelcloud_fs_filesystem_snapshot_policy.subject.retention &&
      data.viettelcloud_fs_filesystem_snapshot_policy.by_all_filters.created_at == viettelcloud_fs_filesystem_snapshot_policy.subject.created_at
    )
    error_message = "The lookup by non-canonical criteria returned another policy or did not keep the configured spelling."
  }
}
