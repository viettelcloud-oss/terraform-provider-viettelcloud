# Test index — copy the run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lookup (shared fixture state):
#   run "querySupportedFilters_isolatedVolumeFleet_matchingObjectsAndConfiguredValuesReturned" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variables {
  zone        = "vn-central-1a"
  volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

run "querySupportedFilters_isolatedVolumeFleet_matchingObjectsAndConfiguredValuesReturned" {
  command = apply

  module {
    source = "./fixtures/volume/datasource"
  }

  variables {
    volumes = {
      target = {
        name        = "tf-test-volume-datasource-target"
        description = "Target volume for data source tests"
        size        = 30
      }
      peer_a = {
        name        = "tf-test-volume-datasource-peer-a"
        description = "First peer volume"
        size        = 30
      }
      peer_b = {
        name        = "tf-test-volume-datasource-peer-b"
        description = "Second peer volume"
        size        = 40
      }
    }
    lookup_key = "target"
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 3 &&
      length(distinct([for volume in values(viettelcloud_volume.volume) : volume.id])) == 3 &&
      alltrue([
        for key, volume in viettelcloud_volume.volume : (
          data.viettelcloud_volume.by_id[key].id == volume.id &&
          data.viettelcloud_volume.by_id[key].name == volume.name &&
          data.viettelcloud_volume.by_id[key].description == volume.description &&
          data.viettelcloud_volume.by_id[key].size == volume.size &&
          data.viettelcloud_volume.by_id[key].status == volume.status &&
          data.viettelcloud_volume.by_id[key].bootable == volume.bootable &&
          data.viettelcloud_volume.by_id[key].encrypted == volume.encrypted &&
          data.viettelcloud_volume.by_id[key].zone == volume.zone.name &&
          data.viettelcloud_volume.by_id[key].zone_id == volume.zone_id &&
          data.viettelcloud_volume.by_id[key].volume_type == volume.volume_type &&
          data.viettelcloud_volume.by_id[key].iops == volume.iops &&
          length(data.viettelcloud_volume.by_id[key].created_at) > 0 &&
          length(data.viettelcloud_volume.by_id[key].updated_at) > 0
        )
      ])
    )
    error_message = "Concurrent volume lookups by ID returned incomplete state or selected the wrong fleet member."
  }

  assert {
    condition = alltrue([
      for key, volume in viettelcloud_volume.volume :
      data.viettelcloud_volume.by_name[key].id == volume.id
    ])
    error_message = "Lookup by unique exact name selected the wrong volume in a multi-volume project."
  }

  assert {
    condition     = data.viettelcloud_volume.by_size.id == viettelcloud_volume.volume["target"].id
    error_message = "Lookup by name and size returned the wrong target volume."
  }

  assert {
    condition     = data.viettelcloud_volume.by_status.id == viettelcloud_volume.volume["target"].id
    error_message = "Lookup by name and status returned the wrong target volume."
  }

  assert {
    condition     = data.viettelcloud_volume.by_bootable.id == viettelcloud_volume.volume["target"].id
    error_message = "Lookup by name and bootable returned the wrong target volume."
  }

  assert {
    condition     = data.viettelcloud_volume.by_zone.id == viettelcloud_volume.volume["target"].id
    error_message = "Lookup by name and zone returned the wrong target volume."
  }

  assert {
    condition = (
      data.viettelcloud_volume.by_all_filters.id == upper(viettelcloud_volume.volume["target"].id) &&
      data.viettelcloud_volume.by_all_filters.name == " ${viettelcloud_volume.volume["target"].name} " &&
      data.viettelcloud_volume.by_all_filters.status == " ${viettelcloud_volume.volume["target"].status} " &&
      data.viettelcloud_volume.by_all_filters.zone == " ${viettelcloud_volume.volume["target"].zone.name} "
    )
    error_message = "Combined lookup did not select the target or preserve equivalent configured representations."
  }
}
