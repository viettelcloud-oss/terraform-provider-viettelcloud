# Test index. Source volumes belong to state_key "sources", created first.
# The lifecycle and import chains use separate bootable volumes from that state.
# Terraform removes both image states before removing the source volumes.
# import_target only plans import and does not own a second copy of the image.
# Non-empty sharing needs custom_image_shared_project_id. With null, only empty
# sharing and omission preservation are exercised.
# container_format replacement is N/A because the SDK exposes only bare.
# Chain state_key = "sources":
# run "createVolumes_customImageSources_bootableVolumesAvailable" {
# Chain state_key = "lifecycle":
# run "createCustomImage_optionalInputsOmitted_completeStateReturned" {
# run "changeMutableFields_existingImage_updatedInPlace" {
# run "omitMutableFields_valuesSet_valuesPreservedInPlace" {
# run "planAgain_mutableFieldsOmitted_noDrift" {
# run "clearMutableFields_valuesSet_valuesEmptyInPlace" {
# run "changeState_existingImage_disabledInPlace" {
# run "changeState_disabledImage_enabledInPlace" {
# run "changeName_whitespaceConfigured_representationPreserved" {
# run "planAgain_whitespaceConfigured_noDrift" {
# run "changeDiskFormat_existingImage_imageReplaced" {
# run "changeVolume_existingImage_imageReplaced" {
# Chain state_key = "import_source":
# run "createCustomImage_volumeIdConfigured_imageCreated" {
# run "changeDescription_volumeIdConfigured_updatedInPlace" {
# run "planAgain_volumeIdConfigured_noDrift" {
# run "changeVolumeId_existingImage_imageReplaced" {
# Chain state_key = "import_target":
# run "planImport_volumeBackedImage_fullStateRestored" {

