# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# The backend refuses a backup while another backup of the same DB instance
# runs, so no run sets parallel and the chains run one after another. Set
# db_root_password, for example as TF_VAR_db_root_password, and the catalog
# values in the variables block below.
#
# Chain network (shared setup; state_key = "network"):
#   run "createNetwork_dbBackupSuite_subnetReturned" {
#
# Chain sources (shared setup; state_key = "sources"):
#   run "createDbInstances_backupSources_instancesActive" {
#
# Chain lifecycle (state_key = "lifecycle"):
#   run "createDbInstanceBackup_requiredOnly_becomesCompleted" {
#   run "planAgain_createdBackup_noChanges" {
#   run "changeDescription_existingBackup_updatedInPlace" {
#   run "omitDescription_existingBackup_valuePreserved" {
#   run "clearDescription_existingBackup_valueCleared" {
#   run "changeName_existingBackup_replaced" {
#   run "changeDbInstance_existingBackup_replaced" {
#
# Chain import (state_key = "import" -> "import_plan"; plan-only import):
#   run "createDbInstanceBackup_importChain_becomesCompleted" {
#   run "planImport_existingBackup_refreshMatches" {
#
# Chain keep_backup (state_key = "keep_backup"):
#   run "createDbInstanceAndBackup_keepBackupDefault_backupCompleted" {
#   run "destroyDbInstance_keepBackupTrue_backupRemains" {
#   run "refreshBackup_instanceDeleted_instanceIdPreserved" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

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

variable "db_root_password" {
  type        = string
  sensitive   = true
  description = "Root password for every test DB instance: 8 to 32 characters with lower and upper case letters, a digit, and a symbol other than @."
}

variables {
  region        = "vn-central-1"
  database_type = "postgresql"
  db_version    = "16"
  flavor        = "m1.small"
  volume_type   = "ssd"
  solution      = "basic"
}

run "createNetwork_dbBackupSuite_subnetReturned" {
  command   = apply
  state_key = "network"
  module { source = "./fixtures/db_instance/network" }
  variables {
    name = "tf-test-db-backup-network"
    cidr = "10.43.0.0/16"
  }
  assert {
    condition     = length(output.subnet_id) > 0
    error_message = "The shared subnet was not created."
  }
}

run "createDbInstances_backupSources_instancesActive" {
  command   = apply
  state_key = "sources"
  module { source = "./fixtures/db_backup/source" }
  variables {
    subnet_id     = run.createNetwork_dbBackupSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition     = length(output.instance_ids) == 2
    error_message = "The backup source DB instances were not created."
  }
}

run "createDbInstanceBackup_requiredOnly_becomesCompleted" {
  command   = apply
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-lifecycle"
  }
  assert {
    condition = (
      length(output.backup.id) > 0 &&
      output.backup.status == "COMPLETED" &&
      output.backup.backup_type == "manual" &&
      output.backup.name == "tf-test-db-backup-lifecycle" &&
      output.backup.description == "" &&
      output.backup.db_instance.id == run.createDbInstances_backupSources_instancesActive.instance_ids["primary"] &&
      output.backup.db_instance_name == "tf-test-db-backup-source-primary" &&
      output.backup.database_type == var.database_type &&
      output.backup.db_version == var.db_version && length(output.backup.db_version_id) > 0 &&
      output.backup.region == var.region && length(output.backup.region_id) > 0 &&
      output.backup.size >= 0 && output.backup.required_volume_size > 0 &&
      length(output.backup.backup_timestamp) > 0 &&
      length(output.backup.owner_id) > 0 && length(output.backup.project_id) > 0 &&
      length(output.backup.created_at) > 0 && length(output.backup.updated_at) > 0
    )
    error_message = "The created backup returned incomplete state."
  }
}

run "planAgain_createdBackup_noChanges" {
  command   = plan
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-lifecycle"
  }
  # Any planned change leaves updated_at unknown, so a known value proves an
  # empty plan.
  assert {
    condition     = output.backup.id == run.createDbInstanceBackup_requiredOnly_becomesCompleted.backup.id && length(output.backup.updated_at) > 0
    error_message = "A second plan of the created backup was not empty."
  }
}

run "changeDescription_existingBackup_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-lifecycle"
    description    = "before upgrade"
  }
  assert {
    condition     = output.backup.id == run.createDbInstanceBackup_requiredOnly_becomesCompleted.backup.id && output.backup.description == "before upgrade"
    error_message = "Changing the description did not update the backup in place."
  }
}

