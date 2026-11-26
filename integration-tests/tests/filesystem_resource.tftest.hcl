# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Each chain creates one filesystem, except from_snapshot, which also creates a
# source filesystem and two snapshots of it, and drift, which creates its
# filesystem again after deleting it. The lifecycle, replacement_protocol,
# replacement_type, replacement_subnet, import, from_snapshot, and drift chains
# run concurrently because they hold separate state keys.
#
# Chain lifecycle (state_key = "lifecycle"):
#   run "createFilesystem_minimalConfig_filesystemAvailable" {
#   run "refresh_existingFilesystem_statePreserved" {
#   run "addDescription_existingFilesystem_descriptionSetInPlace" {
#   run "updateDescription_existingFilesystem_descriptionUpdatedInPlace" {
#   run "omitDescription_descriptionSet_valuePreservedInPlace" {
#   run "clearDescription_descriptionSet_valueClearedInPlace" {
#   run "resizeFilesystem_existingFilesystem_sizeIncreasedInPlace" {
#   run "respellStrings_existingFilesystem_configuredSpellingKeptInPlace" {
#   run "refresh_respelledStrings_noChangesPlanned" {
#
# Chain replacement_protocol (state_key = "replacement_protocol"):
#   run "createFilesystem_protocolChain_filesystemCreated" {
#   run "changeProtocol_nfsFilesystem_filesystemReplaced" {
#
# Chain replacement_type (state_key = "replacement_type"):
#   run "createFilesystem_typeChain_filesystemCreated" {
#   run "changeFilesystemType_existingFilesystem_filesystemReplaced" {
#
# Chain replacement_subnet (state_key = "replacement_subnet"):
#   run "createFilesystem_subnetChain_filesystemCreated" {
#   run "changeSubnet_existingFilesystem_filesystemReplaced" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import):
#   run "createFilesystem_importChainFixture_filesystemCreated" {
#   run "importFilesystem_byId_noChangesPlanned" {
#
# Chain from_snapshot (state_key = "snapshot_source" -> "from_snapshot"):
#   run "createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable" {
#   run "createFilesystem_fromSnapshot_filesystemRestored" {
#   run "refresh_fromSnapshotFilesystem_statePreserved" {
#   run "resizeFilesystem_fromSnapshotFilesystem_sizeIncreasedInPlace" {
#   run "changeSnapshot_fromSnapshotFilesystem_filesystemReplaced" {
#
# Chain drift (state_key = "drift", with "drift_deleter" deleting the filesystem
# outside the state that created it):
#   run "createFilesystem_driftChainFixture_filesystemCreated" {
#   run "adoptFilesystem_secondState_filesystemAdopted" {
#   run "destroyFilesystem_secondState_deletionConfirmed" {
#   run "deleteFilesystem_outsideTerraform_driftDetected" {

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

# Existing subnet whose zone carries the NFS filesystem type named by
# filesystem_type_name. Set via TF_VAR_subnet_id.
variable "subnet_id" {
  type        = string
  description = "UUID of an existing subnet for the NFS filesystem chains."
}

# Second subnet for the subnet replacement chain. Must be different from
# subnet_id. Set via TF_VAR_subnet_id_b.
variable "subnet_id_b" {
  type        = string
  description = "UUID of a second existing subnet for the subnet replacement chain."
}

# SMB filesystem type name in the zone of subnet_id. Required by the protocol
# replacement chain. Set via TF_VAR_smb_filesystem_type_name.
variable "smb_filesystem_type_name" {
  type        = string
  description = "Name of an enabled SMB filesystem type in the zone of subnet_id."
}

# Second NFS filesystem type name for the filesystem_type replacement chain.
# Must be different from filesystem_type_name. Set via TF_VAR_filesystem_type_name_b.
variable "filesystem_type_name_b" {
  type        = string
  description = "Name of a second enabled NFS filesystem type in zone_name, different from filesystem_type_name."
}