variable "token" {
  type      = string
  sensitive = true
}
variable "project_id" { type = string }
variable "api_endpoint" { type = string }
variable "custom_image_shared_project_id" {
  type        = string
  default     = null
  description = "Optional project UUID used to test sharing. The test account must have sharing permission."
}
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
    source_keys = ["primary", "replacement", "import", "importReplacement"]
    name_prefix = "${var.name_prefix}-source"
    image_name  = var.image_name
    volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
    zone        = var.zone
    volume_size = var.volume_size
  }
  assert {
    condition = (
      length(output.volumes) == 4 &&
      length(distinct([for volume in values(output.volumes) : volume.id])) == 4 &&
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


run "createCustomImage_optionalInputsOmitted_completeStateReturned" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name   = var.name_prefix
    volume = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.name == var.name_prefix &&
      viettelcloud_custom_image.image.volume.name == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name &&
      viettelcloud_custom_image.image.volume.id == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].id &&
      viettelcloud_custom_image.image.disk_format == "qcow2" &&
      viettelcloud_custom_image.image.container_format == "bare" &&
      viettelcloud_custom_image.image.status == "active" &&
      contains(["enabled", "disabled"], viettelcloud_custom_image.image.state) &&
      can(regex("^[0-9a-f-]{36}$", viettelcloud_custom_image.image.id)) &&
      can(regex("^[0-9a-f-]{36}$", viettelcloud_custom_image.image.volume.id)) &&
      length(viettelcloud_custom_image.image.region) > 0 &&
      length(viettelcloud_custom_image.image.project_id) > 0 &&
      length(viettelcloud_custom_image.image.project) > 0 &&
      (viettelcloud_custom_image.image.owner_id == null) == (viettelcloud_custom_image.image.owner == null) &&
      contains(["0", "128", "256"], viettelcloud_custom_image.image.encrypt_key_size) &&
      can(formatdate("YYYY", viettelcloud_custom_image.image.created_at)) &&
      can(formatdate("YYYY", viettelcloud_custom_image.image.updated_at)) &&
      (viettelcloud_custom_image.image.size == null ? true : viettelcloud_custom_image.image.size >= 0) &&
      (viettelcloud_custom_image.image.min_volume_size == null ? true : viettelcloud_custom_image.image.min_volume_size >= 0) &&
      (viettelcloud_custom_image.image.metadata == null ? true : can(jsondecode(viettelcloud_custom_image.image.metadata))) &&
      (viettelcloud_custom_image.image.shared_projects == null ? true : alltrue([for project in viettelcloud_custom_image.image.shared_projects : can(regex("^[0-9a-f-]{36}$", project.id))]))
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeMutableFields_existingImage_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name               = "${var.name_prefix}-updated"
    volume             = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    description        = "custom image description"
    os                 = "Linux"
    image_version      = "test-1"
    state              = "enabled"
    metadata           = jsonencode({ terraform_test = "custom_image" })
    shared_project_ids = var.custom_image_shared_project_id == null ? [] : [var.custom_image_shared_project_id]
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id == run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id &&
      viettelcloud_custom_image.image.name == "${var.name_prefix}-updated" &&
      viettelcloud_custom_image.image.description == "custom image description" &&
      viettelcloud_custom_image.image.os == "Linux" &&
      viettelcloud_custom_image.image.version == "test-1" &&
      viettelcloud_custom_image.image.state == "enabled" &&
      jsondecode(viettelcloud_custom_image.image.metadata) == { terraform_test = "custom_image" } &&
      toset(sort([for project in viettelcloud_custom_image.image.shared_projects : project.id])) == toset(var.custom_image_shared_project_id == null ? [] : [var.custom_image_shared_project_id])
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "omitMutableFields_valuesSet_valuesPreservedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name   = "${var.name_prefix}-updated"
    volume = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id == run.changeMutableFields_existingImage_updatedInPlace.image.id &&
      viettelcloud_custom_image.image.description == run.changeMutableFields_existingImage_updatedInPlace.image.description &&
      viettelcloud_custom_image.image.os == run.changeMutableFields_existingImage_updatedInPlace.image.os &&
      viettelcloud_custom_image.image.version == run.changeMutableFields_existingImage_updatedInPlace.image.version &&
      viettelcloud_custom_image.image.state == run.changeMutableFields_existingImage_updatedInPlace.image.state &&
      viettelcloud_custom_image.image.metadata == run.changeMutableFields_existingImage_updatedInPlace.image.metadata &&
      viettelcloud_custom_image.image.shared_projects == run.changeMutableFields_existingImage_updatedInPlace.image.shared_projects
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

# Omitted mutable fields keep their prior values in the next plan. Any planned
# change makes updated_at unknown. Omitted shared_projects is covered only when
# custom_image_shared_project_id is set, because an empty set has no elements.
run "planAgain_mutableFieldsOmitted_noDrift" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name   = "${var.name_prefix}-updated"
    volume = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id == run.omitMutableFields_valuesSet_valuesPreservedInPlace.image.id &&
      viettelcloud_custom_image.image.updated_at == run.omitMutableFields_valuesSet_valuesPreservedInPlace.image.updated_at &&
      viettelcloud_custom_image.image.shared_projects == run.omitMutableFields_valuesSet_valuesPreservedInPlace.image.shared_projects
    )
    error_message = "Re-planning with mutable fields omitted proposed a change."
  }
}

