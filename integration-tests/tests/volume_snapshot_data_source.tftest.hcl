# Test index. Copy the run declaration below to find its block.
# Chain lookup (shared fixture state):
#   run "querySnapshot_createdSubject_allFiltersAndFieldsReturned" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variables {
  zone        = "vn-central-1a"
  volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

run "querySnapshot_createdSubject_allFiltersAndFieldsReturned" {
  command = apply
  module { source = "./fixtures/volume_snapshot/datasource" }
  assert {
    condition = (
      output.by_id.id == output.subject.id &&
      output.by_id.name == output.subject.name &&
      output.by_id.description == output.subject.description &&
      output.by_id.volume_id == output.subject.volume.id &&
      output.by_id.volume_name == output.subject.volume_name &&
      output.by_id.size == output.subject.size &&
      output.by_id.status == output.subject.status &&
      output.by_id.from_policy == output.subject.from_policy &&
      output.by_id.project_id == output.subject.project_id &&
      output.by_id.zone == output.subject.zone &&
      output.by_id.zone_id == output.subject.zone_id &&
      output.by_id.created_at == output.subject.created_at &&
      output.by_id.updated_at == output.subject.updated_at &&
      output.by_name.id == output.subject.id &&
      output.by_volume.id == output.subject.id &&
      output.by_zone_and_status.id == output.subject.id
    )
    error_message = "Snapshot lookups returned the wrong object or incomplete fields."
  }
  assert {
    condition = (
      output.by_all.id == upper(output.subject.id) &&
      output.by_all.name == " ${output.subject.name} " &&
      output.by_all.volume_id == upper(output.subject.volume.id) &&
      output.by_all.zone == " ${output.subject.zone} " &&
      output.by_all.status == " available "
    )
    error_message = "The combined lookup did not preserve configured values."
  }
}