# Enabled filesystem type in zone_name that the NFS filesystems use. Set via
# TF_VAR_filesystem_type_name.
variable "filesystem_type_name" {
  type        = string
  description = "Name of an enabled filesystem type in zone_name."
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Shared module defaults for all chains. Each run block may override them.
variables {
  zone_name = "vn-central-1a"
}

# Chain lifecycle.

run "createFilesystem_minimalConfig_filesystemAvailable" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-lifecycle"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition = (
      length(output.filesystem_id) > 0 &&
      output.filesystem_name == "tf-test-fs-lifecycle" &&
      output.filesystem_size == 100 &&
      output.filesystem_protocol == "NFS" &&
      output.filesystem_description == null &&
      output.filesystem_status == "available" &&
      length(output.filesystem_zone_name) > 0 &&
      length(output.filesystem_zone_id) > 0 &&
      length(output.filesystem_subnet_id) > 0 &&
      length(output.filesystem_mount_target_address) > 0 &&
      length(output.filesystem_project_id) > 0 &&
      length(output.filesystem_created_at) > 0
    )
    error_message = "The created filesystem returned incomplete state."
  }
}

run "refresh_existingFilesystem_statePreserved" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-lifecycle"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_status == "available"
    )
    error_message = "Filesystem state was not preserved during refresh."
  }
}

run "addDescription_existingFilesystem_descriptionSetInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name        = "tf-test-fs-lifecycle"
    size        = 100
    description = "first"
    subnet_id   = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_description == "first"
    )
    error_message = "Adding a description did not update the filesystem in place."
  }
}

run "updateDescription_existingFilesystem_descriptionUpdatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name        = "tf-test-fs-lifecycle-renamed"
    size        = 100
    description = "second"
    subnet_id   = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_name == "tf-test-fs-lifecycle-renamed" &&
      output.filesystem_description == "second"
    )
    error_message = "Updating name and description did not update the filesystem in place."
  }
}

run "omitDescription_descriptionSet_valuePreservedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-lifecycle-renamed"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_description == "second"
    )
    error_message = "Omitting the description changed the filesystem or its description."
  }
}

run "clearDescription_descriptionSet_valueClearedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name        = "tf-test-fs-lifecycle-renamed"
    size        = 100
    description = ""
    subnet_id   = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_description == ""
    )
    error_message = "Setting description to empty did not clear the filesystem description."
  }
}

run "resizeFilesystem_existingFilesystem_sizeIncreasedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name        = "tf-test-fs-lifecycle-renamed"
    size        = 150
    description = ""
    subnet_id   = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_size == 150 &&
      output.filesystem_status == "available"
    )
    error_message = "Resizing the filesystem did not increase the size in place."
  }
}

# AC15: the provider sends trimmed strings and keeps the configured spelling.
# The padded name, the protocol, and the upper-case subnet ID differ from state
# only by spelling, so the filesystem keeps its ID. The description is a real
# change from "" and is sent trimmed.
run "respellStrings_existingFilesystem_configuredSpellingKeptInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name        = "  tf-test-fs-lifecycle-renamed "
    size        = 150
    protocol    = " NFS "
    description = " padded "
    subnet_id   = " ${upper(var.subnet_id)} "
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_name == "  tf-test-fs-lifecycle-renamed " &&
      output.filesystem_protocol == " NFS " &&
      output.filesystem_description == " padded " &&
      output.filesystem_subnet_id == var.subnet_id
    )
    error_message = "A change of spelling replaced the filesystem or lost the configured spelling."
  }
}

# A planned change would make status unknown and fail this plan-only run, so
# the padded configuration must plan no change after refresh.
run "refresh_respelledStrings_noChangesPlanned" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name        = "  tf-test-fs-lifecycle-renamed "
    size        = 150
    protocol    = " NFS "
    description = " padded "
    subnet_id   = " ${upper(var.subnet_id)} "
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_minimalConfig_filesystemAvailable.filesystem_id &&
      output.filesystem_status == "available"
    )
    error_message = "The padded configuration planned a change after refresh."
  }
}

