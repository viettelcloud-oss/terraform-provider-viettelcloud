# Test index. Each backup chain depends on the shared source volumes.
# Shared source state_key "sources":
#   run "createVolumes_backupSources_volumesAvailable" {
# Lifecycle state_key "lifecycle":
#   run "createBackup_descriptionSet_backupAvailable" {
#   run "omitDescription_existingBackup_valuePreserved" {
#   run "clearDescription_existingBackup_valueCleared" {
#   run "renameBackup_existingBackup_updatedInPlace" {
#   run "planBackup_normalizedName_noDrift" {
#   run "replaceVolumeId_existingBackup_newBackupCreated" {
# Import state_key "import", then plan-only state_key "import_plan":
#   run "createBackup_importChain_backupAvailable" {
#   run "importBackup_existingBackup_refreshMatches" {

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
    condition     = length(output.volume_ids) == 2
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
