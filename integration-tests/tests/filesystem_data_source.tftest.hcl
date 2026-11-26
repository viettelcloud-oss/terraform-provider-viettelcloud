# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain lookup (shared fixture state):
#   run "querySupportedFilters_isolatedFilesystem_matchingObjectAndConfiguredValuesReturned" {

variable "api_endpoint" {
  type = string
}

variable "token" {
  type      = string
  sensitive = true
}

variable "project_id" {
  type = string
}

# Existing subnet whose zone carries the filesystem type named by
# filesystem_type_name. Set via TF_VAR_subnet_id.
variable "subnet_id" {
  type        = string
  description = "UUID of an existing subnet for the filesystem data source test."
}

# Enabled filesystem type in zone_name that the NFS filesystems use. Set via
# TF_VAR_filesystem_type_name.
variable "filesystem_type_name" {
  type        = string
  description = "Name of an enabled filesystem type in zone_name."
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variables {
  zone_name = "vn-central-1a"
}

# Create one filesystem and verify every supported lookup criterion. The
# filesystem name includes a unique token so the name+zone and name+subnet_id
# lookups are guaranteed to match exactly one object.
run "querySupportedFilters_isolatedFilesystem_matchingObjectAndConfiguredValuesReturned" {
  command = apply
  module { source = "./fixtures/filesystem/datasource" }
  variables {
    name      = "tf-test-fs-datasource"
    subnet_id = var.subnet_id
  }

  # Assert direct ID lookup returns every attribute of the filesystem.
  assert {
    condition = (
      data.viettelcloud_fs_filesystem.by_id.id == output.filesystem_id &&
      data.viettelcloud_fs_filesystem.by_id.name == "tf-test-fs-datasource" &&
      data.viettelcloud_fs_filesystem.by_id.protocol == "NFS" &&
      data.viettelcloud_fs_filesystem.by_id.size == 100 &&
      data.viettelcloud_fs_filesystem.by_id.status == output.filesystem_status &&
      data.viettelcloud_fs_filesystem.by_id.zone == output.filesystem_zone_name &&
      length(data.viettelcloud_fs_filesystem.by_id.zone_id) > 0 &&
      length(data.viettelcloud_fs_filesystem.by_id.subnet_id) > 0 &&
      length(data.viettelcloud_fs_filesystem.by_id.mount_target_address) > 0 &&
      length(data.viettelcloud_fs_filesystem.by_id.project_id) > 0 &&
      length(data.viettelcloud_fs_filesystem.by_id.created_at) > 0 &&
      length(data.viettelcloud_fs_filesystem.by_id.updated_at) > 0
    )
    error_message = "Filesystem lookup by ID returned unexpected values."
  }

  # Assert name + zone lookup selects the correct filesystem.
  assert {
    condition = (
      data.viettelcloud_fs_filesystem.by_name_zone.id == output.filesystem_id &&
      data.viettelcloud_fs_filesystem.by_name_zone.name == "tf-test-fs-datasource" &&
      data.viettelcloud_fs_filesystem.by_name_zone.zone == output.filesystem_zone_name
    )
    error_message = "Filesystem lookup by name and zone returned unexpected values."
  }

  # Assert name + subnet_id lookup selects the correct filesystem.
  assert {
    condition = (
      data.viettelcloud_fs_filesystem.by_name_subnet.id == output.filesystem_id &&
      data.viettelcloud_fs_filesystem.by_name_subnet.name == "tf-test-fs-datasource" &&
      data.viettelcloud_fs_filesystem.by_name_subnet.subnet_id == var.subnet_id
    )
    error_message = "Filesystem lookup by name and subnet_id returned unexpected values."
  }
}