# Chain replacement_protocol.

run "createFilesystem_protocolChain_filesystemCreated" {
  command   = apply
  state_key = "replacement_protocol"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-protocol-nfs"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition     = output.filesystem_protocol == "NFS" && output.filesystem_status == "available"
    error_message = "The protocol chain NFS filesystem was not created."
  }
}

run "changeProtocol_nfsFilesystem_filesystemReplaced" {
  command   = apply
  state_key = "replacement_protocol"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name                 = "tf-test-fs-protocol-smb"
    size                 = 100
    protocol             = "SMB"
    filesystem_type_name = var.smb_filesystem_type_name
    subnet_id            = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id != run.createFilesystem_protocolChain_filesystemCreated.filesystem_id &&
      output.filesystem_protocol == "SMB" &&
      output.filesystem_status == "available"
    )
    error_message = "Changing the protocol did not replace the filesystem."
  }
}

# Chain replacement_type.

run "createFilesystem_typeChain_filesystemCreated" {
  command   = apply
  state_key = "replacement_type"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-type-a"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition     = length(output.filesystem_id) > 0 && output.filesystem_status == "available"
    error_message = "The type chain filesystem was not created."
  }
}

run "changeFilesystemType_existingFilesystem_filesystemReplaced" {
  command   = apply
  state_key = "replacement_type"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name                 = "tf-test-fs-type-b"
    size                 = 100
    filesystem_type_name = var.filesystem_type_name_b
    subnet_id            = var.subnet_id
  }

  assert {
    condition = (
      output.filesystem_id != run.createFilesystem_typeChain_filesystemCreated.filesystem_id &&
      output.filesystem_status == "available"
    )
    error_message = "Changing the filesystem type did not replace the filesystem."
  }
}

# Chain replacement_subnet.

run "createFilesystem_subnetChain_filesystemCreated" {
  command   = apply
  state_key = "replacement_subnet"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-subnet-a"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition     = output.filesystem_subnet_id == var.subnet_id && output.filesystem_status == "available"
    error_message = "The subnet chain filesystem was not created on subnet_id."
  }
}

run "changeSubnet_existingFilesystem_filesystemReplaced" {
  command   = apply
  state_key = "replacement_subnet"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-subnet-b"
    size      = 100
    subnet_id = var.subnet_id_b
  }

  assert {
    condition = (
      output.filesystem_id != run.createFilesystem_subnetChain_filesystemCreated.filesystem_id &&
      output.filesystem_subnet_id == var.subnet_id_b &&
      output.filesystem_status == "available"
    )
    error_message = "Changing the subnet did not replace the filesystem."
  }
}

# Chain import.
#
# The import chain creates the filesystem it later imports so no other chain
# shares its object. The import fixture leaves description unconfigured, so the
# value the plan reports can only come from the import refresh.

run "createFilesystem_importChainFixture_filesystemCreated" {
  command   = apply
  state_key = "import"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-import"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition     = output.filesystem_status == "available" && length(output.filesystem_id) > 0
    error_message = "The import chain filesystem was not created."
  }
}

# The import fixture holds the import block. A plan-only run leaves the object
# in state_key "import", so teardown deletes it once. Using state_key
# "import_target" keeps it plan-only and separates its state from "import".
run "importFilesystem_byId_noChangesPlanned" {
  command   = plan
  state_key = "import_target"
  parallel  = true
  module { source = "./fixtures/filesystem/import" }
  variables {
    filesystem_id = run.createFilesystem_importChainFixture_filesystemCreated.filesystem_id
    name          = run.createFilesystem_importChainFixture_filesystemCreated.filesystem_name
    size          = run.createFilesystem_importChainFixture_filesystemCreated.filesystem_size
    protocol      = run.createFilesystem_importChainFixture_filesystemCreated.filesystem_protocol
    subnet_id     = run.createFilesystem_importChainFixture_filesystemCreated.filesystem_subnet_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_importChainFixture_filesystemCreated.filesystem_id &&
      output.filesystem_status == "available" &&
      length(output.filesystem_project_id) > 0 &&
      length(output.filesystem_mount_target_address) > 0
    )
    error_message = "Import did not restore the filesystem state or the refresh returned unexpected values."
  }
}

