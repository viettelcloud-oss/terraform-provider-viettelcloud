# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (state_key = "lifecycle"; required inputs only, then a rename
# to an untrimmed name and a filesystem replacement):
#   run "createFilesystemSnapshot_minimal_available" {
#   run "updateFilesystemSnapshot_name_inPlace" {
#   run "planFilesystemSnapshot_untrimmedName_noChanges" {
#   run "updateFilesystemSnapshot_filesystemId_replacement" {
#
# Chain description (state_key = "description"; description set, changed,
# omitted, then cleared):
#   run "createFilesystemSnapshot_withDescription_stateMatches" {
#   run "updateFilesystemSnapshot_description_inPlace" {
#   run "updateFilesystemSnapshot_omitDescription_backendKeepsValue" {
#   run "updateFilesystemSnapshot_clearDescription_backendClears" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# snapshot this chain creates for itself):
#   run "createFilesystemSnapshot_importChainFixture_created" {
#   run "importFilesystemSnapshot_byId_noChanges" {
#
# Chain drift (state_key = "drift", with "drift_deleter" deleting the snapshot
# outside the state that created it):
#   run "createFilesystemSnapshot_driftChainFixture_created" {
#   run "adoptFilesystemSnapshot_secondState_snapshotAdopted" {
#   run "destroyFilesystemSnapshot_allResources_confirmed" {
#   run "deleteFilesystemSnapshot_outsideTerraform_driftDetected" {
#
# Each chain snapshots its own filesystem, because the backend rejects a second
# snapshot of a filesystem that is busy with one (resource_busy). The chains use
# filesystem_id, description_filesystem_id, import_filesystem_id, and
# drift_filesystem_id. The lifecycle chain then moves to
# replacement_filesystem_id. Destroy returns only after the backend reports the
# snapshot as removed.
#
# Provider diagnostics, such as a rejected filesystem ID, are unit-tested.

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

variable "filesystem_id" {
  description = "Existing filesystem that the lifecycle chain snapshots first."
  type        = string
}

variable "description_filesystem_id" {
  description = "Existing filesystem that the description chain snapshots."
  type        = string
}

variable "drift_filesystem_id" {
  description = "Existing filesystem that the drift chain snapshots."
  type        = string
}

variable "replacement_filesystem_id" {
  description = "Second existing filesystem that replaces filesystem_id in the lifecycle chain."
  type        = string
}

variable "import_filesystem_id" {
  description = "Existing filesystem that the import chain snapshots."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

# Creates a snapshot from the required inputs only. Creation waits for
# available, and every computed attribute must be known after apply.
run "createFilesystemSnapshot_minimal_available" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-lifecycle"
    filesystem_id = var.filesystem_id
  }

  assert {
    condition = (
      length(output.snapshot.id) > 0 &&
      output.snapshot.name == "tf-test-fss-lifecycle" &&
      output.snapshot.filesystem.id == var.filesystem_id &&
      length(output.snapshot.filesystem_name) > 0 &&
      output.snapshot.status == "available" &&
      output.snapshot.from_policy == false &&
      output.snapshot.size >= 0 &&
      length(output.snapshot.owner_id) > 0 &&
      length(output.snapshot.project_id) > 0 &&
      length(output.snapshot.created_at) > 0 &&
      length(output.snapshot.updated_at) > 0
    )
    error_message = "The minimal snapshot did not become available with complete state."
  }
}

# Renames the snapshot to a name with outer whitespace. The update is in place,
# so the ID stays, and state keeps the configured spelling.
run "updateFilesystemSnapshot_name_inPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = " tf-test-fss-lifecycle-renamed "
    filesystem_id = var.filesystem_id
  }

  assert {
    condition = (
      output.snapshot.id == run.createFilesystemSnapshot_minimal_available.snapshot.id &&
      output.snapshot.name == " tf-test-fss-lifecycle-renamed " &&
      output.snapshot.status == "available"
    )
    error_message = "Renaming the snapshot changed its ID or did not update its name."
  }
}

