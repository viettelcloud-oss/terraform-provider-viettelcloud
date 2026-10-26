# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (shared fixture state):
#   run "createFleet_threeEmptyVolumeConfigs_allVolumesAvailable" {
#   run "omitZone_configuredZone_zonePreservedInPlace" {
#   run "planAgain_zoneOmitted_stateStable" {
#   run "addTwoVolumes_threeVolumeFleet_existingIdsPreserved" {
#   run "removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved" {
#   run "changeName_existingPrimaryVolume_nameUpdatedInPlace" {
#   run "changeDescription_existingPrimaryVolume_descriptionUpdatedInPlace" {
#   run "increaseSize_existingPrimaryVolume_volumeResizedInPlace" {
#   run "setIops_resizedPrimaryVolume_iopsUpdatedInPlace" {
#   run "omitDescriptionAndIops_configuredDescriptionAndIops_descriptionAndIopsPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "setUntrimmedName_existingPrimaryVolume_configuredNamePreserved" {
#   run "planAgain_fleetWithUntrimmedPrimaryName_stateStable" {
#   run "configureImageSource_emptyPrimaryVolume_primaryReplaced" {
#   run "planAgain_fleetWithImageBackedPrimary_stateStable" {
#   run "addSnapshotVolume_fleetWithImageBackedPrimary_existingIdsPreserved" {
#   run "switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated" {
#   run "increaseSize_snapshotBackedVolume_volumeResizedInPlace" {
#   run "planAgain_externalSourceFleet_stateStable" {
#   run "changeVolumeType_backupBackedVolume_volumeRetypedInPlace" {
#   run "planAgain_retypedVolumeFleet_stateStable" {

# Chain selectors (state_key "selectors"): independent image volume, then cleanup.
#   run "createVolume_imageAndVolumeTypeCriteria_selectorsPreserved" {
#   run "planAgain_catalogCriteria_stateStable" {
#   run "changeSelectorsToNames_sameCatalogEntries_updatedWithoutRetypeOrReplace" {
#   run "changeDescription_catalogCriteria_updatedInPlace" {
#   run "changeVolumeTypeSelector_catalogCriteria_retypedInPlace" {
#   run "changeImageSelector_catalogCriteria_volumeReplaced" {
# Chain selector import (state_key "selectors-import-source" -> "selectors-import"; plan-only import).
#   run "createVolume_catalogCriteriaForImport_volumeCreated" {
#   run "changeSelectorsToNames_selectorImportOwner_identityPreserved" {
#   run "importVolume_selectorCreatedVolume_backendFieldsReturned" {
#   run "changeSelectorsToCriteria_namedSelectorImportOwner_updatedWithoutRetypeOrReplace" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Identifiers of sources that already exist in the test project. They are
# declared here rather than in provider.tf because only this file needs them.
# `terraform test` auto-loads integration-tests/terraform.tfvars, so the values
# are supplied there (or with -var) exactly like the connection variables.
# custom_image_id defaults to null because the assertions that consume it are
# disabled; set it again when they are re-enabled, or the volume is created with
# an unparseable source ID instead of failing early. snapshot_id and backup_id
# carry no default so a missing value fails before any volume is created.
variable "custom_image_id" {
  description = "ID (UUID) of an existing custom image a volume can be created from."
  type        = string
  default     = null
}

variable "snapshot_id" {
  description = "ID (UUID) of an existing volume snapshot a volume can be created from."
  type        = string
}

variable "backup_id" {
  description = "ID (UUID) of an existing volume backup a volume can be created from."
  type        = string
}

variable "source_volume_size" {
  description = "Size in GiB requested for the backup-backed volume. It must be a multiple of 10 and at least as large as the backup."
  type        = number
  default     = 30
}

# hdd_st1, selected by var.volume_type, fixes IOPS at 500.
# The backend rejects other values with "IOPS must be between 500 and 500".
# Override volume_iops together with var.volume_type.
variable "volume_iops" {
  description = "IOPS the configured volume type accepts."
  type        = number
  default     = 500
}

# The retype runs move one volume onto a second type. It has to be a ready type
# the backend accepts for an existing volume of var.source_volume_size GiB, and
# it must differ from the type that var.volume_type selects, or the retype
# would be a no-op.
variable "alternate_volume_type" {
  description = "Criteria of a second ready volume type for retyping, without encryption or multiple attachments."
  type        = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
  default     = { name = "hdd_io1", encryption_key_size = "0", multiattach = false }
}

variables {
  image_name  = "cirros"
  zone        = "vn-central-1a"
  volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

# Every run performs exactly one change so a failure names the operation that
# caused it. The fleet is created, scaled, and reduced first; the single-attribute
# updates then run against three volumes instead of five.

run "createFleet_threeEmptyVolumeConfigs_allVolumesAvailable" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary"
        size = 30
      }
      logs = {
        name        = "tf-test-volume-logs"
        description = "Log volume"
        size        = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 3 &&
      length(distinct([for volume in values(viettelcloud_volume.volume) : volume.id])) == 3 &&
      alltrue([
        for volume in values(viettelcloud_volume.volume) : (
          length(volume.id) > 0 &&
          volume.size == 30 &&
          volume.status == "available" &&
          !volume.bootable &&
          volume.zone.name == var.zone &&
          length(volume.zone_id) > 0 &&
          length(volume.volume_type) > 0 &&
          volume.create_from.volume_type.id != null &&
          volume.create_from.volume_type == merge(var.volume_type, { id = volume.create_from.volume_type.id }) &&
          length(volume.created_at) > 0 &&
          length(volume.updated_at) > 0
        )
      ]) &&
      # primary configures no description; the backend may report it as absent.
      (viettelcloud_volume.volume["primary"].description == null || viettelcloud_volume.volume["primary"].description == "") &&
      viettelcloud_volume.volume["logs"].description == "Log volume" &&
      # cache configures IOPS at creation; the create request must carry it.
      viettelcloud_volume.volume["cache"].iops == var.volume_iops
    )
    error_message = "The initial volume fleet was not created concurrently with unique IDs and complete platform metadata."
  }
}