run "omitDescription_existingBackup_valuePreserved" {
  command   = apply
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-lifecycle"
  }
  assert {
    condition     = output.backup.id == run.createDbInstanceBackup_requiredOnly_becomesCompleted.backup.id && output.backup.description == "before upgrade"
    error_message = "Omitting the description changed the backup."
  }
}

run "clearDescription_existingBackup_valueCleared" {
  command   = apply
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-lifecycle"
    description    = ""
  }
  assert {
    condition     = output.backup.id == run.createDbInstanceBackup_requiredOnly_becomesCompleted.backup.id && output.backup.description == ""
    error_message = "An empty description did not clear the backup description."
  }
}

run "changeName_existingBackup_replaced" {
  command   = apply
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-renamed"
  }
  assert {
    condition     = output.backup.id != run.createDbInstanceBackup_requiredOnly_becomesCompleted.backup.id && output.backup.name == "tf-test-db-backup-renamed" && output.backup.status == "COMPLETED"
    error_message = "Changing the name did not replace the backup."
  }
}

run "changeDbInstance_existingBackup_replaced" {
  command   = apply
  state_key = "lifecycle"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["replacement"]
    name           = "tf-test-db-backup-renamed"
  }
  assert {
    condition     = output.backup.id != run.changeName_existingBackup_replaced.backup.id && output.backup.db_instance.id == run.createDbInstances_backupSources_instancesActive.instance_ids["replacement"]
    error_message = "Changing db_instance.id did not replace the backup."
  }
}

run "createDbInstanceBackup_importChain_becomesCompleted" {
  command   = apply
  state_key = "import"
  module { source = "./fixtures/db_backup/resource" }
  variables {
    db_instance_id = run.createDbInstances_backupSources_instancesActive.instance_ids["primary"]
    name           = "tf-test-db-backup-import"
    description    = "import subject"
  }
  assert {
    condition     = output.backup.status == "COMPLETED"
    error_message = "The import-chain backup is not completed."
  }
}

run "planImport_existingBackup_refreshMatches" {
  command   = plan
  state_key = "import_plan"
  module { source = "./fixtures/db_backup/import" }
  variables {
    backup_id      = run.createDbInstanceBackup_importChain_becomesCompleted.backup.id
    db_instance_id = run.createDbInstanceBackup_importChain_becomesCompleted.backup.db_instance.id
    name           = run.createDbInstanceBackup_importChain_becomesCompleted.backup.name
  }
  assert {
    condition = (
      output.backup.id == run.createDbInstanceBackup_importChain_becomesCompleted.backup.id &&
      output.backup.description == "import subject" &&
      output.backup.status == "COMPLETED" &&
      output.backup.backup_timestamp == run.createDbInstanceBackup_importChain_becomesCompleted.backup.backup_timestamp &&
      length(output.backup.updated_at) > 0
    )
    error_message = "Import did not refresh the existing backup without changes."
  }
}

run "createDbInstanceAndBackup_keepBackupDefault_backupCompleted" {
  command   = apply
  state_key = "keep_backup"
  module { source = "./fixtures/db_backup/keep_backup" }
  variables {
    subnet_id     = run.createNetwork_dbBackupSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition     = output.backup.status == "COMPLETED" && output.backup.db_instance.id == output.instance_id
    error_message = "The kept backup was not created."
  }
}

run "destroyDbInstance_keepBackupTrue_backupRemains" {
  command   = apply
  state_key = "keep_backup"
  module { source = "./fixtures/db_backup/keep_backup" }
  variables {
    subnet_id     = run.createNetwork_dbBackupSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
    with_instance = false
    instance_id   = run.createDbInstanceAndBackup_keepBackupDefault_backupCompleted.instance_id
  }
  assert {
    condition     = output.backup.id == run.createDbInstanceAndBackup_keepBackupDefault_backupCompleted.backup.id
    error_message = "Destroying the DB instance replaced or removed its backup."
  }
}

run "refreshBackup_instanceDeleted_instanceIdPreserved" {
  command   = apply
  state_key = "keep_backup"
  module { source = "./fixtures/db_backup/keep_backup" }
  variables {
    subnet_id     = run.createNetwork_dbBackupSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
    with_instance = false
    instance_id   = run.createDbInstanceAndBackup_keepBackupDefault_backupCompleted.instance_id
  }
  assert {
    condition = (
      output.backup.id == run.createDbInstanceAndBackup_keepBackupDefault_backupCompleted.backup.id &&
      output.backup.db_instance.id == run.createDbInstanceAndBackup_keepBackupDefault_backupCompleted.instance_id &&
      output.backup.db_instance_name == null &&
      output.lookup.db_instance_id == null
    )
    error_message = "A backup of a deleted instance must keep db_instance.id in state and report a null instance."
  }
}
