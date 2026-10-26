# Test index. Copy a run declaration below to find its block.
# Chain lifecycle (state_key snapshot_lifecycle):
#   run "createSnapshot_availableSource_snapshotAvailable" {
#   run "renameSnapshot_existingSnapshot_nameUpdatedInPlace" {
#   run "planAgain_untrimmedSnapshotName_configuredNamePreserved" {
#   run "setDescription_renamedSnapshot_descriptionUpdatedInPlace" {
#   run "omitDescription_configuredDescription_valuePreserved" {
#   run "clearDescription_configuredDescription_emptyValueReturned" {
#   run "replaceSource_existingSnapshot_newSnapshotCreated" {
#   run "planAgain_replacedSnapshot_noDrift" {
# Chain import_create (state_key snapshot_import_create):
#   run "createImportSubject_availableSource_snapshotAvailable" {
# Chain import_plan (state_key snapshot_import_plan; depends on import_create):
#   run "importSnapshot_existingSnapshot_refreshedWithoutChange" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variables {
  zone        = "vn-central-1a"
  name        = "tf-test-snapshot-lifecycle"
  volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

run "createSnapshot_availableSource_snapshotAvailable" {
  command   = apply
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  assert {
    condition = (
      length(output.snapshot.id) > 0 &&
      output.snapshot.name == var.name &&
      output.snapshot.volume.id == output.source_id &&
      length(output.snapshot.volume_name) > 0 &&
      output.snapshot.size == 30 &&
      output.snapshot.status == "available" &&
      !output.snapshot.from_policy &&
      length(output.snapshot.project_id) > 0 &&
      output.snapshot.zone == var.zone &&
      length(output.snapshot.zone_id) > 0 &&
      length(output.snapshot.created_at) > 0 &&
      length(output.snapshot.updated_at) > 0
    )
    error_message = "The snapshot did not become available with complete state."
  }
}

run "renameSnapshot_existingSnapshot_nameUpdatedInPlace" {
  command   = apply
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables { name = " tf-test-snapshot-renamed " }
  assert {
    condition     = output.snapshot.id == run.createSnapshot_availableSource_snapshotAvailable.snapshot.id && output.snapshot.name == var.name
    error_message = "Renaming the snapshot changed its ID or did not update its name."
  }
}

run "planAgain_untrimmedSnapshotName_configuredNamePreserved" {
  command   = plan
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables { name = " tf-test-snapshot-renamed " }
  assert {
    condition     = output.snapshot.id == run.renameSnapshot_existingSnapshot_nameUpdatedInPlace.snapshot.id && output.snapshot.name == var.name
    error_message = "The normalized snapshot name caused drift in an unchanged plan."
  }
}

run "setDescription_renamedSnapshot_descriptionUpdatedInPlace" {
  command   = apply
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables {
    name        = " tf-test-snapshot-renamed "
    description = "Before upgrade"
  }
  assert {
    condition     = output.snapshot.id == run.createSnapshot_availableSource_snapshotAvailable.snapshot.id && output.snapshot.description == "Before upgrade"
    error_message = "The description update changed the snapshot ID or did not apply."
  }
}

run "omitDescription_configuredDescription_valuePreserved" {
  command   = apply
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables { name = " tf-test-snapshot-renamed " }
  assert {
    condition     = output.snapshot.description == "Before upgrade" && output.snapshot.id == run.createSnapshot_availableSource_snapshotAvailable.snapshot.id
    error_message = "Omitting the description did not preserve the backend value."
  }
}

run "clearDescription_configuredDescription_emptyValueReturned" {
  command   = apply
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables {
    name        = " tf-test-snapshot-renamed "
    description = ""
  }
  assert {
    condition     = output.snapshot.description == "" && output.snapshot.id == run.createSnapshot_availableSource_snapshotAvailable.snapshot.id
    error_message = "An explicit empty description did not clear the value."
  }
}

run "replaceSource_existingSnapshot_newSnapshotCreated" {
  command   = apply
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables {
    name        = " tf-test-snapshot-renamed "
    description = ""
    source_key  = "second"
  }
  assert {
    condition     = output.snapshot.id != run.createSnapshot_availableSource_snapshotAvailable.snapshot.id && output.snapshot.volume.id == output.source_id && output.snapshot.status == "available"
    error_message = "Changing the source volume did not replace the snapshot."
  }
}

run "planAgain_replacedSnapshot_noDrift" {
  command   = plan
  state_key = "snapshot_lifecycle"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables {
    name        = " tf-test-snapshot-renamed "
    description = ""
    source_key  = "second"
  }
  assert {
    condition = (
      output.snapshot.id == run.replaceSource_existingSnapshot_newSnapshotCreated.snapshot.id &&
      output.snapshot.name == run.replaceSource_existingSnapshot_newSnapshotCreated.snapshot.name &&
      output.snapshot.description == "" &&
      output.snapshot.volume.id == run.replaceSource_existingSnapshot_newSnapshotCreated.snapshot.volume.id
    )
    error_message = "The unchanged snapshot configuration planned a state change."
  }
}

run "createImportSubject_availableSource_snapshotAvailable" {
  command   = apply
  state_key = "snapshot_import_create"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/resource" }
  variables {
    name          = "tf-test-snapshot-import"
    source_prefix = "import"
  }
  assert {
    condition     = output.snapshot.status == "available"
    error_message = "The import subject did not become available."
  }
}

run "importSnapshot_existingSnapshot_refreshedWithoutChange" {
  command   = plan
  state_key = "snapshot_import_plan"
  parallel  = true
  module { source = "./fixtures/volume_snapshot/import" }
  variables {
    id        = run.createImportSubject_availableSource_snapshotAvailable.snapshot.id
    name      = run.createImportSubject_availableSource_snapshotAvailable.snapshot.name
    volume_id = run.createImportSubject_availableSource_snapshotAvailable.snapshot.volume.id
  }
  assert {
    condition = (
      output.snapshot.id == run.createImportSubject_availableSource_snapshotAvailable.snapshot.id &&
      output.snapshot.status == "available" &&
      output.snapshot.volume_name == run.createImportSubject_availableSource_snapshotAvailable.snapshot.volume_name &&
      output.snapshot.zone_id == run.createImportSubject_availableSource_snapshotAvailable.snapshot.zone_id
    )
    error_message = "Import did not refresh the snapshot fields."
  }
}