run "omitZone_configuredZone_zonePreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    zone = null
    volumes = {
      primary = {
        name = "tf-test-volume-primary"
        size = 30
      }
      logs = {
        name        = "tf-test-volume-logs"
        description = "Log volume"
        size        = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
    }
  }

  assert {
    condition = alltrue([
      for key, volume in viettelcloud_volume.volume : (
        volume.id == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].id &&
        volume.zone == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].zone
      )
    ])
    error_message = "Removing zone from configuration replaced a volume or discarded its current zone."
  }
}

run "planAgain_zoneOmitted_stateStable" {
  command = plan

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    zone = null
    volumes = {
      primary = {
        name = "tf-test-volume-primary"
        size = 30
      }
      logs = {
        name        = "tf-test-volume-logs"
        description = "Log volume"
        size        = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
    }
  }

  assert {
    condition     = output.volumes == run.omitZone_configuredZone_zonePreservedInPlace.volumes
    error_message = "Re-planning with zone omitted proposed a change to the volume fleet."
  }
}

run "addTwoVolumes_threeVolumeFleet_existingIdsPreserved" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary"
        size = 30
      }
      logs = {
        name        = "tf-test-volume-logs"
        description = "Log volume"
        size        = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
      archive = {
        name = "tf-test-volume-archive"
        size = 30
      }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 5 &&
      length(distinct([for volume in values(viettelcloud_volume.volume) : volume.id])) == 5 &&
      alltrue([
        for key in ["metrics", "archive"] : (
          !contains(values(run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volume_ids), viettelcloud_volume.volume[key].id) &&
          viettelcloud_volume.volume[key].size == 30 &&
          viettelcloud_volume.volume[key].status == "available"
        )
      ]) &&
      viettelcloud_volume.volume["metrics"].description == "Metrics volume" &&
      # Adding members must leave the existing volumes untouched.
      alltrue([
        for key in ["primary", "logs", "cache"] : (
          viettelcloud_volume.volume[key].id == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].id &&
          viettelcloud_volume.volume[key].name == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].name &&
          viettelcloud_volume.volume[key].description == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].description &&
          viettelcloud_volume.volume[key].size == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].size &&
          viettelcloud_volume.volume[key].iops == run.createFleet_threeEmptyVolumeConfigs_allVolumesAvailable.volumes[key].iops
        )
      ])
    )
    error_message = "Scaling out did not add two unique volumes, or it disturbed the volumes created earlier."
  }
}

run "removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary"
        size = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 3 &&
      alltrue([
        for key in ["primary", "cache", "metrics"] : (
          viettelcloud_volume.volume[key].id == run.addTwoVolumes_threeVolumeFleet_existingIdsPreserved.volumes[key].id &&
          viettelcloud_volume.volume[key].name == run.addTwoVolumes_threeVolumeFleet_existingIdsPreserved.volumes[key].name &&
          viettelcloud_volume.volume[key].description == run.addTwoVolumes_threeVolumeFleet_existingIdsPreserved.volumes[key].description &&
          viettelcloud_volume.volume[key].size == run.addTwoVolumes_threeVolumeFleet_existingIdsPreserved.volumes[key].size &&
          viettelcloud_volume.volume[key].iops == run.addTwoVolumes_threeVolumeFleet_existingIdsPreserved.volumes[key].iops
        )
      ])
    )
    error_message = "Scaling in did not remove only the dropped volumes; a retained volume changed identity or values."
  }
}

run "changeName_existingPrimaryVolume_nameUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary-renamed"
        size = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].name == "tf-test-volume-primary-renamed" &&
      viettelcloud_volume.volume["primary"].size == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].size &&
      viettelcloud_volume.volume["primary"].iops == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].iops &&
      alltrue([
        for key in ["cache", "metrics"] :
        viettelcloud_volume.volume[key].name == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes[key].name
      ])
    )
    error_message = "Renaming a volume replaced it, changed another attribute, or affected another fleet member."
  }
}

run "changeDescription_existingPrimaryVolume_descriptionUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name        = "tf-test-volume-primary-renamed"
        description = "Primary volume after update"
        size        = 30
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].description == "Primary volume after update" &&
      viettelcloud_volume.volume["primary"].name == run.changeName_existingPrimaryVolume_nameUpdatedInPlace.volumes["primary"].name &&
      viettelcloud_volume.volume["primary"].size == run.changeName_existingPrimaryVolume_nameUpdatedInPlace.volumes["primary"].size &&
      viettelcloud_volume.volume["primary"].iops == run.changeName_existingPrimaryVolume_nameUpdatedInPlace.volumes["primary"].iops
    )
    error_message = "Setting a description on a volume that had none replaced the volume or moved another attribute."
  }
}