# Chain from_snapshot.
#
# The chain creates its own source filesystem and two snapshots, so no
# pre-existing snapshot is needed. Teardown destroys state_key "from_snapshot"
# before "snapshot_source", so the restored filesystem goes before the
# snapshots it was created from. backend_snapshot_id comes from a data source
# read of the restored filesystem, so it proves what the backend recorded
# rather than the configured value.

run "createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable" {
  command   = apply
  state_key = "snapshot_source"
  parallel  = true
  module { source = "./fixtures/filesystem/snapshot_source" }
  variables {
    name      = "tf-test-fs-snapshot-source"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition = (
      length(output.filesystem_id) > 0 &&
      output.snapshot_a_status == "available" &&
      output.snapshot_b_status == "available" &&
      output.snapshot_a_id != output.snapshot_b_id
    )
    error_message = "The source filesystem or its two snapshots were not created."
  }
}

# Q8: the configuration has only name and snapshot, so the backend takes size,
# protocol, filesystem_type, and subnet from the source filesystem of
# snapshot A, which snapshot_source creates with 100 GiB, NFS,
# filesystem_type_name, and subnet_id.
run "createFilesystem_fromSnapshot_filesystemRestored" {
  command   = apply
  state_key = "from_snapshot"
  parallel  = true
  module { source = "./fixtures/filesystem/from_snapshot" }
  variables {
    name        = "tf-test-fs-from-snapshot"
    snapshot_id = run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_a_id
  }

  assert {
    condition = (
      length(output.filesystem_id) > 0 &&
      output.filesystem_id != run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.filesystem_id &&
      output.filesystem_status == "available" &&
      output.filesystem_snapshot_id == run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_a_id &&
      output.backend_snapshot_id == run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_a_id
    )
    error_message = "The filesystem was not created from snapshot A, or the backend does not report snapshot A as its source."
  }

  assert {
    condition = (
      output.filesystem_size == 100 &&
      output.filesystem_protocol == "NFS" &&
      output.filesystem_type_name == var.filesystem_type_name &&
      output.filesystem_type_id == output.backend_filesystem_type_id &&
      lower(output.filesystem_subnet_id) == lower(var.subnet_id)
    )
    error_message = "The size, protocol, filesystem_type, or subnet of the filesystem differ from the source filesystem of snapshot A."
  }
}

# A plan that changes nothing keeps status known; any planned update would make
# the computed status unknown and fail the condition.
run "refresh_fromSnapshotFilesystem_statePreserved" {
  command   = plan
  state_key = "from_snapshot"
  parallel  = true
  module { source = "./fixtures/filesystem/from_snapshot" }
  variables {
    name        = "tf-test-fs-from-snapshot"
    snapshot_id = run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_a_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_fromSnapshot_filesystemRestored.filesystem_id &&
      output.filesystem_status == "available" &&
      output.filesystem_size == 100 &&
      output.filesystem_snapshot_id == run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_a_id
    )
    error_message = "Refreshing the filesystem created from a snapshot planned a change."
  }
}

# AC17: adding a size larger than the size from the snapshot grows the
# filesystem in place.
run "resizeFilesystem_fromSnapshotFilesystem_sizeIncreasedInPlace" {
  command   = apply
  state_key = "from_snapshot"
  parallel  = true
  module { source = "./fixtures/filesystem/from_snapshot" }
  variables {
    name        = "tf-test-fs-from-snapshot"
    size        = 200
    snapshot_id = run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_a_id
  }

  assert {
    condition = (
      output.filesystem_id == run.createFilesystem_fromSnapshot_filesystemRestored.filesystem_id &&
      output.filesystem_status == "available" &&
      output.filesystem_size == 200 &&
      output.backend_size == 200
    )
    error_message = "Adding a larger size did not grow the filesystem created from a snapshot in place."
  }
}

