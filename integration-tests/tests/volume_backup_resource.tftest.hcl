# Test index. Each backup chain depends on the shared source volumes.
# Shared source state_key "sources":
#   run "createVolumes_backupSources_volumesAvailable" {
# Lifecycle state_key "lifecycle":
#   run "createBackup_descriptionSet_backupAvailable" {
#   run "omitDescription_existingBackup_valuePreserved" {
#   run "clearDescription_existingBackup_valueCleared" {
#   run "renameBackup_existingBackup_updatedInPlace" {
#   run "planBackup_normalizedName_noDrift" {
#   run "changeTimeouts_existingBackup_updatedInPlace" {
#   run "planBackup_changedTimeouts_noDrift" {
#   run "replaceVolumeId_existingBackup_newBackupCreated" {
# Import state_key "import", then plan-only state_key "import_plan":
#   run "createBackup_importChain_backupAvailable" {
#   run "importBackup_existingBackup_refreshMatches" {
# Restore state_key "restore". The chain restores into the restore_first and
# restore_second volumes, never into its own backup source. Its last run
# restores into restore_attached_volume_id, which the operator provides:
#   run "createBackup_restoreChain_backupAvailable" {
#   run "restoreBackup_existingVolume_volumeRestored" {
#   run "changeRestoreVolume_otherVolume_volumeRestored" {
#   run "changeRestoreVolumeCase_sameVolume_updatedWithoutRestore" {
#   run "planBackup_restoreVolumeCase_noDrift" {
#   run "removeRestoreVolume_existingBackup_noApiCall" {
#   run "restoreBackup_attachedVolume_volumeInUse" {

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

# The operator provides this volume, because attaching one here would need a
# server and its network. The volume must be attached only to servers that are
# shut down, and the restore overwrites its data.
variable "restore_attached_volume_id" {
  description = "Existing unencrypted volume of at least 30 GiB, in the test region, attached only to shut-down servers."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variables {
  zone        = "vn-central-1a"
  volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

run "createVolumes_backupSources_volumesAvailable" {
  command   = apply
  state_key = "sources"
  parallel  = true
  module { source = "./fixtures/volume_backup/source" }
  assert {
    condition     = length(output.volume_ids) == 5
    error_message = "The backup source volumes were not created."
  }
}

run "createBackup_descriptionSet_backupAvailable" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name        = "tf-test-volume-backup-lifecycle"
    description = "first"
  }
  assert {
    condition = (
      length(output.backup.id) > 0 &&
      output.backup.name == "tf-test-volume-backup-lifecycle" &&
      output.backup.description == "first" &&
      output.backup.volume.id == run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"] &&
      output.backup.volume_name == "tf-test-backup-source-primary" &&
      output.backup.status == "available" &&
      output.backup.size == 30 &&
      output.backup.expired == false &&
      try(length(output.backup.disk_type), 0) > 0 &&
      length(output.backup.encryption_key_size) > 0 &&
      length(output.backup.project_id) > 0 &&
      length(output.backup.region) > 0 &&
      length(output.backup.region_id) > 0 &&
      output.backup.policy_id == null &&
      length(output.backup.created_at) > 0 &&
      length(output.backup.updated_at) > 0
    )
    error_message = "The created backup returned incomplete state."
  }
}

run "omitDescription_existingBackup_valuePreserved" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name      = "tf-test-volume-backup-lifecycle"
  }
  assert {
    condition     = output.backup.id == run.createBackup_descriptionSet_backupAvailable.backup.id && output.backup.description == "first"
    error_message = "Omitting the description changed the backup."
  }
}

run "clearDescription_existingBackup_valueCleared" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name        = "tf-test-volume-backup-lifecycle"
    description = ""
  }
  assert {
    condition     = output.backup.id == run.createBackup_descriptionSet_backupAvailable.backup.id && output.backup.description == ""
    error_message = "An empty description did not clear the backup description."
  }
}

run "renameBackup_existingBackup_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name        = " tf-test-volume-backup-renamed "
    description = ""
  }
  assert {
    condition     = output.backup.id == run.createBackup_descriptionSet_backupAvailable.backup.id && output.backup.name == " tf-test-volume-backup-renamed "
    error_message = "Renaming the backup replaced it or kept the old name."
  }
}

run "planBackup_normalizedName_noDrift" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name        = " tf-test-volume-backup-renamed "
    description = ""
  }
  assert {
    condition     = output.backup.id == run.createBackup_descriptionSet_backupAvailable.backup.id && length(output.backup.updated_at) > 0
    error_message = "The normalized backup name produced another change."
  }
}

# AC24: a change of timeouts alone updates in place and keeps the backup.
run "changeTimeouts_existingBackup_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name        = " tf-test-volume-backup-renamed "
    description = ""
    timeouts    = { create = "45m", update = "90m" }
  }
  assert {
    condition     = output.backup.id == run.createBackup_descriptionSet_backupAvailable.backup.id && output.backup.name == " tf-test-volume-backup-renamed " && output.backup.timeouts.update == "90m"
    error_message = "Changing timeouts replaced the backup or did not record the new value."
  }
}

# A planned change makes updated_at unknown, so this assertion fails on drift.
run "planBackup_changedTimeouts_noDrift" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name        = " tf-test-volume-backup-renamed "
    description = ""
    timeouts    = { create = "45m", update = "90m" }
  }
  assert {
    condition     = output.backup.id == run.createBackup_descriptionSet_backupAvailable.backup.id && length(output.backup.updated_at) > 0
    error_message = "The configured timeouts produced another change."
  }
}

