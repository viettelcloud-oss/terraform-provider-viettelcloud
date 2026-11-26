# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Filesystem snapshot data-source lookup chain (shared fixture state):
#   run "datasourceFilesystemSnapshot_byId_returnsCorrect" {
#   run "datasourceFilesystemSnapshot_byNameFilesystem_returnsCorrect" {
#   run "datasourceFilesystemSnapshot_nonCanonicalFilters_configuredValuesPreserved" {
#
# The name and filesystem_id lookup needs a filesystem with no other snapshot
# of that name, because the backend allows duplicate names (design doc Q2).
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

variable "filesystem_id" {
  description = "Existing filesystem that the subject snapshot belongs to."
  type        = string
}

variable "snapshot_name" {
  description = "Name of the snapshot created for lookups. It must be unique in filesystem_id."
  type        = string
  default     = "tf-test-fss-ds"
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Creates the subject and reads it back by ID. Every attribute must match the
# managed snapshot.
run "datasourceFilesystemSnapshot_byId_returnsCorrect" {
  command = apply

  module {
    source = "./fixtures/filesystem_snapshot/datasource"
  }

  variables {
    name          = var.snapshot_name
    filesystem_id = var.filesystem_id
  }

  assert {
    condition = (
      output.by_id.id == output.subject.id &&
      output.by_id.name == var.snapshot_name &&
      output.by_id.description == output.subject.description &&
      output.by_id.filesystem_name == output.subject.filesystem_name &&
      output.by_id.from_policy == false &&
      output.by_id.status == "available" &&
      output.by_id.size == output.subject.size &&
      output.by_id.owner_id == output.subject.owner_id &&
      output.by_id.project_id == output.subject.project_id &&
      output.by_id.created_at == output.subject.created_at
    )
    error_message = "The lookup by ID did not return the managed snapshot."
  }
}

# Resolves the subject by name and filesystem_id. The lookup lists snapshots
# and matches both criteria exactly.
run "datasourceFilesystemSnapshot_byNameFilesystem_returnsCorrect" {
  command = apply

  module {
    source = "./fixtures/filesystem_snapshot/datasource"
  }

  variables {
    name          = var.snapshot_name
    filesystem_id = var.filesystem_id
  }

  assert {
    condition = (
      output.by_name_and_filesystem.id == output.subject.id &&
      output.by_name_and_filesystem.name == var.snapshot_name &&
      output.by_name_and_filesystem.filesystem_id == var.filesystem_id &&
      output.by_name_and_filesystem.status == "available"
    )
    error_message = "The lookup by name and filesystem_id did not return the managed snapshot."
  }
}

# Looks the subject up with an uppercase ID and filesystem_id and a padded name.
# State keeps the configured spelling of each criterion.
run "datasourceFilesystemSnapshot_nonCanonicalFilters_configuredValuesPreserved" {
  command = apply

  module {
    source = "./fixtures/filesystem_snapshot/datasource"
  }

  variables {
    name          = var.snapshot_name
    filesystem_id = var.filesystem_id
  }

  assert {
    condition = (
      lower(output.by_all_filters.id) == output.subject.id &&
      output.by_all_filters.id == upper(output.subject.id) &&
      output.by_all_filters.name == " ${var.snapshot_name} " &&
      output.by_all_filters.filesystem_id == upper(var.filesystem_id)
    )
    error_message = "The data source did not keep the configured spelling of its criteria."
  }
}