run "increaseSize_existingPrimaryVolume_volumeResizedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name        = "tf-test-volume-primary-renamed"
        description = "Primary volume after update"
        size        = 40
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].size == 40 &&
      viettelcloud_volume.volume["primary"].status == "available" &&
      viettelcloud_volume.volume["primary"].name == run.changeDescription_existingPrimaryVolume_descriptionUpdatedInPlace.volumes["primary"].name &&
      viettelcloud_volume.volume["primary"].description == run.changeDescription_existingPrimaryVolume_descriptionUpdatedInPlace.volumes["primary"].description &&
      viettelcloud_volume.volume["cache"].size == 30 &&
      viettelcloud_volume.volume["metrics"].size == 30
    )
    error_message = "Growing a volume was rejected, replaced the volume, or resized another fleet member."
  }
}

# IOPS is fixed by the volume type, so this proves that configuring the value
# the type allows is accepted and does not replace the volume. A type with a
# range would also exercise the update path; the type that var.volume_type
# selects cannot.
run "setIops_resizedPrimaryVolume_iopsUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name        = "tf-test-volume-primary-renamed"
        description = "Primary volume after update"
        size        = 40
        iops        = var.volume_iops
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].iops == var.volume_iops &&
      viettelcloud_volume.volume["primary"].name == run.increaseSize_existingPrimaryVolume_volumeResizedInPlace.volumes["primary"].name &&
      viettelcloud_volume.volume["primary"].description == run.increaseSize_existingPrimaryVolume_volumeResizedInPlace.volumes["primary"].description &&
      viettelcloud_volume.volume["primary"].size == run.increaseSize_existingPrimaryVolume_volumeResizedInPlace.volumes["primary"].size
    )
    error_message = "Configuring the IOPS the volume type allows was rejected or changed another attribute."
  }
}

run "omitDescriptionAndIops_configuredDescriptionAndIops_descriptionAndIopsPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary-renamed"
        size = 40
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].description == run.setIops_resizedPrimaryVolume_iopsUpdatedInPlace.volumes["primary"].description &&
      viettelcloud_volume.volume["primary"].iops == run.setIops_resizedPrimaryVolume_iopsUpdatedInPlace.volumes["primary"].iops &&
      viettelcloud_volume.volume["primary"].size == run.setIops_resizedPrimaryVolume_iopsUpdatedInPlace.volumes["primary"].size
    )
    error_message = "Removing description and iops from configuration discarded the backend values instead of retaining them."
  }
}

run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name        = "tf-test-volume-primary-renamed"
        description = ""
        size        = 40
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].description == "" &&
      viettelcloud_volume.volume["primary"].name == run.omitDescriptionAndIops_configuredDescriptionAndIops_descriptionAndIopsPreservedInPlace.volumes["primary"].name &&
      viettelcloud_volume.volume["primary"].size == run.omitDescriptionAndIops_configuredDescriptionAndIops_descriptionAndIopsPreservedInPlace.volumes["primary"].size &&
      viettelcloud_volume.volume["primary"].iops == run.omitDescriptionAndIops_configuredDescriptionAndIops_descriptionAndIopsPreservedInPlace.volumes["primary"].iops &&
      viettelcloud_volume.volume["metrics"].description == "Metrics volume"
    )
    error_message = "An explicitly empty description was not stored as an empty string, or it cleared another volume."
  }
}

# The provider trims the name before sending it and keeps the configured
# spelling in state, so padding must not produce a permanent diff.
run "setUntrimmedName_existingPrimaryVolume_configuredNamePreserved" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name        = "  tf-test-volume-primary-renamed  "
        description = ""
        size        = 40
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["primary"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["primary"].name == "  tf-test-volume-primary-renamed  " &&
      viettelcloud_volume.volume["primary"].description == run.clearDescription_descriptionSet_descriptionEmptyInPlace.volumes["primary"].description &&
      viettelcloud_volume.volume["primary"].size == run.clearDescription_descriptionSet_descriptionEmptyInPlace.volumes["primary"].size
    )
    error_message = "A padded name changed the volume identity or was not preserved as configured."
  }
}

run "planAgain_fleetWithUntrimmedPrimaryName_stateStable" {
  command = plan

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name        = "  tf-test-volume-primary-renamed  "
        description = ""
        size        = 40
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  # Re-planning the applied configuration must produce no change. Every value
  # below is compared against the preceding apply. A replacement would make the
  # planned id unknown, an in-place update would move a compared attribute, and
  # a missing UseStateForUnknown would leave a computed value unknown.
  assert {
    condition = alltrue([
      for key, volume in viettelcloud_volume.volume : (
        volume.id == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].id &&
        volume.name == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].name &&
        volume.description == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].description &&
        volume.size == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].size &&
        volume.iops == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].iops &&
        volume.status == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].status &&
        volume.bootable == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].bootable &&
        volume.encrypted == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].encrypted &&
        volume.zone == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].zone &&
        volume.zone_id == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].zone_id &&
        volume.volume_type == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].volume_type &&
        volume.created_at == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].created_at &&
        volume.create_from.source_type == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].source_type &&
        volume.create_from.volume_type == run.setUntrimmedName_existingPrimaryVolume_configuredNamePreserved.volumes[key].source_volume_type
      )
    ])
    error_message = "Re-planning the applied empty-source fleet proposed a change; the volume resource drifts against its own state."
  }
}