# The run omits size again before it changes snapshot.id, because a create with
# snapshot and size reports Conflicting filesystem source. The new filesystem
# takes the 100 GiB of the source filesystem of snapshot B.
run "changeSnapshot_fromSnapshotFilesystem_filesystemReplaced" {
  command   = apply
  state_key = "from_snapshot"
  parallel  = true
  module { source = "./fixtures/filesystem/from_snapshot" }
  variables {
    name        = "tf-test-fs-from-snapshot"
    snapshot_id = run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_b_id
  }

  assert {
    condition = (
      output.filesystem_id != run.createFilesystem_fromSnapshot_filesystemRestored.filesystem_id &&
      output.filesystem_status == "available" &&
      output.filesystem_size == 100 &&
      output.backend_snapshot_id == run.createSnapshotSource_nfsFilesystem_twoSnapshotsAvailable.snapshot_b_id
    )
    error_message = "Changing snapshot.id did not replace the filesystem with one created from snapshot B."
  }
}

# Chain drift.
#
# The drift_deleter state imports the filesystem that the drift state created,
# then removes it from its configuration, which deletes it on the backend while
# the drift state still records it.

run "createFilesystem_driftChainFixture_filesystemCreated" {
  command   = apply
  state_key = "drift"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-drift"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition     = output.filesystem_status == "available" && length(output.filesystem_id) > 0
    error_message = "The drift chain filesystem was not created."
  }
}

run "adoptFilesystem_secondState_filesystemAdopted" {
  command   = apply
  state_key = "drift_deleter"
  parallel  = true
  module { source = "./fixtures/filesystem/adopt" }
  variables {
    adopt         = true
    filesystem_id = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_id
    name          = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_name
    size          = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_size
    protocol      = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_protocol
    subnet_id     = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_subnet_id
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem.adopted[run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_id].id == run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_id
    )
    error_message = "The second state did not adopt the drift chain filesystem."
  }
}

# Removing the filesystem from the drift_deleter configuration destroys it on
# the backend. Apply returns only after the delete waiter sees HTTP 404.
run "destroyFilesystem_secondState_deletionConfirmed" {
  command   = apply
  state_key = "drift_deleter"
  parallel  = true
  module { source = "./fixtures/filesystem/adopt" }
  variables {
    adopt         = false
    filesystem_id = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_id
    name          = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_name
    size          = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_size
    protocol      = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_protocol
    subnet_id     = run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_subnet_id
  }

  assert {
    condition     = output.adopted_count == 0
    error_message = "The drift_deleter state still holds the filesystem after destroy."
  }
}

# The drift state still records the deleted filesystem. Refresh removes it from
# state and apply creates a new filesystem, so the ID changes (AC7). The
# assertion reads adopted_count from the destroy run, which keeps this run
# behind the deletion. Teardown deletes the new filesystem.
run "deleteFilesystem_outsideTerraform_driftDetected" {
  command   = apply
  state_key = "drift"
  parallel  = true
  module { source = "./fixtures/filesystem/resource" }
  variables {
    name      = "tf-test-fs-drift"
    size      = 100
    subnet_id = var.subnet_id
  }

  assert {
    condition = (
      run.destroyFilesystem_secondState_deletionConfirmed.adopted_count == 0 &&
      output.filesystem_id != run.createFilesystem_driftChainFixture_filesystemCreated.filesystem_id &&
      output.filesystem_name == "tf-test-fs-drift" &&
      output.filesystem_status == "available"
    )
    error_message = "Refresh did not detect the deleted filesystem, or the filesystem was not created again."
  }
}
