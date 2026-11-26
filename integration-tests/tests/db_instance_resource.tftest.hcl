# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Each DB instance takes minutes to create, so the chains share one network
# and the lifecycle chain replaces its instance only for the attributes whose
# replacement it has to prove. Set db_root_password, for example as
# TF_VAR_db_root_password, and the catalog values in the variables block below.
#
# Chain network (shared setup; state_key = "network"):
#   run "createNetwork_dbInstanceSuite_subnetReturned" {
#
# Chain lifecycle (state_key = "lifecycle"):
#   run "createDbInstance_requiredOnly_becomesActive" {
#   run "planAgain_createdDbInstance_noChanges" {
#   run "changeBackupPolicy_existingDbInstance_updatedInPlace" {
#   run "changeBackupIntervalToDaily_dayOfWeekOmitted_updatedInPlace" {
#   run "omitOptionalFields_existingDbInstance_valuesPreserved" {
#   run "disableBackupsAndClearDescription_existingDbInstance_updatedInPlace" {
#   run "changeName_existingDbInstance_replaced" {
#   run "changeDiskSize_existingDbInstance_replaced" {
#   run "changeFlavor_otherCatalogEntry_replaced" {
#   run "changeSubnet_otherSubnet_replaced" {
#   run "enablePublicAccess_configuredTrue_replaced" {
#
# Chain import (state_key = "import" -> "import_plan"; plan-only import):
#   run "createDbInstance_descriptionAndBackupPolicySet_becomesActive" {
#   run "planImport_existingDbInstance_refreshMatches" {

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
  region             = "vn-central-1"
  database_type      = "postgresql"
  db_version         = "16"
  flavor             = "m1.small"
  replacement_flavor = "m1.medium"
  volume_type        = "ssd"
  solution           = "basic"
}

run "createNetwork_dbInstanceSuite_subnetReturned" {
  command   = apply
  state_key = "network"
  parallel  = true
  module { source = "./fixtures/db_instance/network" }
  variables {
    name                    = "tf-test-db-instance-network"
    cidr                    = "10.41.0.0/16"
    with_replacement_subnet = true
  }
  assert {
    condition     = length(output.subnet_id) > 0 && length(output.replacement_subnet_id) > 0
    error_message = "The shared subnets were not created."
  }
}

run "createDbInstance_requiredOnly_becomesActive" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-lifecycle"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      length(output.instance.id) > 0 &&
      output.instance.status == "ACTIVE" &&
      output.instance.operational_status == "NOOP" &&
      output.instance.name == "tf-test-db-instance-lifecycle" &&
      output.instance.description == "" &&
      output.instance.database_type == var.database_type &&
      output.instance.db_version.version == var.db_version && length(output.instance.db_version.id) > 0 &&
      output.instance.flavor.name == var.flavor && length(output.instance.flavor.id) > 0 &&
      output.instance.volume_type.name == var.volume_type && length(output.instance.volume_type.id) > 0 &&
      output.instance.region.name == var.region && length(output.instance.region_id) > 0 &&
      output.instance.flavor_vcpus > 0 && output.instance.flavor_ram > 0 &&
      length(output.instance.volume_type_disk_type) > 0 &&
      output.instance.disk_size == 10 && output.instance.current_disk_size >= 10 &&
      output.instance.solution == var.solution &&
      output.instance.subnet.id == run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id &&
      output.instance.subnet_name == "tf-test-db-instance-network" &&
      output.instance.vpc_id == run.createNetwork_dbInstanceSuite_subnetReturned.vpc.id &&
      output.instance.root_password == null &&
      output.instance.enable_public_access == false &&
      output.instance.keep_backup == true &&
      output.instance.backup_policy.enabled == false &&
      output.instance.auto_increase_volume_size.enabled == false &&
      length(output.instance.maintenance_config.day_of_week) > 0 &&
      output.instance.port > 0 &&
      output.instance.project_id != null &&
      length(output.instance.created_at) > 0 && length(output.instance.updated_at) > 0
    )
    error_message = "The created DB instance returned incomplete state."
  }
}