run "configureImageSource_emptyPrimaryVolume_primaryReplaced" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary-from-image"
        size = 40
        create_from = {
          source_type = "image"
          image       = { name = var.image_name }
        }
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 3 &&
      viettelcloud_volume.volume["primary"].id != run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["primary"].id &&
      viettelcloud_volume.volume["cache"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["cache"].id &&
      viettelcloud_volume.volume["metrics"].id == run.removeTwoVolumes_fiveVolumeFleet_remainingIdsPreserved.volumes["metrics"].id &&
      viettelcloud_volume.volume["primary"].name == "tf-test-volume-primary-from-image" &&
      viettelcloud_volume.volume["primary"].size == 40 &&
      viettelcloud_volume.volume["primary"].status == "available" &&
      length(viettelcloud_volume.volume["primary"].volume_type) > 0 &&
      viettelcloud_volume.volume["primary"].create_from.source_type == "image" &&
      viettelcloud_volume.volume["primary"].create_from.image.name == var.image_name &&
      viettelcloud_volume.volume["primary"].create_from.volume_type.id != null &&
      viettelcloud_volume.volume["primary"].create_from.volume_type == merge(var.volume_type, { id = viettelcloud_volume.volume["primary"].create_from.volume_type.id })
    )
    error_message = "Replacing the empty source with an image did not recreate only the primary volume with the expected image source."
  }
}

run "planAgain_fleetWithImageBackedPrimary_stateStable" {
  command = plan

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary-from-image"
        size = 40
        create_from = {
          source_type = "image"
          image       = { name = var.image_name }
        }
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
    }
  }

  # The same empty-plan proof for an image-sourced volume. create_from must
  # refresh to the value it was applied with, or the next plan would propose
  # destroying and recreating a volume that already holds data.
  assert {
    condition = alltrue([
      for key, volume in viettelcloud_volume.volume : (
        volume.id == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].id &&
        volume.name == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].name &&
        volume.description == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].description &&
        volume.size == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].size &&
        volume.iops == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].iops &&
        volume.status == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].status &&
        volume.bootable == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].bootable &&
        volume.encrypted == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].encrypted &&
        volume.zone == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].zone &&
        volume.zone_id == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].zone_id &&
        volume.volume_type == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].volume_type &&
        volume.created_at == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].created_at &&
        volume.create_from.source_type == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].source_type &&
        volume.create_from.image == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].source_image &&
        volume.create_from.volume_type == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].source_volume_type
      )
    ])
    error_message = "Re-planning the image-sourced fleet proposed a change; create_from does not refresh to the value it was applied with."
  }
}

# A snapshot is the only source that owns both the size and the volume type, so
# it is the one source whose volume cannot be described by the configuration
# alone. It is added to the fleet rather than replacing it: a fourth volume
# stays below the five the scale-out run already reached.
run "addSnapshotVolume_fleetWithImageBackedPrimary_existingIdsPreserved" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      primary = {
        name = "tf-test-volume-primary-from-image"
        size = 40
        create_from = {
          source_type = "image"
          image       = { name = var.image_name }
        }
      }
      cache = {
        name = "tf-test-volume-cache"
        size = 30
        iops = var.volume_iops
      }
      metrics = {
        name        = "tf-test-volume-metrics"
        description = "Metrics volume"
        size        = 30
      }
      # size is deliberately omitted: the snapshot determines it, and the
      # provider rejects an explicit size for this source at creation.
      snapshot = {
        name = "tf-test-volume-from-snapshot"
        create_from = {
          source_type = "snapshot"
          snapshot    = { id = var.snapshot_id }
        }
      }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 4 &&
      alltrue([
        for key in ["primary", "cache", "metrics"] : (
          viettelcloud_volume.volume[key].id == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].id &&
          viettelcloud_volume.volume[key].name == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].name &&
          viettelcloud_volume.volume[key].description == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].description &&
          viettelcloud_volume.volume[key].size == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].size &&
          viettelcloud_volume.volume[key].iops == run.configureImageSource_emptyPrimaryVolume_primaryReplaced.volumes[key].iops
        )
      ])
    )
    error_message = "Adding the snapshot-backed volume did not leave the volumes that were already applied untouched."
  }

  # The size must come back from the backend, and create_from.volume_type must
  # stay absent because the provider refuses to accept one for this source.
  assert {
    condition = (
      viettelcloud_volume.volume["snapshot"].name == "tf-test-volume-from-snapshot" &&
      viettelcloud_volume.volume["snapshot"].status == "available" &&
      viettelcloud_volume.volume["snapshot"].size > 0 &&
      length(viettelcloud_volume.volume["snapshot"].volume_type) > 0 &&
      length(viettelcloud_volume.volume["snapshot"].zone.name) > 0 &&
      length(viettelcloud_volume.volume["snapshot"].zone_id) > 0 &&
      length(viettelcloud_volume.volume["snapshot"].created_at) > 0 &&
      length(viettelcloud_volume.volume["snapshot"].updated_at) > 0 &&
      viettelcloud_volume.volume["snapshot"].create_from.source_type == "snapshot" &&
      viettelcloud_volume.volume["snapshot"].create_from.snapshot.id == var.snapshot_id &&
      viettelcloud_volume.volume["snapshot"].create_from.volume_type == null &&
      viettelcloud_volume.volume["snapshot"].create_from.image == null &&
      viettelcloud_volume.volume["snapshot"].create_from.custom_image == null &&
      viettelcloud_volume.volume["snapshot"].create_from.backup == null
    )
    error_message = "The snapshot-backed volume did not derive its size from the snapshot or reported the wrong source."
  }
}