# Replanning the untrimmed name plans no change, whatever spelling the backend
# stores. A planned update would leave updated_at unknown and fail the assertion.
run "planFilesystemSnapshot_untrimmedName_noChanges" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = " tf-test-fss-lifecycle-renamed "
    filesystem_id = var.filesystem_id
  }

  assert {
    condition = (
      output.snapshot.id == run.createFilesystemSnapshot_minimal_available.snapshot.id &&
      output.snapshot.name == " tf-test-fss-lifecycle-renamed " &&
      output.snapshot.updated_at == run.updateFilesystemSnapshot_name_inPlace.snapshot.updated_at
    )
    error_message = "The untrimmed name planned a change or was not preserved."
  }
}

# Moves the snapshot to another filesystem. filesystem.id requires replacement,
# so the ID changes and the new snapshot reports the new filesystem.
run "updateFilesystemSnapshot_filesystemId_replacement" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = " tf-test-fss-lifecycle-renamed "
    filesystem_id = var.replacement_filesystem_id
  }

  assert {
    condition = (
      output.snapshot.id != run.createFilesystemSnapshot_minimal_available.snapshot.id &&
      output.snapshot.name == " tf-test-fss-lifecycle-renamed " &&
      output.snapshot.filesystem.id == var.replacement_filesystem_id &&
      output.snapshot.status == "available"
    )
    error_message = "Changing filesystem.id did not replace the snapshot."
  }
}


# Chain description.

# Creates a snapshot with a description. State must equal the configuration.
run "createFilesystemSnapshot_withDescription_stateMatches" {
  command   = apply
  parallel  = true
  state_key = "description"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-description"
    description   = "Initial description"
    filesystem_id = var.description_filesystem_id
  }

  assert {
    condition = (
      output.snapshot.description == "Initial description" &&
      output.snapshot.status == "available"
    )
    error_message = "The snapshot description does not match the configuration."
  }
}

# Changes the description. The update is in place, so the ID stays.
run "updateFilesystemSnapshot_description_inPlace" {
  command   = apply
  parallel  = true
  state_key = "description"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-description"
    description   = "Changed description"
    filesystem_id = var.description_filesystem_id
  }

  assert {
    condition = (
      output.snapshot.id == run.createFilesystemSnapshot_withDescription_stateMatches.snapshot.id &&
      output.snapshot.description == "Changed description"
    )
    error_message = "Changing the description changed the ID or did not update it."
  }
}

# Omits the description. The provider sends no description, so the backend keeps
# the value and state keeps it too.
run "updateFilesystemSnapshot_omitDescription_backendKeepsValue" {
  command   = apply
  parallel  = true
  state_key = "description"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-description"
    filesystem_id = var.description_filesystem_id
  }

  assert {
    condition = (
      output.snapshot.id == run.createFilesystemSnapshot_withDescription_stateMatches.snapshot.id &&
      output.snapshot.description == "Changed description"
    )
    error_message = "Omitting the description did not keep the backend value."
  }
}

# Clears the description with an empty string. The backend stores an empty
# description, which state reports as "" rather than null.
run "updateFilesystemSnapshot_clearDescription_backendClears" {
  command   = apply
  parallel  = true
  state_key = "description"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-description"
    description   = ""
    filesystem_id = var.description_filesystem_id
  }

  assert {
    condition = (
      output.snapshot.id == run.createFilesystemSnapshot_withDescription_stateMatches.snapshot.id &&
      output.snapshot.description == ""
    )
    error_message = "Setting description to an empty string did not clear it."
  }
}


# Chain import.

# Creates the snapshot that the next run imports. This chain owns it, so a
# failure here stays inside the chain.
run "createFilesystemSnapshot_importChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-import"
    description   = "Snapshot for import"
    filesystem_id = var.import_filesystem_id
  }

  assert {
    condition = (
      length(output.snapshot.id) > 0 &&
      output.snapshot.status == "available"
    )
    error_message = "The import chain's snapshot did not become available."
  }
}

