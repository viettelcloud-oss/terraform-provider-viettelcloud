# Test index. This file owns its source volume and backup.
# Lookup chain (shared fixture state):
#   run "lookupBackup_supportedFilters_matchingBackupReturned" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

run "lookupBackup_supportedFilters_matchingBackupReturned" {
  command = apply
  module { source = "./fixtures/volume_backup/datasource" }
  variables {
    zone        = "vn-central-1a"
    volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
  }

  assert {
    condition     = output.by_id.id == output.subject.id && output.by_id.description == output.subject.description && output.by_id.volume_name == output.subject.volume_name && output.by_id.status == output.subject.status && output.by_id.size == output.subject.size && output.by_id.expired == output.subject.expired && output.by_id.disk_type == output.subject.disk_type && output.by_id.encryption_key_size == output.subject.encryption_key_size && output.by_id.project_id == output.subject.project_id && output.by_id.region_id == output.subject.region_id && output.by_id.policy_id == output.subject.policy_id && output.by_id.created_at == output.subject.created_at && length(output.by_id.updated_at) > 0
    error_message = "The ID lookup returned incomplete backup state."
  }
  assert {
    condition     = output.by_name.id == output.subject.id && output.by_volume.id == output.subject.id && output.by_status.id == output.subject.id && output.by_size.id == output.subject.id && output.by_region.id == output.subject.id
    error_message = "A backup filter selected the wrong object."
  }
  assert {
    condition     = output.combined.id == upper(output.subject.id) && output.combined.name == " ${output.subject.name} " && output.combined.volume_id == upper(output.subject.volume.id) && output.combined.status == " ${output.subject.status} " && output.combined.region == " ${output.subject.region} "
    error_message = "Combined filters did not preserve configured values."
  }
}