# The runs below swap the whole fleet for volumes built from sources that
# already exist in the project. Replacing rather than extending it keeps the
# concurrent volume count at the level the earlier runs already reached. The
# custom_image member of each fleet stays commented out while custom images are
# unstable in the test project; snapshots and backups are ready.
run "switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      # size is deliberately omitted: the snapshot determines it, and the
      # provider rejects an explicit size for this source at creation. The
      # configuration is unchanged from the previous run, so this member must
      # survive while the rest of the fleet is destroyed.
      snapshot = {
        name = "tf-test-volume-from-snapshot"
        create_from = {
          source_type = "snapshot"
          snapshot    = { id = var.snapshot_id }
        }
      }
      backup = {
        name = "tf-test-volume-from-backup"
        size = var.source_volume_size
        create_from = {
          source_type = "backup"
          backup      = { id = var.backup_id }
        }
      }
      # Restore this member together with the custom_image assertion below and
      # the custom_image_id value in terraform.tfvars.
      # custom_image = {
      #   name = "tf-test-volume-from-custom-image"
      #   size = var.source_volume_size
      #   create_from = {
      #     source_type  = "custom_image"
      #     custom_image = { id = var.custom_image_id }
      #   }
      # }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 2 &&
      viettelcloud_volume.volume["snapshot"].id == run.addSnapshotVolume_fleetWithImageBackedPrimary_existingIdsPreserved.volumes["snapshot"].id &&
      !contains([for prior in values(run.addSnapshotVolume_fleetWithImageBackedPrimary_existingIdsPreserved.volumes) : prior.id], viettelcloud_volume.volume["backup"].id) &&
      alltrue([
        for volume in values(viettelcloud_volume.volume) : (
          volume.status == "available" &&
          volume.size > 0 &&
          length(volume.zone.name) > 0 &&
          length(volume.zone_id) > 0 &&
          length(volume.volume_type) > 0 &&
          length(volume.created_at) > 0 &&
          length(volume.updated_at) > 0
        )
      ])
    )
    error_message = "Dropping the empty- and image-sourced members did not keep the snapshot-backed volume, add a new backup-backed one, and describe both completely."
  }

  # A snapshot source owns both the size and the volume type. size must come
  # back from the backend, and create_from.volume_type must stay absent because
  # the provider refuses to accept one for this source.
  assert {
    condition = (
      viettelcloud_volume.volume["snapshot"].size > 0 &&
      length(viettelcloud_volume.volume["snapshot"].volume_type) > 0 &&
      viettelcloud_volume.volume["snapshot"].create_from.source_type == "snapshot" &&
      viettelcloud_volume.volume["snapshot"].create_from.snapshot.id == var.snapshot_id &&
      viettelcloud_volume.volume["snapshot"].create_from.volume_type == null &&
      viettelcloud_volume.volume["snapshot"].create_from.image == null &&
      viettelcloud_volume.volume["snapshot"].create_from.custom_image == null &&
      viettelcloud_volume.volume["snapshot"].create_from.backup == null
    )
    error_message = "The snapshot-backed volume did not derive its size from the snapshot or reported the wrong source."
  }

  assert {
    condition = (
      viettelcloud_volume.volume["backup"].size == var.source_volume_size &&
      length(viettelcloud_volume.volume["backup"].volume_type) > 0 &&
      viettelcloud_volume.volume["backup"].create_from.source_type == "backup" &&
      viettelcloud_volume.volume["backup"].create_from.backup.id == var.backup_id &&
      viettelcloud_volume.volume["backup"].create_from.volume_type.id != null &&
      viettelcloud_volume.volume["backup"].create_from.volume_type == merge(var.volume_type, { id = viettelcloud_volume.volume["backup"].create_from.volume_type.id }) &&
      viettelcloud_volume.volume["backup"].create_from.image == null &&
      viettelcloud_volume.volume["backup"].create_from.custom_image == null &&
      viettelcloud_volume.volume["backup"].create_from.snapshot == null
    )
    error_message = "The backup-backed volume did not honor the requested size and volume type or reported the wrong source."
  }

  # assert {
  #   condition = (
  #     viettelcloud_volume.volume["custom_image"].size == var.source_volume_size &&
  #     length(viettelcloud_volume.volume["custom_image"].volume_type) > 0 &&
  #     viettelcloud_volume.volume["custom_image"].create_from.source_type == "custom_image" &&
  #     viettelcloud_volume.volume["custom_image"].create_from.custom_image.id == var.custom_image_id &&
  #     viettelcloud_volume.volume["custom_image"].create_from.volume_type.id != null &&
  #     viettelcloud_volume.volume["custom_image"].create_from.volume_type == merge(var.volume_type, { id = viettelcloud_volume.volume["custom_image"].create_from.volume_type.id }) &&
  #     viettelcloud_volume.volume["custom_image"].create_from.image == null &&
  #     viettelcloud_volume.volume["custom_image"].create_from.snapshot == null &&
  #     viettelcloud_volume.volume["custom_image"].create_from.backup == null
  #   )
  #   error_message = "The custom-image-backed volume did not honor the requested size and volume type or reported the wrong source."
  # }
}

