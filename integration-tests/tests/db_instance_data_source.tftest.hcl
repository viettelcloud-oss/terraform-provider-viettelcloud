# Test index — copy a run declaration below and Find Next to jump to its block.
# This file owns its network and DB instance. Set db_root_password, for example
# as TF_VAR_db_root_password, and the catalog values in the variables block.
#
# Chain network (shared setup; state_key = "network"):
#   run "createNetwork_dbInstanceLookup_subnetReturned" {
#
# Chain lookup (state_key = "lookup"):
#   run "lookupDbInstance_idAndName_matchingInstanceReturned" {

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

run "createNetwork_dbInstanceLookup_subnetReturned" {
  command   = apply
  state_key = "network"
  module { source = "./fixtures/db_instance/network" }
  variables {
    name = "tf-test-db-instance-lookup-network"
    cidr = "10.42.0.0/16"
  }
  assert {
    condition     = length(output.subnet_id) > 0
    error_message = "The lookup subnet was not created."
  }
}

run "lookupDbInstance_idAndName_matchingInstanceReturned" {
  command   = apply
  state_key = "lookup"
  module { source = "./fixtures/db_instance/datasource" }
  variables {
    subnet_id     = run.createNetwork_dbInstanceLookup_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      output.by_id.id == output.subject.id &&
      output.by_id.name == output.subject.name &&
      output.by_id.description == output.subject.description &&
      output.by_id.database_type == output.subject.database_type &&
      output.by_id.db_version == output.subject.db_version.version &&
      output.by_id.db_version_id == output.subject.db_version.id &&
      output.by_id.flavor == output.subject.flavor.name &&
      output.by_id.flavor_id == output.subject.flavor.id &&
      output.by_id.volume_type == output.subject.volume_type.name &&
      output.by_id.volume_type_id == output.subject.volume_type.id &&
      output.by_id.region == output.subject.region.name &&
      output.by_id.region_id == output.subject.region_id &&
      output.by_id.subnet_id == output.subject.subnet.id &&
      output.by_id.current_disk_size == output.subject.current_disk_size &&
      output.by_id.solution == output.subject.solution &&
      output.by_id.keep_backup == output.subject.keep_backup &&
      output.by_id.backup_policy == output.subject.backup_policy &&
      output.by_id.auto_increase_volume_size == output.subject.auto_increase_volume_size &&
      output.by_id.maintenance_config == output.subject.maintenance_config &&
      output.by_id.project_id == output.subject.project_id &&
      output.by_id.created_at == output.subject.created_at
    )
    error_message = "The ID lookup returned incomplete DB instance state."
  }
  assert {
    condition     = output.by_name.id == output.subject.id && output.by_name.current_disk_size == output.subject.current_disk_size
    error_message = "The name lookup selected the wrong DB instance."
  }
  assert {
    condition     = output.by_upper_id.id == upper(output.subject.id) && output.by_padded_name.name == " ${output.subject.name} " && output.by_padded_name.id == output.subject.id
    error_message = "The lookup did not preserve the configured id or name."
  }
}