# Importing the snapshot restores its state from the backend and plans no
# change. The run stays plan-only under its own state key, so the snapshot that
# the previous run owns does not gain a second owner. The assertions check the
# attributes that only the import refresh supplies; any planned change would
# make the computed values unknown and fail the run.
run "importFilesystemSnapshot_byId_noChanges" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/filesystem_snapshot/import"
  }

  variables {
    id            = run.createFilesystemSnapshot_importChainFixture_created.snapshot.id
    name          = run.createFilesystemSnapshot_importChainFixture_created.snapshot.name
    filesystem_id = run.createFilesystemSnapshot_importChainFixture_created.snapshot.filesystem.id
  }

  assert {
    condition = (
      output.snapshot.id == run.createFilesystemSnapshot_importChainFixture_created.snapshot.id &&
      output.snapshot.description == run.createFilesystemSnapshot_importChainFixture_created.snapshot.description &&
      output.snapshot.status == "available" &&
      output.snapshot.size == run.createFilesystemSnapshot_importChainFixture_created.snapshot.size &&
      output.snapshot.filesystem_name == run.createFilesystemSnapshot_importChainFixture_created.snapshot.filesystem_name &&
      output.snapshot.owner_id == run.createFilesystemSnapshot_importChainFixture_created.snapshot.owner_id &&
      output.snapshot.project_id == run.createFilesystemSnapshot_importChainFixture_created.snapshot.project_id &&
      output.snapshot.created_at == run.createFilesystemSnapshot_importChainFixture_created.snapshot.created_at &&
      length(output.snapshot.updated_at) > 0
    )
    error_message = "Importing the snapshot did not restore the state the backend reports, or it planned a change."
  }
}


# Chain drift.

# Creates the snapshot that the drift_deleter state deletes later in this chain.
run "createFilesystemSnapshot_driftChainFixture_created" {
  command   = apply
  parallel  = true
  state_key = "drift"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-drift"
    filesystem_id = var.drift_filesystem_id
  }

  assert {
    condition     = output.snapshot.status == "available"
    error_message = "The drift chain's snapshot did not become available."
  }
}

# Imports the snapshot into the drift_deleter state. That state can then delete
# the snapshot while the drift state still records it.
run "adoptFilesystemSnapshot_secondState_snapshotAdopted" {
  command   = apply
  parallel  = true
  state_key = "drift_deleter"

  module {
    source = "./fixtures/filesystem_snapshot/adopt"
  }

  variables {
    adopt         = true
    snapshot_id   = run.createFilesystemSnapshot_driftChainFixture_created.snapshot.id
    name          = run.createFilesystemSnapshot_driftChainFixture_created.snapshot.name
    filesystem_id = var.drift_filesystem_id
  }

  assert {
    condition = (
      viettelcloud_fs_filesystem_snapshot.adopted[run.createFilesystemSnapshot_driftChainFixture_created.snapshot.id].id == run.createFilesystemSnapshot_driftChainFixture_created.snapshot.id
    )
    error_message = "The second state did not adopt the drift chain snapshot."
  }
}

# Removing the snapshot from the drift_deleter configuration destroys it on the
# backend. Apply returns only after the delete waiter sees HTTP 404, which
# covers AC9 and confirms the 404 assumption of design doc question Q4.
run "destroyFilesystemSnapshot_allResources_confirmed" {
  command   = apply
  parallel  = true
  state_key = "drift_deleter"

  module {
    source = "./fixtures/filesystem_snapshot/adopt"
  }

  variables {
    adopt         = false
    snapshot_id   = run.createFilesystemSnapshot_driftChainFixture_created.snapshot.id
    name          = run.createFilesystemSnapshot_driftChainFixture_created.snapshot.name
    filesystem_id = var.drift_filesystem_id
  }

  assert {
    condition     = length(viettelcloud_fs_filesystem_snapshot.adopted) == 0
    error_message = "The drift_deleter state still holds the snapshot after destroy."
  }
}

# The drift state still records the deleted snapshot. Refresh removes it from
# state and apply creates a new snapshot, so the ID changes. The assertion reads
# adopted_count from the destroy run, which keeps this run behind the deletion.
# Teardown deletes the new snapshot.
run "deleteFilesystemSnapshot_outsideTerraform_driftDetected" {
  command   = apply
  parallel  = true
  state_key = "drift"

  module {
    source = "./fixtures/filesystem_snapshot/resource"
  }

  variables {
    name          = "tf-test-fss-drift"
    filesystem_id = var.drift_filesystem_id
  }

  assert {
    condition = (
      run.destroyFilesystemSnapshot_allResources_confirmed.adopted_count == 0 &&
      output.snapshot.id != run.createFilesystemSnapshot_driftChainFixture_created.snapshot.id &&
      output.snapshot.name == "tf-test-fss-drift" &&
      output.snapshot.status == "available"
    )
    error_message = "Refresh did not detect the deleted snapshot, or the snapshot was not created again."
  }
}