run "increaseSize_snapshotBackedVolume_volumeResizedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      # The snapshot fixes only the initial size; growing the volume afterwards
      # must be an in-place update, not a rejected configuration or a replacement.
      snapshot = {
        name = "tf-test-volume-from-snapshot"
        size = run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["snapshot"].size + 10
        create_from = {
          source_type = "snapshot"
          snapshot    = { id = var.snapshot_id }
        }
      }
      backup = {
        name = "tf-test-volume-from-backup"
        size = var.source_volume_size
        create_from = {
          source_type = "backup"
          backup      = { id = var.backup_id }
        }
      }
      # custom_image = {
      #   name = "tf-test-volume-from-custom-image"
      #   size = var.source_volume_size
      #   create_from = {
      #     source_type  = "custom_image"
      #     custom_image = { id = var.custom_image_id }
      #   }
      # }
    }
  }

  assert {
    condition = (
      length(viettelcloud_volume.volume) == 2 &&
      viettelcloud_volume.volume["snapshot"].id == run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["snapshot"].id &&
      viettelcloud_volume.volume["snapshot"].size == run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["snapshot"].size + 10 &&
      viettelcloud_volume.volume["snapshot"].status == "available" &&
      viettelcloud_volume.volume["snapshot"].create_from.snapshot.id == var.snapshot_id &&
      viettelcloud_volume.volume["backup"].id == run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["backup"].id &&
      viettelcloud_volume.volume["backup"].size == run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["backup"].size
    )
    error_message = "Growing the snapshot-backed volume was rejected or replaced a volume; only the size of the snapshot-backed volume may change."
  }
}

run "planAgain_externalSourceFleet_stateStable" {
  command = plan

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      snapshot = {
        name = "tf-test-volume-from-snapshot"
        size = run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["snapshot"].size + 10
        create_from = {
          source_type = "snapshot"
          snapshot    = { id = var.snapshot_id }
        }
      }
      backup = {
        name = "tf-test-volume-from-backup"
        size = var.source_volume_size
        create_from = {
          source_type = "backup"
          backup      = { id = var.backup_id }
        }
      }
      # custom_image = {
      #   name = "tf-test-volume-from-custom-image"
      #   size = var.source_volume_size
      #   create_from = {
      #     source_type  = "custom_image"
      #     custom_image = { id = var.custom_image_id }
      #   }
      # }
    }
  }

  # The same empty-plan proof for volumes built from an external source. Every
  # value is compared against the preceding apply. A replacement would make the
  # planned id unknown. A create_from that does not refresh to the value it was
  # applied with would propose destroying a volume that already holds data.
  assert {
    condition = alltrue([
      for key, volume in viettelcloud_volume.volume : (
        volume.id == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].id &&
        volume.name == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].name &&
        volume.description == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].description &&
        volume.size == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].size &&
        volume.iops == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].iops &&
        volume.status == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].status &&
        volume.bootable == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].bootable &&
        volume.encrypted == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].encrypted &&
        volume.zone == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].zone &&
        volume.zone_id == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].zone_id &&
        volume.volume_type == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].volume_type &&
        volume.created_at == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].created_at &&
        volume.create_from.source_type == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].source_type &&
        volume.create_from.image == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].source_image &&
        volume.create_from.custom_image == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].source_custom_image &&
        volume.create_from.snapshot == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].source_snapshot &&
        volume.create_from.backup == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].source_backup &&
        volume.create_from.volume_type == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes[key].source_volume_type
      )
    ])
    error_message = "Re-planning the source-backed fleet proposed a change; create_from does not refresh to the value it was applied with."
  }
}

# The backend retypes a volume in place, so a changed create_from.volume_type
# must update the volume that already holds data instead of destroying it. Only
# the backup-backed member is moved: a snapshot source takes its type from the
# snapshot and the provider rejects the attribute for it.
#
# The backup volume has a configured zone, so retype must keep it there.
# Encryption can follow the new volume type and is read after the action.
run "changeVolumeType_backupBackedVolume_volumeRetypedInPlace" {
  command = apply

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      snapshot = {
        name = "tf-test-volume-from-snapshot"
        size = run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["snapshot"].size + 10
        create_from = {
          source_type = "snapshot"
          snapshot    = { id = var.snapshot_id }
        }
      }
      backup = {
        name = "tf-test-volume-from-backup"
        size = var.source_volume_size
        create_from = {
          source_type = "backup"
          backup      = { id = var.backup_id }
          volume_type = var.alternate_volume_type
        }
      }
    }
  }

  assert {
    condition = (
      viettelcloud_volume.volume["backup"].id == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["backup"].id &&
      viettelcloud_volume.volume["backup"].volume_type != run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["backup"].volume_type &&
      viettelcloud_volume.volume["backup"].create_from.volume_type.id != run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["backup"].source_volume_type.id &&
      viettelcloud_volume.volume["backup"].create_from.volume_type == merge(var.alternate_volume_type, { id = viettelcloud_volume.volume["backup"].create_from.volume_type.id }) &&
      viettelcloud_volume.volume["backup"].size == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["backup"].size &&
      viettelcloud_volume.volume["backup"].zone == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["backup"].zone &&
      viettelcloud_volume.volume["backup"].zone_id == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["backup"].zone_id &&
      viettelcloud_volume.volume["backup"].status == "available"
    )
    error_message = "Changing create_from.volume_type replaced, resized, or moved the volume, or left it reporting the previous type."
  }

  # A retype travels through its own backend action, so it must not reach any
  # other fleet member.
  assert {
    condition = (
      viettelcloud_volume.volume["snapshot"].id == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["snapshot"].id &&
      viettelcloud_volume.volume["snapshot"].volume_type == run.increaseSize_snapshotBackedVolume_volumeResizedInPlace.volumes["snapshot"].volume_type &&
      viettelcloud_volume.volume["snapshot"].create_from.volume_type == null
    )
    error_message = "Retyping one volume changed another fleet member."
  }
}

