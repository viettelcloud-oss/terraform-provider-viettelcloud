# Test index. Source state_key "sources" owns one bootable volume, created first.
# Subject state_key "subject" owns its image and depends on that volume.
# Each lookup uses its own state_key and depends on the subject.
# Terraform removes the image before removing the source volume.
# Chain state_key = "sources":
# run "createVolumes_customImageSources_bootableVolumesAvailable" {
# Chain state_key = "subject":
# run "createCustomImage_lookupSubject_imageCreated" {
# Lookup chains (one state_key per criterion):
# run "lookupCustomImage_idCriterion_fullStateReturned" {
# run "lookupCustomImage_nameCriterion_fullStateReturned" {
# run "lookupCustomImage_volumeCriterion_fullStateReturned" {
# run "lookupCustomImage_volumeIdCriterion_fullStateReturned" {
# run "lookupCustomImage_regionCriterion_fullStateReturned" {
# run "lookupCustomImage_osCriterion_fullStateReturned" {
# run "lookupCustomImage_versionCriterion_fullStateReturned" {
# run "lookupCustomImage_stateCriterion_fullStateReturned" {
# run "lookupCustomImage_statusCriterion_fullStateReturned" {
# run "lookupCustomImage_combinedCriteria_fullStateReturned" {

variable "token" {
  type      = string
  sensitive = true
}
variable "project_id" { type = string }
variable "api_endpoint" { type = string }
variable "name_prefix" {
  type        = string
  default     = "tf-test-custom-image"
  description = "Prefix for test-owned custom image and source volume names."
}

variable "image_name" {
  type        = string
  default     = "cirros"
  description = "Unique catalog image name used to create the bootable source volumes."
}
variable "zone" {
  type        = string
  default     = "vn-central-1a"
  description = "Zone where the source volumes are created."
}
variable "volume_size" {
  type        = number
  default     = 30
  description = "Source volume size in GiB. It must satisfy the image minimum and volume type constraints."
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}
run "createVolumes_customImageSources_bootableVolumesAvailable" {
  command   = apply
  state_key = "sources"
  parallel  = true
  module { source = "./fixtures/custom_image/source" }
  variables {
    source_keys = ["lookup"]
    name_prefix = "${var.name_prefix}-lookup-source"
    image_name  = var.image_name
    volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
    zone        = var.zone
    volume_size = var.volume_size
  }
  assert {
    condition = (
      length(output.volumes) == 1 &&
      length(distinct([for volume in values(output.volumes) : volume.id])) == 1 &&
      alltrue([for volume in values(output.volumes) : (
        can(regex("^[0-9a-f-]{36}$", volume.id)) &&
        volume.status == "available" && volume.bootable &&
        volume.size == var.volume_size && volume.zone.name == var.zone &&
        length(volume.volume_type) > 0 && volume.create_from.volume_type.id != null &&
        volume.create_from.volume_type == merge(var.volume_type, { id = volume.create_from.volume_type.id }) &&
        volume.create_from.source_type == "image" &&
        volume.create_from.image.name == var.image_name
      )])
    )
    error_message = "The test-owned source volumes must be distinct, available, and bootable."
  }
}


run "createCustomImage_lookupSubject_imageCreated" {
  command   = apply
  state_key = "subject"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name          = "${var.name_prefix}-lookup"
    volume        = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["lookup"].name
    description   = "lookup subject"
    os            = "Linux"
    image_version = "test-1"
    metadata      = jsonencode({ terraform_test = "lookup" })
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.status == "active" &&
      viettelcloud_custom_image.image.volume.id == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["lookup"].id
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_idCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_id"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    id = run.createCustomImage_lookupSubject_imageCreated.image.id
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_nameCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_name"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    name = " ${run.createCustomImage_lookupSubject_imageCreated.image.name} "
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_volumeCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_volume"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    volume = " ${run.createCustomImage_lookupSubject_imageCreated.image.volume.name} "
    name   = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_volumeIdCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_volume_id"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    volume_id = run.createCustomImage_lookupSubject_imageCreated.image.volume.id
    name      = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_regionCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_region"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    region = " ${run.createCustomImage_lookupSubject_imageCreated.image.region} "
    name   = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_osCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_os"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    os   = " ${run.createCustomImage_lookupSubject_imageCreated.image.os} "
    name = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_versionCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_version"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    image_version = " ${run.createCustomImage_lookupSubject_imageCreated.image.version} "
    name          = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_stateCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_state"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    state = run.createCustomImage_lookupSubject_imageCreated.image.state
    name  = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_statusCriterion_fullStateReturned" {
  command   = apply
  state_key = "lookup_status"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    status = run.createCustomImage_lookupSubject_imageCreated.image.status
    name   = run.createCustomImage_lookupSubject_imageCreated.image.name
  }
  assert {
    condition = (
      data.viettelcloud_custom_image.image.id == run.createCustomImage_lookupSubject_imageCreated.image.id && { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "lookupCustomImage_combinedCriteria_fullStateReturned" {
  command   = apply
  state_key = "combined"
  parallel  = true
  module { source = "./fixtures/custom_image/datasource" }
  variables {
    id            = run.createCustomImage_lookupSubject_imageCreated.image.id
    name          = run.createCustomImage_lookupSubject_imageCreated.image.name
    volume        = " ${run.createCustomImage_lookupSubject_imageCreated.image.volume.name} "
    volume_id     = run.createCustomImage_lookupSubject_imageCreated.image.volume.id
    region        = " ${run.createCustomImage_lookupSubject_imageCreated.image.region} "
    os            = " ${run.createCustomImage_lookupSubject_imageCreated.image.os} "
    image_version = " ${run.createCustomImage_lookupSubject_imageCreated.image.version} "
    state         = run.createCustomImage_lookupSubject_imageCreated.image.state
    status        = run.createCustomImage_lookupSubject_imageCreated.image.status
  }
  assert {
    condition = (
      { for k, v in data.viettelcloud_custom_image.image : k => v if !contains(["name", "volume", "region", "os", "version"], k) } == { for k, v in run.createCustomImage_lookupSubject_imageCreated.image_as_data_source : k => v if !contains(["name", "volume", "region", "os", "version"], k) } && alltrue([for key in ["name", "volume", "region", "os", "version"] : try(trimspace(data.viettelcloud_custom_image.image[key]), "") == try(trimspace(run.createCustomImage_lookupSubject_imageCreated.image_as_data_source[key]), "")])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}