run "clearMutableFields_valuesSet_valuesEmptyInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name               = "${var.name_prefix}-updated"
    volume             = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    description        = ""
    os                 = ""
    image_version      = ""
    metadata           = "{}"
    shared_project_ids = []
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id == run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id &&
      viettelcloud_custom_image.image.description == "" && viettelcloud_custom_image.image.os == "" &&
      viettelcloud_custom_image.image.version == "" && jsondecode(viettelcloud_custom_image.image.metadata) == {} &&
      length(viettelcloud_custom_image.image.shared_projects) == 0
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeState_existingImage_disabledInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name               = "${var.name_prefix}-updated"
    volume             = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    description        = ""
    os                 = ""
    image_version      = ""
    metadata           = "{}"
    shared_project_ids = []
    state              = "disabled"
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.state == "disabled" && viettelcloud_custom_image.image.id == run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeState_disabledImage_enabledInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name               = "${var.name_prefix}-updated"
    volume             = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    description        = ""
    os                 = ""
    image_version      = ""
    metadata           = "{}"
    shared_project_ids = []
    state              = "enabled"
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.state == "enabled" && viettelcloud_custom_image.image.id == run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeName_whitespaceConfigured_representationPreserved" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name               = " ${var.name_prefix}-updated "
    volume             = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    description        = ""
    os                 = ""
    image_version      = ""
    metadata           = "{}"
    shared_project_ids = []
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.name == " ${var.name_prefix}-updated " && viettelcloud_custom_image.image.id == run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "planAgain_whitespaceConfigured_noDrift" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name               = " ${var.name_prefix}-updated "
    volume             = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    description        = ""
    os                 = ""
    image_version      = ""
    metadata           = "{}"
    shared_project_ids = []
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id == run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id && viettelcloud_custom_image.image.updated_at == run.changeName_whitespaceConfigured_representationPreserved.image.updated_at
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeDiskFormat_existingImage_imageReplaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name        = var.name_prefix
    volume      = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].name
    disk_format = "raw"
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id != run.createCustomImage_optionalInputsOmitted_completeStateReturned.image.id && viettelcloud_custom_image.image.disk_format == "raw" &&
      viettelcloud_custom_image.image.volume.id == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].id
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeVolume_existingImage_imageReplaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name        = var.name_prefix
    volume      = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["replacement"].name
    disk_format = "raw"
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id != run.changeDiskFormat_existingImage_imageReplaced.image.id && viettelcloud_custom_image.image.volume.name == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["replacement"].name &&
      viettelcloud_custom_image.image.volume.id == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["replacement"].id &&
      run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["replacement"].id != run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["primary"].id
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "createCustomImage_volumeIdConfigured_imageCreated" {
  command   = apply
  state_key = "import_source"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name      = "${var.name_prefix}-import"
    volume_id = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["import"].id
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.status == "active" &&
      viettelcloud_custom_image.image.volume.id == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["import"].id &&
      viettelcloud_custom_image.image.volume.name == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["import"].name
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}

run "changeDescription_volumeIdConfigured_updatedInPlace" {
  command   = apply
  state_key = "import_source"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name        = "${var.name_prefix}-import"
    volume_id   = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["import"].id
    description = "Created from a volume UUID"
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id == run.createCustomImage_volumeIdConfigured_imageCreated.image.id &&
      viettelcloud_custom_image.image.description == "Created from a volume UUID" &&
      viettelcloud_custom_image.image.volume == run.createCustomImage_volumeIdConfigured_imageCreated.image.volume
    )
    error_message = "Changing the description must preserve the image and its source volume."
  }
}

run "planAgain_volumeIdConfigured_noDrift" {
  command   = plan
  state_key = "import_source"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name        = "${var.name_prefix}-import"
    volume_id   = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["import"].id
    description = "Created from a volume UUID"
  }
  assert {
    condition     = viettelcloud_custom_image.image == run.changeDescription_volumeIdConfigured_updatedInPlace.image
    error_message = "The unchanged volume UUID must produce the same image state."
  }
}

run "changeVolumeId_existingImage_imageReplaced" {
  command   = apply
  state_key = "import_source"
  parallel  = true
  module { source = "./fixtures/custom_image/resource" }
  variables {
    name      = "${var.name_prefix}-import"
    volume_id = run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["importReplacement"].id
  }
  assert {
    condition = (
      viettelcloud_custom_image.image.id != run.changeDescription_volumeIdConfigured_updatedInPlace.image.id &&
      viettelcloud_custom_image.image.volume.id == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["importReplacement"].id &&
      viettelcloud_custom_image.image.volume.name == run.createVolumes_customImageSources_bootableVolumesAvailable.volumes["importReplacement"].name
    )
    error_message = "Changing the volume UUID must replace the image and return the new source name."
  }
}

run "planImport_volumeBackedImage_fullStateRestored" {
  command   = plan
  state_key = "import_target"
  parallel  = true
  module { source = "./fixtures/custom_image/import" }
  variables {
    image_id         = run.changeVolumeId_existingImage_imageReplaced.image.id
    name             = run.changeVolumeId_existingImage_imageReplaced.image.name
    volume_id        = run.changeVolumeId_existingImage_imageReplaced.image.volume.id
    disk_format      = run.changeVolumeId_existingImage_imageReplaced.image.disk_format
    container_format = run.changeVolumeId_existingImage_imageReplaced.image.container_format
  }
  assert {
    condition = (
      viettelcloud_custom_image.image == run.changeVolumeId_existingImage_imageReplaced.image
    )
    error_message = "Custom image lifecycle result differs from the expected state."
  }
}