run "planAgain_retypedVolumeFleet_stateStable" {
  command = plan

  module {
    source = "./fixtures/volume/resource"
  }

  variables {
    volumes = {
      snapshot = {
        name = "tf-test-volume-from-snapshot"
        size = run.switchToExternalSources_mixedVolumeFleet_snapshotPreservedAndBackupCreated.volumes["snapshot"].size + 10
        create_from = {
          source_type = "snapshot"
          snapshot    = { id = var.snapshot_id }
        }
      }
      backup = {
        name = "tf-test-volume-from-backup"
        size = var.source_volume_size
        create_from = {
          source_type = "backup"
          backup      = { id = var.backup_id }
          volume_type = var.alternate_volume_type
        }
      }
    }
  }

  # Every value the retype settled on, including zone and encrypted, must now be
  # stable: the plan may not propose moving the volume again.
  assert {
    condition = alltrue([
      for key, volume in viettelcloud_volume.volume : (
        volume.id == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].id &&
        volume.name == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].name &&
        volume.description == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].description &&
        volume.size == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].size &&
        volume.iops == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].iops &&
        volume.status == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].status &&
        volume.bootable == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].bootable &&
        volume.encrypted == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].encrypted &&
        volume.zone == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].zone &&
        volume.zone_id == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].zone_id &&
        volume.volume_type == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].volume_type &&
        volume.created_at == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].created_at &&
        volume.create_from.source_type == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].source_type &&
        volume.create_from.snapshot == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].source_snapshot &&
        volume.create_from.backup == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].source_backup &&
        volume.create_from.volume_type == run.changeVolumeType_backupBackedVolume_volumeRetypedInPlace.volumes[key].source_volume_type
      )
    ])
    error_message = "Re-planning the retyped fleet proposed a change; a retype does not settle into a stable state."
  }
}

# These criteria must identify one catalog entry in the test project.
# Override them when the catalog differs. Each image must fit a 30 GiB volume.
variable "selector_image_name" {
  description = "Name of the image selected by selector_image."
  type        = string
  default     = "cirros"
}

variable "selector_alternate_image_name" {
  description = "Name of a different image for selector replacement."
  type        = string
  default     = "ubuntu-22.04-publiccloud"
}

variable "selector_image" {
  type    = object({ name = optional(string), os = optional(string), version = optional(string), type = optional(string) })
  default = { os = "cirros", version = "20.04" }
}

variable "selector_volume_type" {
  type    = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
  default = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

run "createVolume_imageAndVolumeTypeCriteria_selectorsPreserved" {
  command   = apply
  state_key = "selectors"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selectors"
      size        = 30
      description = null
      create_from = { source_type = "image", image = var.selector_image, volume_type = var.selector_volume_type }
    } }
  }
  assert {
    condition     = viettelcloud_volume.volume["selected"].create_from.image == merge(var.selector_image, { id = viettelcloud_volume.volume["selected"].create_from.image.id }) && viettelcloud_volume.volume["selected"].create_from.volume_type == merge(var.selector_volume_type, { id = viettelcloud_volume.volume["selected"].create_from.volume_type.id })
    error_message = "The volume selector lifecycle did not preserve the expected selection or identity."
  }
  assert {
    condition     = viettelcloud_volume.volume["selected"].create_from.image.id != null && viettelcloud_volume.volume["selected"].create_from.volume_type.id != null
    error_message = "Created selectors must contain catalog IDs."
  }

}

run "planAgain_catalogCriteria_stateStable" {
  command   = plan
  state_key = "selectors"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selectors"
      size        = 30
      description = null
      create_from = { source_type = "image", image = var.selector_image, volume_type = var.selector_volume_type }
    } }
  }
  assert {
    condition = (
      viettelcloud_volume.volume["selected"].id == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].id &&
      viettelcloud_volume.volume["selected"].created_at == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].created_at &&
      viettelcloud_volume.volume["selected"].create_from.image == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].source_image &&
      viettelcloud_volume.volume["selected"].create_from.volume_type == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].source_volume_type &&
      viettelcloud_volume.volume["selected"].volume_type == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].volume_type &&
      viettelcloud_volume.volume["selected"].status == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].status
    )
    error_message = "The volume selector lifecycle did not preserve the expected selection or identity."
  }
}

run "changeSelectorsToNames_sameCatalogEntries_updatedWithoutRetypeOrReplace" {
  command   = apply
  state_key = "selectors"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selectors"
      size        = 30
      description = null
      create_from = {
        source_type = "image"
        image       = { name = var.selector_image_name }
        volume_type = { name = run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].volume_type }
      }
    } }
  }
  assert {
    condition = (
      viettelcloud_volume.volume["selected"].id == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volume_ids["selected"] &&
      viettelcloud_volume.volume["selected"].created_at == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].created_at &&
      viettelcloud_volume.volume["selected"].create_from.image.id == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].source_image.id &&
      viettelcloud_volume.volume["selected"].create_from.volume_type.id == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].source_volume_type.id
    )
    error_message = "Equivalent names changed the volume or its catalog identities."
  }
}

run "changeDescription_catalogCriteria_updatedInPlace" {
  command   = apply
  state_key = "selectors"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selectors"
      size        = 30
      description = "selector update"
      create_from = { source_type = "image", image = var.selector_image, volume_type = var.selector_volume_type }
    } }
  }
  assert {
    condition     = viettelcloud_volume.volume["selected"].create_from.image == merge(var.selector_image, { id = viettelcloud_volume.volume["selected"].create_from.image.id }) && viettelcloud_volume.volume["selected"].create_from.volume_type == merge(var.selector_volume_type, { id = viettelcloud_volume.volume["selected"].create_from.volume_type.id }) && viettelcloud_volume.volume["selected"].id == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volume_ids["selected"] && viettelcloud_volume.volume["selected"].description == "selector update"
    error_message = "The volume selector lifecycle did not preserve the expected selection or identity."
  }
}