run "planAgain_createdDbInstance_noChanges" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-lifecycle"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  # Any planned change leaves updated_at unknown, so a known value proves an
  # empty plan.
  assert {
    condition     = output.instance.id == run.createDbInstance_requiredOnly_becomesActive.instance.id && length(output.instance.updated_at) > 0
    error_message = "A second plan of the created DB instance was not empty."
  }
}

run "changeBackupPolicy_existingDbInstance_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-lifecycle"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
    description   = "updated"
    keep_backup   = false
    backup_policy = {
      enabled         = true
      backup_interval = "weekly"
      day_of_week     = "monday"
      start_hour      = 2
      max_auto_backup = 7
    }
    auto_increase_volume_size = { enabled = true, threshold = 80, limit = 100 }
  }
  assert {
    condition = (
      output.instance.id == run.createDbInstance_requiredOnly_becomesActive.instance.id &&
      output.instance.description == "updated" &&
      output.instance.keep_backup == false &&
      output.instance.backup_policy.enabled == true &&
      output.instance.backup_policy.backup_interval == "weekly" &&
      output.instance.backup_policy.day_of_week == "monday" &&
      output.instance.backup_policy.start_hour == 2 &&
      output.instance.backup_policy.max_auto_backup == 7 &&
      output.instance.auto_increase_volume_size.enabled == true &&
      output.instance.auto_increase_volume_size.threshold == 80 &&
      output.instance.auto_increase_volume_size.limit == 100
    )
    error_message = "The mutable fields were not updated in place."
  }
}

run "changeBackupIntervalToDaily_dayOfWeekOmitted_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-lifecycle"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
    backup_policy = {
      enabled         = true
      backup_interval = "daily"
      start_hour      = 3
      max_auto_backup = 7
    }
  }
  assert {
    condition = (
      output.instance.id == run.createDbInstance_requiredOnly_becomesActive.instance.id &&
      output.instance.backup_policy.backup_interval == "daily" &&
      output.instance.backup_policy.start_hour == 3 &&
      output.instance.description == "updated" &&
      output.instance.keep_backup == false &&
      output.instance.auto_increase_volume_size.enabled == true
    )
    error_message = "Changing to a daily interval did not update the policy in place or changed omitted fields."
  }
}

run "omitOptionalFields_existingDbInstance_valuesPreserved" {
  command   = plan
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-lifecycle"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      length(output.instance.updated_at) > 0 &&
      output.instance.description == "updated" &&
      output.instance.keep_backup == false &&
      output.instance.backup_policy.backup_interval == "daily" &&
      output.instance.auto_increase_volume_size.limit == 100
    )
    error_message = "Omitting the optional fields planned a change."
  }
}

run "disableBackupsAndClearDescription_existingDbInstance_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name                      = "tf-test-db-instance-lifecycle"
    disk_size                 = 10
    subnet_id                 = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password             = var.db_root_password
    description               = ""
    backup_policy             = { enabled = false }
    auto_increase_volume_size = { enabled = false }
  }
  assert {
    condition = (
      output.instance.id == run.createDbInstance_requiredOnly_becomesActive.instance.id &&
      output.instance.description == "" &&
      output.instance.backup_policy.enabled == false &&
      output.instance.auto_increase_volume_size.enabled == false
    )
    error_message = "Disabling the policies or clearing the description failed."
  }
}

run "changeName_existingDbInstance_replaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-renamed"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition     = output.instance.id != run.createDbInstance_requiredOnly_becomesActive.instance.id && output.instance.name == "tf-test-db-instance-renamed" && output.instance.keep_backup == true
    error_message = "Changing name did not replace the DB instance."
  }
}

