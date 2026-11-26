# Test index — copy a run declaration below and Find Next to jump to its block.
# This file owns its network, DB instance, and backup. Set db_root_password,
# for example as TF_VAR_db_root_password, and the catalog values in the
# variables block.
#
# Chain network (shared setup; state_key = "network"):
#   run "createNetwork_dbBackupLookup_subnetReturned" {
#
# Chain lookup (state_key = "lookup"):
#   run "lookupBackup_supportedFilters_matchingBackupReturned" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

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

variable "db_root_password" {
  type        = string
  sensitive   = true
  description = "Root password for the test DB instance: 8 to 32 characters with lower and upper case letters, a digit, and a symbol other than @."
}

variables {
  region        = "vn-central-1"
  database_type = "postgresql"
  db_version    = "16"
  flavor        = "m1.small"
  volume_type   = "ssd"
  solution      = "basic"
}

run "createNetwork_dbBackupLookup_subnetReturned" {
  command   = apply
  state_key = "network"
  module { source = "./fixtures/db_instance/network" }
  variables {
    name = "tf-test-db-backup-lookup-network"
    cidr = "10.44.0.0/16"
  }
  assert {
    condition     = length(output.subnet_id) > 0
    error_message = "The lookup subnet was not created."
  }
}

run "lookupBackup_supportedFilters_matchingBackupReturned" {
  command   = apply
  state_key = "lookup"
  module { source = "./fixtures/db_backup/datasource" }
  variables {
    subnet_id     = run.createNetwork_dbBackupLookup_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      output.by_id.id == output.subject.id &&
      output.by_id.name == output.subject.name &&
      output.by_id.description == output.subject.description &&
      output.by_id.db_instance_id == output.subject.db_instance.id &&
      output.by_id.db_instance_name == output.subject.db_instance_name &&
      output.by_id.backup_type == "manual" &&
      output.by_id.status == output.subject.status &&
      output.by_id.database_type == output.subject.database_type &&
      output.by_id.db_version == output.subject.db_version &&
      output.by_id.db_version_id == output.subject.db_version_id &&
      output.by_id.region == output.subject.region &&
      output.by_id.region_id == output.subject.region_id &&
      output.by_id.size == output.subject.size &&
      output.by_id.required_volume_size == output.subject.required_volume_size &&
      output.by_id.backup_timestamp == output.subject.backup_timestamp &&
      output.by_id.owner_id == output.subject.owner_id &&
      output.by_id.project_id == output.subject.project_id &&
      output.by_id.created_at == output.subject.created_at
    )
    error_message = "The ID lookup returned incomplete backup state."
  }
  assert {
    condition     = output.unique.id == output.subject.id && output.combined.id == output.subject.id
    error_message = "A backup filter selected the wrong object."
  }
  assert {
    condition     = output.combined.name == " ${output.subject.name} " && output.combined.db_instance_id == upper(output.subject.db_instance.id)
    error_message = "Combined filters did not preserve configured values."
  }
}