run "changeVolumeTypeSelector_catalogCriteria_retypedInPlace" {
  command   = apply
  state_key = "selectors"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selectors"
      size        = 30
      description = "selector update"
      create_from = { source_type = "image", image = var.selector_image, volume_type = var.alternate_volume_type }
    } }
  }
  assert {
    condition     = viettelcloud_volume.volume["selected"].id == run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volume_ids["selected"] && viettelcloud_volume.volume["selected"].create_from.volume_type.id != run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volumes["selected"].source_volume_type.id && viettelcloud_volume.volume["selected"].create_from.volume_type == merge(var.alternate_volume_type, { id = viettelcloud_volume.volume["selected"].create_from.volume_type.id })
    error_message = "The volume selector lifecycle did not preserve the expected selection or identity."
  }
}

run "changeImageSelector_catalogCriteria_volumeReplaced" {
  command   = apply
  state_key = "selectors"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selectors"
      size        = 30
      description = "selector update"
      create_from = { source_type = "image", image = { name = var.selector_alternate_image_name }, volume_type = var.alternate_volume_type }
    } }
  }
  assert {
    condition     = viettelcloud_volume.volume["selected"].id != run.createVolume_imageAndVolumeTypeCriteria_selectorsPreserved.volume_ids["selected"] && viettelcloud_volume.volume["selected"].create_from.image.name == var.selector_alternate_image_name
    error_message = "The volume selector lifecycle did not preserve the expected selection or identity."
  }
}

run "createVolume_catalogCriteriaForImport_volumeCreated" {
  command   = apply
  state_key = "selectors-import-source"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = "tf-test-volume-selector-import"
      size        = 30
      create_from = { source_type = "image", image = var.selector_image, volume_type = var.selector_volume_type }
    } }
  }
  assert {
    condition     = viettelcloud_volume.volume["selected"].create_from.image.id != null && viettelcloud_volume.volume["selected"].create_from.volume_type.id != null
    error_message = "The import owner must record both catalog IDs."
  }
}

# Import refresh returns name selectors. The owner first records those names
# because criterion-based creation leaves the selector names null in state.
run "changeSelectorsToNames_selectorImportOwner_identityPreserved" {
  command   = apply
  state_key = "selectors-import-source"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name = run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].name
      size = run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].size
      create_from = {
        source_type = "image"
        image       = { name = var.selector_image_name }
        volume_type = { name = run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].volume_type }
      }
    } }
  }
  assert {
    condition = (
      viettelcloud_volume.volume["selected"].id == run.createVolume_catalogCriteriaForImport_volumeCreated.volume_ids["selected"] &&
      viettelcloud_volume.volume["selected"].created_at == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].created_at &&
      viettelcloud_volume.volume["selected"].create_from.image.id == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].source_image.id &&
      viettelcloud_volume.volume["selected"].create_from.volume_type.id == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].source_volume_type.id
    )
    error_message = "Name selectors changed the import owner or its catalog identities."
  }
}

run "importVolume_selectorCreatedVolume_backendFieldsReturned" {
  command   = plan
  state_key = "selectors-import"
  module { source = "./fixtures/volume/import" }
  variables {
    volume_id   = run.createVolume_catalogCriteriaForImport_volumeCreated.volume_ids["selected"]
    volume_size = run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].size
    volume_name = run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].name
    image       = { name = run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].source_image.name }
    volume_type = { name = run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].source_volume_type.name }
  }
  assert {
    condition     = viettelcloud_volume.imported.id == run.createVolume_catalogCriteriaForImport_volumeCreated.volume_ids["selected"] && viettelcloud_volume.imported.create_from.image.id == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].source_image.id && viettelcloud_volume.imported.create_from.volume_type.id == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].source_volume_type.id && viettelcloud_volume.imported.size == 30 && viettelcloud_volume.imported.volume_type == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].volume_type && viettelcloud_volume.imported.bootable == true && viettelcloud_volume.imported.zone_id == run.createVolume_catalogCriteriaForImport_volumeCreated.volumes["selected"].zone_id
    error_message = "Import did not refresh the volume created with selectors."
  }
}

# The plan-only import keeps one cleanup owner. Apply alternate criteria to
# that owner to prove that matching catalog IDs preserve the infrastructure.
run "changeSelectorsToCriteria_namedSelectorImportOwner_updatedWithoutRetypeOrReplace" {
  command   = apply
  state_key = "selectors-import-source"
  module { source = "./fixtures/volume/resource" }
  variables {
    volumes = { selected = {
      name        = run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].name
      size        = run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].size
      create_from = { source_type = "image", image = var.selector_image, volume_type = var.selector_volume_type }
    } }
  }
  assert {
    condition = (
      viettelcloud_volume.volume["selected"].id == run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volume_ids["selected"] &&
      viettelcloud_volume.volume["selected"].created_at == run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].created_at &&
      viettelcloud_volume.volume["selected"].volume_type == run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].volume_type &&
      viettelcloud_volume.volume["selected"].create_from.image == merge(var.selector_image, { id = run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].source_image.id }) &&
      viettelcloud_volume.volume["selected"].create_from.volume_type == merge(var.selector_volume_type, { id = run.changeSelectorsToNames_selectorImportOwner_identityPreserved.volumes["selected"].source_volume_type.id })
    )
    error_message = "Alternate criteria changed the import owner or its catalog identities."
  }
}