run "replaceVolumeId_existingBackup_newBackupCreated" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id   = run.createVolumes_backupSources_volumesAvailable.volume_ids["replacement"]
    name        = " tf-test-volume-backup-renamed "
    description = ""
  }
  assert {
    condition     = output.backup.id != run.createBackup_descriptionSet_backupAvailable.backup.id && output.backup.volume.id == run.createVolumes_backupSources_volumesAvailable.volume_ids["replacement"]
    error_message = "Changing volume.id did not replace the backup."
  }
}

run "createBackup_importChain_backupAvailable" {
  command   = apply
  state_key = "import"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id = run.createVolumes_backupSources_volumesAvailable.volume_ids["primary"]
    name      = "tf-test-volume-backup-import"
  }
  assert {
    condition     = output.backup.status == "available"
    error_message = "The import-chain backup is not available."
  }
}

run "importBackup_existingBackup_refreshMatches" {
  command   = plan
  state_key = "import_plan"
  parallel  = true
  module { source = "./fixtures/volume_backup/import" }
  variables {
    backup_id = run.createBackup_importChain_backupAvailable.backup.id
    volume_id = run.createBackup_importChain_backupAvailable.backup.volume.id
    name      = run.createBackup_importChain_backupAvailable.backup.name
  }
  assert {
    condition     = output.backup.id == run.createBackup_importChain_backupAvailable.backup.id && output.backup.status == "available" && output.backup.project_id == run.createBackup_importChain_backupAvailable.backup.project_id
    error_message = "Import did not refresh the existing backup."
  }
}

run "createBackup_restoreChain_backupAvailable" {
  command   = apply
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name      = "tf-test-volume-backup-restore"
  }
  assert {
    condition     = output.backup.status == "available" && output.backup.restore_volume == null
    error_message = "The restore-chain backup is not available or has a restore volume."
  }
}

# AC14: adding restore_volume restores in place and waits for both objects.
run "restoreBackup_existingVolume_volumeRestored" {
  command   = apply
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id         = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name              = "tf-test-volume-backup-restore"
    restore_volume_id = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_first"]
  }
  assert {
    condition = (
      output.backup.id == run.createBackup_restoreChain_backupAvailable.backup.id &&
      output.backup.status == "available" &&
      output.backup.restore_volume.id == run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_first"] &&
      output.restore_target_status == "available"
    )
    error_message = "The restore replaced the backup, or the backup or the volume is not available after it."
  }
}

# AC16: a restore_volume that names another volume restores into that volume.
run "changeRestoreVolume_otherVolume_volumeRestored" {
  command   = apply
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id         = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name              = "tf-test-volume-backup-restore"
    restore_volume_id = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_second"]
  }
  assert {
    condition = (
      output.backup.id == run.createBackup_restoreChain_backupAvailable.backup.id &&
      output.backup.status == "available" &&
      output.backup.restore_volume.id == run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_second"] &&
      output.restore_target_status == "available"
    )
    error_message = "The restore into another volume replaced the backup, or the backup or the volume is not available after it."
  }
}

# AC16: the same UUID in another spelling updates state only and keeps the
# configured spelling.
run "changeRestoreVolumeCase_sameVolume_updatedWithoutRestore" {
  command   = apply
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id         = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name              = "tf-test-volume-backup-restore"
    restore_volume_id = " ${upper(run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_second"])} "
  }
  assert {
    condition = (
      output.backup.id == run.createBackup_restoreChain_backupAvailable.backup.id &&
      output.backup.restore_volume.id == " ${upper(run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_second"])} "
    )
    error_message = "A new spelling of the same restore volume replaced the backup or lost the configured spelling."
  }
}

# A planned change makes updated_at unknown, so this assertion fails on drift.
run "planBackup_restoreVolumeCase_noDrift" {
  command   = plan
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id         = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name              = "tf-test-volume-backup-restore"
    restore_volume_id = " ${upper(run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_second"])} "
  }
  assert {
    condition     = output.backup.id == run.createBackup_restoreChain_backupAvailable.backup.id && length(output.backup.updated_at) > 0
    error_message = "The configured spelling of restore_volume produced another change."
  }
}

# AC17: removing restore_volume changes state only.
run "removeRestoreVolume_existingBackup_noApiCall" {
  command   = apply
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name      = "tf-test-volume-backup-restore"
  }
  assert {
    condition     = output.backup.id == run.createBackup_restoreChain_backupAvailable.backup.id && output.backup.restore_volume == null && output.backup.status == "available"
    error_message = "Removing restore_volume replaced the backup or kept the restore volume."
  }
}

# AC21: a restore into a volume attached to a shut-down server leaves the
# volume in-use.
run "restoreBackup_attachedVolume_volumeInUse" {
  command   = apply
  state_key = "restore"
  parallel  = true
  module { source = "./fixtures/volume_backup/resource" }
  variables {
    volume_id         = run.createVolumes_backupSources_volumesAvailable.volume_ids["restore_source"]
    name              = "tf-test-volume-backup-restore"
    restore_volume_id = var.restore_attached_volume_id
  }
  assert {
    condition = (
      output.backup.id == run.createBackup_restoreChain_backupAvailable.backup.id &&
      output.backup.status == "available" &&
      output.backup.restore_volume.id == var.restore_attached_volume_id &&
      output.restore_target_status == "in-use"
    )
    error_message = "The restore into the attached volume replaced the backup, or the volume is not in-use after it. Check that restore_attached_volume_id is attached to a shut-down server."
  }
}