run "changeDiskSize_existingDbInstance_replaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-renamed"
    disk_size     = 20
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition     = output.instance.id != run.changeName_existingDbInstance_replaced.instance.id && output.instance.disk_size == 20 && output.instance.current_disk_size >= 20
    error_message = "Changing disk_size did not replace the DB instance."
  }
}

run "changeFlavor_otherCatalogEntry_replaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-renamed"
    disk_size     = 20
    flavor        = var.replacement_flavor
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      output.instance.id != run.changeDiskSize_existingDbInstance_replaced.instance.id &&
      output.instance.flavor.name == var.replacement_flavor &&
      output.instance.flavor.id != run.changeDiskSize_existingDbInstance_replaced.instance.flavor.id
    )
    error_message = "Selecting another flavor did not replace the DB instance."
  }
}

run "changeSubnet_otherSubnet_replaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-renamed"
    disk_size     = 20
    flavor        = var.replacement_flavor
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.replacement_subnet_id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      output.instance.id != run.changeFlavor_otherCatalogEntry_replaced.instance.id &&
      output.instance.subnet.id == run.createNetwork_dbInstanceSuite_subnetReturned.replacement_subnet_id &&
      output.instance.subnet_name == "tf-test-db-instance-network-replacement"
    )
    error_message = "Selecting another subnet did not replace the DB instance."
  }
}

run "enablePublicAccess_configuredTrue_replaced" {
  command   = apply
  state_key = "lifecycle"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name                 = "tf-test-db-instance-renamed"
    disk_size            = 20
    flavor               = var.replacement_flavor
    subnet_id            = run.createNetwork_dbInstanceSuite_subnetReturned.replacement_subnet_id
    root_password        = var.db_root_password
    enable_public_access = true
  }
  assert {
    condition = (
      output.instance.id != run.changeSubnet_otherSubnet_replaced.instance.id &&
      output.instance.enable_public_access == true
    )
    error_message = "Enabling public access did not replace the DB instance."
  }
}

run "createDbInstance_descriptionAndBackupPolicySet_becomesActive" {
  command   = apply
  state_key = "import"
  parallel  = true
  module { source = "./fixtures/db_instance/resource" }
  variables {
    name          = "tf-test-db-instance-import"
    disk_size     = 10
    subnet_id     = run.createNetwork_dbInstanceSuite_subnetReturned.subnet_id
    root_password = var.db_root_password
    description   = "import subject"
    keep_backup   = true
    backup_policy = {
      enabled         = true
      backup_interval = "daily"
      start_hour      = 1
      max_auto_backup = 7
    }
    auto_increase_volume_size = { enabled = false }
  }
  assert {
    condition = (
      output.instance.status == "ACTIVE" &&
      output.instance.description == "import subject" &&
      output.instance.keep_backup == true &&
      output.instance.backup_policy.enabled == true &&
      output.instance.backup_policy.backup_interval == "daily" &&
      output.instance.backup_policy.start_hour == 1 &&
      output.instance.auto_increase_volume_size.enabled == false
    )
    error_message = "The optional create values do not match the backend."
  }
}

run "planImport_existingDbInstance_refreshMatches" {
  command   = plan
  state_key = "import_plan"
  parallel  = true
  module { source = "./fixtures/db_instance/import" }
  variables {
    instance_id   = run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.id
    name          = run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.name
    disk_size     = run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.current_disk_size
    subnet_id     = run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.subnet.id
    root_password = var.db_root_password
  }
  assert {
    condition = (
      output.instance.id == run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.id &&
      output.instance.description == "import subject" &&
      output.instance.backup_policy.backup_interval == "daily" &&
      output.instance.db_version.id == run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.db_version.id &&
      output.instance.flavor.id == run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.flavor.id &&
      output.instance.region_id == run.createDbInstance_descriptionAndBackupPolicySet_becomesActive.instance.region_id &&
      length(output.instance.updated_at) > 0
    )
    error_message = "Import did not refresh the existing DB instance without changes."
  }
}
