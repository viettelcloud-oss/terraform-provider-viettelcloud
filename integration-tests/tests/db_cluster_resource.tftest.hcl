# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; each chain builds its own network.
#
# Chain mongodb (state_key = "mongodb"; a replica set whose lifecycle covers
# every in-place update, omit and clear, and the replacement of the subnet and
# every cluster-level attribute that mongo_rs accepts):
#   run "createCluster_mongoReplicaSetWithBackupSchedule_completeStateReturned" {
#   run "changeDescriptionAndKeepBackup_existingCluster_updatedInPlace" {
#   run "omitDescriptionAndKeepBackup_valuesSet_valuesPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "changeName_existingCluster_clusterReplaced" {
#   run "changeDiskSize_existingCluster_clusterReplaced" {
#   run "changeClusterAndPoolSettings_existingReplicaSet_clusterReplaced" {
#   run "omitReplaceIfConfiguredSettings_valuesSet_valuesPreservedWithoutReplacement" {
#
# Chain opensearch (state_key = "opensearch"; two client-named node pools):
#   run "createCluster_openSearchWithDataAndManagerPools_completeStateReturned" {
#   run "changeDescription_existingOpenSearchCluster_updatedInPlace" {
#   run "changeNodePools_existingOpenSearchCluster_clusterReplaced" {
#   run "renameAndAddNodePool_existingOpenSearchCluster_clusterReplaced" {
#
# Chain cassandra (state_key = "cassandra"; one pool with a daily backup
# schedule and SSL that the backend forces off):
#   run "createCluster_cassandraWithDailySchedule_completeStateReturned" {
#   run "changeKeepBackup_existingCassandraCluster_updatedInPlace" {
#   run "changeNodePoolAndAllowedCidrs_existingCassandraCluster_clusterReplaced" {
#
# Chain memcached (state_key = "memcached"; one pool without storage and the
# engine settings of the direct mode, then a switch of the engine to cassandra):
#   run "createCluster_memcachedDirectModeWithoutStorage_completeStateReturned" {
#   run "changeDescription_existingMemcachedCluster_updatedInPlace" {
#   run "changeNodePoolAndAuthEnabled_existingMemcachedCluster_clusterReplaced" {
#   run "changeEngine_existingMemcachedCluster_clusterReplaced" {
#
# Chain starrocks (state_key = "starrocks"; the fe and be pools, with SSL that
# the backend forces off and no backups):
#   run "createCluster_starRocksWithFeAndBePools_completeStateReturned" {
#   run "changeDescriptionAndKeepBackup_existingStarRocksCluster_updatedInPlace" {
#   run "changeBePool_existingStarRocksCluster_clusterReplaced" {
#
# Chain timescaledb (state_key = "timescaledb"; one pool with PITR and a
# schedule that keeps backups by days):
#   run "createCluster_timescaleDbWithPitrAndRetentionDays_completeStateReturned" {
#   run "changeDescription_existingTimescaleDbCluster_updatedInPlace" {
#   run "changeScheduleAndNodePool_existingTimescaleDbCluster_clusterReplaced" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# cluster this chain creates for itself):
#   run "createCluster_importChainFixture_clusterCreated" {
#   run "planImport_existingClusterId_completeStateRestored" {
#
# Creating a cluster takes many minutes, so the chains run in parallel and the
# suite needs quota for seven clusters at once. A replacement destroys the old
# cluster before it creates the new one, so it needs no extra quota.
#
# Each replacement run changes every attribute of its group in one apply and
# checks that id changed. Unit tests check the plan modifier of each attribute.
#
# These replacements are N/A here:
# - engine_version needs a second active version of the same engine.
# - zone needs a second zone, and region needs a second region and subnet.
# - topology and partition_count need a mongo_sharding cluster and more quota.
# - storage_class needs a second storage class in the catalog.
# - plugins needs an active OpenSearch plugin in the catalog.
# - enable_ssl on opensearch, cassandra and starrocks, because the backend
#   forces it.

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

variable "db_root_password" {
  description = "Administrator password for the test clusters. It must satisfy the backend password policy."
  type        = string
  sensitive   = true
}

# Catalog entries of the test project. Override them in terraform.tfvars when
# the project offers other names.
variable "region" {
  type    = string
  default = "vn-central-1"
}

variable "mongodb_version" {
  type    = string
  default = "7.0"
}

variable "opensearch_version" {
  type    = string
  default = "2.11"
}

variable "cassandra_version" {
  type    = string
  default = "4.1"
}

variable "memcached_version" {
  type    = string
  default = "1.6"
}

variable "starrocks_version" {
  type    = string
  default = "3.2"
}

variable "timescaledb_version" {
  type    = string
  default = "17"
}

variable "resource_package" {
  type    = string
  default = "2c4g"
}

variable "resource_package_large" {
  type    = string
  default = "4c8g"
}

variable "storage_class" {
  type    = string
  default = "ssd"
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Chain mongodb.

# Creates a MongoDB replica set with a weekly backup schedule and checks the
# resource and the data source. The cluster must be active and idle, and the
# write-only password must not reach the state.
run "createCluster_mongoReplicaSetWithBackupSchedule_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.1.0/24"
    name           = "tf-test-db-mongo-before"
    description    = "MongoDB before update"
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 2
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.operational_status == "noop" &&
      viettelcloud_dbc_cluster.cluster.engine == "mongodb" &&
      viettelcloud_dbc_cluster.cluster.topology == "mongo_rs" &&
      viettelcloud_dbc_cluster.cluster.name == "tf-test-db-mongo-before" &&
      viettelcloud_dbc_cluster.cluster.description == "MongoDB before update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == true &&
      viettelcloud_dbc_cluster.cluster.root_password == null &&
      length(viettelcloud_dbc_cluster.cluster.admin_username) > 0 &&
      length(viettelcloud_dbc_cluster.cluster.endpoint) > 0 &&
      viettelcloud_dbc_cluster.cluster.enable_auto_backup == true &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.backup_interval == "weekly" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.day_of_week == "sunday" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.max_auto_backup == 7 &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].name == "mongo" &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].disk_size == 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].actual_disk_size >= 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config.name == var.resource_package &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].storage_class.name == var.storage_class &&
      length(viettelcloud_dbc_cluster.cluster.created_at) > 0 &&
      data.viettelcloud_dbc_cluster.cluster.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.cluster.name == "tf-test-db-mongo-before" &&
      data.viettelcloud_dbc_cluster.cluster.engine == "mongodb" &&
      data.viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config == var.resource_package
    )
    error_message = "The MongoDB cluster was not created with the expected values."
  }
}

# Updates description and keep_backup with one PATCH, without replacing the
# cluster.
run "changeDescriptionAndKeepBackup_existingCluster_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.1.0/24"
    name           = "tf-test-db-mongo-before"
    description    = "MongoDB after update"
    keep_backup    = false
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 2
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_mongoReplicaSetWithBackupSchedule_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "MongoDB after update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == false &&
      data.viettelcloud_dbc_cluster.cluster.description == "MongoDB after update" &&
      data.viettelcloud_dbc_cluster.cluster.keep_backup == false
    )
    error_message = "The cluster was replaced, or its description and keep_backup were not updated in place."
  }
}

# Omitting description and keep_backup keeps the backend values and does not
# replace the cluster.
run "omitDescriptionAndKeepBackup_valuesSet_valuesPreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.1.0/24"
    name           = "tf-test-db-mongo-before"
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 2
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_mongoReplicaSetWithBackupSchedule_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "MongoDB after update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == false
    )
    error_message = "Omitting description and keep_backup changed the cluster."
  }
}

# An explicit empty description clears the backend value in place.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.1.0/24"
    name           = "tf-test-db-mongo-before"
    description    = ""
    keep_backup    = false
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 2
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_mongoReplicaSetWithBackupSchedule_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "" &&
      data.viettelcloud_dbc_cluster.cluster.description == ""
    )
    error_message = "The description was not cleared in place."
  }
}

# Changing the name replaces the cluster, because the name is not in the PATCH
# body.
run "changeName_existingCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.1.0/24"
    name           = "tf-test-db-mongo-after"
    description    = ""
    keep_backup    = false
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 2
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.createCluster_mongoReplicaSetWithBackupSchedule_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.name == "tf-test-db-mongo-after" &&
      viettelcloud_dbc_cluster.cluster.status == "active"
    )
    error_message = "The cluster was not replaced after changing its name."
  }
}

# Changing a node pool disk size replaces the cluster, because this resource
# does not resize in place.
run "changeDiskSize_existingCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.1.0/24"
    name           = "tf-test-db-mongo-after"
    description    = ""
    keep_backup    = false
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 150
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 2
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeName_existingCluster_clusterReplaced.cluster_id &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].disk_size == 150 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].actual_disk_size >= 150
    )
    error_message = "The cluster was not replaced after changing the disk size."
  }
}

# Changes, in one apply, the subnet, every cluster-level attribute that
# mongo_rs accepts, and the replicas and package of its pool. None of them is in
# the PATCH body, so the cluster is replaced. A new subnet CIDR replaces the
# subnet and so changes subnet.id. PITR needs the enabled weekly schedule.
run "changeClusterAndPoolSettings_existingReplicaSet_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region                           = var.region
    network_name                     = "tf-test-db-mongo"
    vpc_cidr                         = "10.70.0.0/16"
    subnet_cidr                      = "10.70.2.0/24"
    name                             = "tf-test-db-mongo-after"
    description                      = ""
    keep_backup                      = false
    engine                           = "mongodb"
    engine_version                   = var.mongodb_version
    topology                         = "mongo_rs"
    root_password                    = var.db_root_password
    allowed_cidrs                    = ["10.70.0.0/16"]
    enable_public_access             = true
    enable_ssl                       = true
    enable_auto_increase_volume_size = true
    enable_pitr_backup               = true
    maintenance_config               = { day_of_week = "saturday", start_hour = 3 }
    metadata                         = { owner = "terraform-test" }
    node_pools = [{
      name            = "mongo"
      replicas        = 1
      disk_size       = 150
      resource_config = var.resource_package_large
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "monday"
      start_hour         = 4
      max_auto_backup    = 8
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeDiskSize_existingCluster_clusterReplaced.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.subnet.id != run.changeDiskSize_existingCluster_clusterReplaced.subnet_id &&
      viettelcloud_dbc_cluster.cluster.allowed_cidrs == toset(["10.70.0.0/16"]) &&
      viettelcloud_dbc_cluster.cluster.enable_public_access == true &&
      viettelcloud_dbc_cluster.cluster.enable_ssl == true &&
      viettelcloud_dbc_cluster.cluster.enable_auto_increase_volume_size == true &&
      viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      viettelcloud_dbc_cluster.cluster.maintenance_config.day_of_week == "saturday" &&
      viettelcloud_dbc_cluster.cluster.maintenance_config.start_hour == 3 &&
      viettelcloud_dbc_cluster.cluster.metadata["owner"] == "terraform-test" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.day_of_week == "monday" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.start_hour == 4 &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.max_auto_backup == 8 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config.name == var.resource_package_large &&
      data.viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      data.viettelcloud_dbc_cluster.cluster.maintenance_config.day_of_week == "saturday"
    )
    error_message = "The cluster was not replaced, or it lacks the new cluster and pool settings."
  }
}

# Omitting the attributes that replace only when configured keeps their values
# in state and on the backend, and does not replace the cluster.
run "omitReplaceIfConfiguredSettings_valuesSet_valuesPreservedWithoutReplacement" {
  command   = apply
  parallel  = true
  state_key = "mongodb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-mongo"
    vpc_cidr       = "10.70.0.0/16"
    subnet_cidr    = "10.70.2.0/24"
    name           = "tf-test-db-mongo-after"
    description    = ""
    keep_backup    = false
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 1
      disk_size       = 150
      resource_config = var.resource_package_large
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "monday"
      start_hour         = 4
      max_auto_backup    = 8
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.changeClusterAndPoolSettings_existingReplicaSet_clusterReplaced.cluster_id &&
      viettelcloud_dbc_cluster.cluster.allowed_cidrs == toset(["10.70.0.0/16"]) &&
      viettelcloud_dbc_cluster.cluster.enable_public_access == true &&
      viettelcloud_dbc_cluster.cluster.enable_ssl == true &&
      viettelcloud_dbc_cluster.cluster.enable_auto_increase_volume_size == true &&
      viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      viettelcloud_dbc_cluster.cluster.maintenance_config.day_of_week == "saturday" &&
      viettelcloud_dbc_cluster.cluster.maintenance_config.start_hour == 3 &&
      viettelcloud_dbc_cluster.cluster.metadata["owner"] == "terraform-test" &&
      data.viettelcloud_dbc_cluster.cluster.allowed_cidrs == toset(["10.70.0.0/16"]) &&
      data.viettelcloud_dbc_cluster.cluster.enable_public_access == true &&
      data.viettelcloud_dbc_cluster.cluster.enable_ssl == true &&
      data.viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      data.viettelcloud_dbc_cluster.cluster.metadata["owner"] == "terraform-test"
    )
    error_message = "Omitting the settings that replace only when configured replaced the cluster or changed their values."
  }
}


# Chain opensearch.

# Creates an OpenSearch cluster with two pools. The backend forces SSL on and
# keep_backup off. It reports pools sorted by name, so the resource must keep
# the configured order (manager, data), while the data source shows the backend
# order (data, manager).
run "createCluster_openSearchWithDataAndManagerPools_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "opensearch"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-search"
    vpc_cidr       = "10.71.0.0/16"
    subnet_cidr    = "10.71.1.0/24"
    name           = "tf-test-db-search"
    description    = "OpenSearch before update"
    engine         = "opensearch"
    engine_version = var.opensearch_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "manager"
        replicas        = 3
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
        engine_roles    = ["cluster_manager"]
      },
      {
        name                  = "data"
        replicas              = 2
        disk_size             = 50
        resource_config       = var.resource_package_large
        storage_class         = var.storage_class
        engine_roles          = ["data", "ingest"]
        pool_config_attribute = "hot"
      },
    ]
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.engine == "opensearch" &&
      viettelcloud_dbc_cluster.cluster.topology == "opensearch" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == false &&
      viettelcloud_dbc_cluster.cluster.enable_ssl == true &&
      viettelcloud_dbc_cluster.cluster.root_password == null &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 2 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].name == "manager" &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 3 &&
      contains(viettelcloud_dbc_cluster.cluster.node_pools[0].engine_roles, "cluster_manager") &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].name == "data" &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].replicas == 2 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].pool_config.attribute == "hot" &&
      contains(viettelcloud_dbc_cluster.cluster.node_pools[1].engine_roles, "ingest") &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].resource_config.name == var.resource_package_large &&
      data.viettelcloud_dbc_cluster.cluster.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.cluster.node_pools[0].name == "data" &&
      data.viettelcloud_dbc_cluster.cluster.node_pools[1].name == "manager"
    )
    error_message = "The OpenSearch cluster was not created with the expected values, or its pools lost their configured order."
  }
}

# Updates the description without replacing the cluster.
run "changeDescription_existingOpenSearchCluster_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "opensearch"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-search"
    vpc_cidr       = "10.71.0.0/16"
    subnet_cidr    = "10.71.1.0/24"
    name           = "tf-test-db-search"
    description    = "OpenSearch after update"
    engine         = "opensearch"
    engine_version = var.opensearch_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "manager"
        replicas        = 3
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
        engine_roles    = ["cluster_manager"]
      },
      {
        name                  = "data"
        replicas              = 2
        disk_size             = 50
        resource_config       = var.resource_package_large
        storage_class         = var.storage_class
        engine_roles          = ["data", "ingest"]
        pool_config_attribute = "hot"
      },
    ]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_openSearchWithDataAndManagerPools_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "OpenSearch after update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == false
    )
    error_message = "The OpenSearch cluster was replaced, or its description was not updated in place."
  }
}

# Changes the replicas, package, engine roles and storage tier of the data
# pool in one apply. Pools do not change in place, so the cluster is replaced.
run "changeNodePools_existingOpenSearchCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "opensearch"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-search"
    vpc_cidr       = "10.71.0.0/16"
    subnet_cidr    = "10.71.1.0/24"
    name           = "tf-test-db-search"
    description    = "OpenSearch after update"
    engine         = "opensearch"
    engine_version = var.opensearch_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "manager"
        replicas        = 3
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
        engine_roles    = ["cluster_manager"]
      },
      {
        name                  = "data"
        replicas              = 1
        disk_size             = 50
        resource_config       = var.resource_package
        storage_class         = var.storage_class
        engine_roles          = ["data"]
        pool_config_attribute = "warm"
      },
    ]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeDescription_existingOpenSearchCluster_updatedInPlace.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].name == "data" &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].replicas == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].resource_config.name == var.resource_package &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].engine_roles == toset(["data"]) &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].pool_config.attribute == "warm"
    )
    error_message = "The OpenSearch cluster was not replaced, or its data pool lacks the new settings."
  }
}

# Renames the data pool and adds an ingest pool, leaving every other pool
# setting unchanged. A pool name or a pool count change replaces the cluster.
run "renameAndAddNodePool_existingOpenSearchCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "opensearch"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-search"
    vpc_cidr       = "10.71.0.0/16"
    subnet_cidr    = "10.71.1.0/24"
    name           = "tf-test-db-search"
    description    = "OpenSearch after update"
    engine         = "opensearch"
    engine_version = var.opensearch_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "manager"
        replicas        = 3
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
        engine_roles    = ["cluster_manager"]
      },
      {
        name                  = "warm-data"
        replicas              = 1
        disk_size             = 50
        resource_config       = var.resource_package
        storage_class         = var.storage_class
        engine_roles          = ["data"]
        pool_config_attribute = "warm"
      },
      {
        name            = "ingest"
        replicas        = 1
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
        engine_roles    = ["ingest"]
      },
    ]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeNodePools_existingOpenSearchCluster_clusterReplaced.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].name == "warm-data" &&
      viettelcloud_dbc_cluster.cluster.node_pools[2].name == "ingest" &&
      viettelcloud_dbc_cluster.cluster.node_pools[2].engine_roles == toset(["ingest"])
    )
    error_message = "The OpenSearch cluster was not replaced, or its pools lack the new names."
  }
}


# Chain cassandra.

# Creates a Cassandra cluster with a daily backup schedule. The backend forces
# SSL off, and Cassandra has no engine settings, so engine_config stays null.
run "createCluster_cassandraWithDailySchedule_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "cassandra"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-cassandra"
    vpc_cidr       = "10.74.0.0/16"
    subnet_cidr    = "10.74.1.0/24"
    name           = "tf-test-db-cassandra"
    engine         = "cassandra"
    engine_version = var.cassandra_version
    root_password  = var.db_root_password
    node_pools = [{
      name            = "cassandra"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "daily"
      start_hour         = 3
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.operational_status == "noop" &&
      viettelcloud_dbc_cluster.cluster.engine == "cassandra" &&
      viettelcloud_dbc_cluster.cluster.topology == "cassandra" &&
      viettelcloud_dbc_cluster.cluster.enable_ssl == false &&
      viettelcloud_dbc_cluster.cluster.keep_backup == true &&
      viettelcloud_dbc_cluster.cluster.engine_config == null &&
      viettelcloud_dbc_cluster.cluster.root_password == null &&
      viettelcloud_dbc_cluster.cluster.enable_auto_backup == true &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.backup_interval == "daily" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.day_of_week == null &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.max_auto_backup == 7 &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].name == "cassandra" &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].disk_size == 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].storage_class.name == var.storage_class &&
      data.viettelcloud_dbc_cluster.cluster.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.cluster.engine == "cassandra"
    )
    error_message = "The Cassandra cluster was not created with the expected values."
  }
}

# Updates keep_backup without replacing the cluster.
run "changeKeepBackup_existingCassandraCluster_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "cassandra"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-cassandra"
    vpc_cidr       = "10.74.0.0/16"
    subnet_cidr    = "10.74.1.0/24"
    name           = "tf-test-db-cassandra"
    keep_backup    = false
    engine         = "cassandra"
    engine_version = var.cassandra_version
    root_password  = var.db_root_password
    node_pools = [{
      name            = "cassandra"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "daily"
      start_hour         = 3
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_cassandraWithDailySchedule_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.keep_backup == false
    )
    error_message = "The Cassandra cluster was replaced, or keep_backup was not updated in place."
  }
}

# Changes the replicas and package of the pool and the allowed CIDRs in one
# apply. None of them changes in place, so the cluster is replaced.
run "changeNodePoolAndAllowedCidrs_existingCassandraCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "cassandra"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-cassandra"
    vpc_cidr       = "10.74.0.0/16"
    subnet_cidr    = "10.74.1.0/24"
    name           = "tf-test-db-cassandra"
    keep_backup    = false
    engine         = "cassandra"
    engine_version = var.cassandra_version
    root_password  = var.db_root_password
    allowed_cidrs  = ["10.74.0.0/16"]
    node_pools = [{
      name            = "cassandra"
      replicas        = 1
      disk_size       = 100
      resource_config = var.resource_package_large
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "daily"
      start_hour         = 3
      max_auto_backup    = 7
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeKeepBackup_existingCassandraCluster_updatedInPlace.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.allowed_cidrs == toset(["10.74.0.0/16"]) &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config.name == var.resource_package_large
    )
    error_message = "The Cassandra cluster was not replaced, or it lacks the new pool and CIDR settings."
  }
}

# Chain memcached.

# Creates a Memcached cluster in direct mode. Its pool has no storage, so the
# pool sets neither storage_class nor disk_size, and both stay null in state.
run "createCluster_memcachedDirectModeWithoutStorage_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "memcached"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-memcached"
    vpc_cidr       = "10.75.0.0/16"
    subnet_cidr    = "10.75.1.0/24"
    name           = "tf-test-db-memcached"
    description    = "Memcached before update"
    engine         = "memcached"
    engine_version = var.memcached_version
    root_password  = var.db_root_password
    engine_config  = { mode = "direct" }
    node_pools = [{
      name            = "memcached"
      replicas        = 2
      resource_config = var.resource_package
    }]
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.operational_status == "noop" &&
      viettelcloud_dbc_cluster.cluster.engine == "memcached" &&
      viettelcloud_dbc_cluster.cluster.topology == "memcached" &&
      viettelcloud_dbc_cluster.cluster.engine_config.mode == "direct" &&
      viettelcloud_dbc_cluster.cluster.engine_config.auth_enabled == false &&
      viettelcloud_dbc_cluster.cluster.engine_config.mcrouter_routing_type == null &&
      viettelcloud_dbc_cluster.cluster.enable_auto_backup == false &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config == null &&
      viettelcloud_dbc_cluster.cluster.root_password == null &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].name == "memcached" &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 2 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].storage_class == null &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].disk_size == null &&
      data.viettelcloud_dbc_cluster.cluster.engine_config.mode == "direct" &&
      data.viettelcloud_dbc_cluster.cluster.node_pools[0].storage_class == null
    )
    error_message = "The Memcached cluster was not created with the expected values, or its pool reported storage."
  }
}

# Updates the description without replacing the cluster. The configuration
# omits engine_config, so the settings in state must stay as they are.
run "changeDescription_existingMemcachedCluster_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "memcached"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-memcached"
    vpc_cidr       = "10.75.0.0/16"
    subnet_cidr    = "10.75.1.0/24"
    name           = "tf-test-db-memcached"
    description    = "Memcached after update"
    engine         = "memcached"
    engine_version = var.memcached_version
    root_password  = var.db_root_password
    node_pools = [{
      name            = "memcached"
      replicas        = 2
      resource_config = var.resource_package
    }]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_memcachedDirectModeWithoutStorage_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "Memcached after update" &&
      viettelcloud_dbc_cluster.cluster.engine_config.mode == "direct"
    )
    error_message = "The Memcached cluster was replaced, or its description or engine settings changed."
  }
}

# Changes the replicas and package of the pool and enables authentication in
# one apply. None of them changes in place, so the cluster is replaced.
run "changeNodePoolAndAuthEnabled_existingMemcachedCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "memcached"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-memcached"
    vpc_cidr       = "10.75.0.0/16"
    subnet_cidr    = "10.75.1.0/24"
    name           = "tf-test-db-memcached"
    description    = "Memcached after update"
    engine         = "memcached"
    engine_version = var.memcached_version
    root_password  = var.db_root_password
    engine_config  = { mode = "direct", auth_enabled = true }
    node_pools = [{
      name            = "memcached"
      replicas        = 1
      resource_config = var.resource_package_large
    }]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeDescription_existingMemcachedCluster_updatedInPlace.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.engine_config.mode == "direct" &&
      viettelcloud_dbc_cluster.cluster.engine_config.auth_enabled == true &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config.name == var.resource_package_large
    )
    error_message = "The Memcached cluster was not replaced, or it lacks the new pool and auth settings."
  }
}

# Changes the engine from memcached to cassandra, with the cassandra version
# and its one pool with storage. The engine is not in the PATCH body, so the
# cluster is replaced.
run "changeEngine_existingMemcachedCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "memcached"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-memcached"
    vpc_cidr       = "10.75.0.0/16"
    subnet_cidr    = "10.75.1.0/24"
    name           = "tf-test-db-memcached"
    description    = "Memcached after update"
    engine         = "cassandra"
    engine_version = var.cassandra_version
    root_password  = var.db_root_password
    node_pools = [{
      name            = "cassandra"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeNodePoolAndAuthEnabled_existingMemcachedCluster_clusterReplaced.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.engine == "cassandra"
    )
    error_message = "The cluster was not replaced after changing its engine to cassandra."
  }
}

# Chain starrocks.

# Creates a StarRocks cluster with the fe and be pools. The backend forces SSL
# off, and StarRocks has no backups and no engine settings, so the schedule and
# engine_config stay null.
run "createCluster_starRocksWithFeAndBePools_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "starrocks"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-starrocks"
    vpc_cidr       = "10.76.0.0/16"
    subnet_cidr    = "10.76.1.0/24"
    name           = "tf-test-db-starrocks"
    description    = "StarRocks before update"
    engine         = "starrocks"
    engine_version = var.starrocks_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "fe"
        replicas        = 1
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
      },
      {
        name            = "be"
        replicas        = 3
        disk_size       = 100
        resource_config = var.resource_package
        storage_class   = var.storage_class
      },
    ]
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.operational_status == "noop" &&
      viettelcloud_dbc_cluster.cluster.engine == "starrocks" &&
      viettelcloud_dbc_cluster.cluster.topology == "starrocks" &&
      viettelcloud_dbc_cluster.cluster.description == "StarRocks before update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == true &&
      viettelcloud_dbc_cluster.cluster.enable_ssl == false &&
      viettelcloud_dbc_cluster.cluster.enable_pitr_backup == false &&
      viettelcloud_dbc_cluster.cluster.enable_auto_backup == false &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config == null &&
      viettelcloud_dbc_cluster.cluster.engine_config == null &&
      viettelcloud_dbc_cluster.cluster.root_password == null &&
      length(viettelcloud_dbc_cluster.cluster.admin_username) > 0 &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 2 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].name == "fe" &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].disk_size == 50 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].storage_class.name == var.storage_class &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].name == "be" &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].replicas == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].node_count == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].disk_size == 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].actual_disk_size >= 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].resource_config.name == var.resource_package &&
      data.viettelcloud_dbc_cluster.cluster.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.cluster.engine == "starrocks" &&
      data.viettelcloud_dbc_cluster.cluster.enable_ssl == false &&
      length(data.viettelcloud_dbc_cluster.cluster.node_pools) == 2
    )
    error_message = "The StarRocks cluster was not created with the expected values."
  }
}

# Updates description and keep_backup with one PATCH, without replacing the
# cluster.
run "changeDescriptionAndKeepBackup_existingStarRocksCluster_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "starrocks"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-starrocks"
    vpc_cidr       = "10.76.0.0/16"
    subnet_cidr    = "10.76.1.0/24"
    name           = "tf-test-db-starrocks"
    description    = "StarRocks after update"
    keep_backup    = false
    engine         = "starrocks"
    engine_version = var.starrocks_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "fe"
        replicas        = 1
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
      },
      {
        name            = "be"
        replicas        = 3
        disk_size       = 100
        resource_config = var.resource_package
        storage_class   = var.storage_class
      },
    ]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_starRocksWithFeAndBePools_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "StarRocks after update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == false &&
      data.viettelcloud_dbc_cluster.cluster.description == "StarRocks after update" &&
      data.viettelcloud_dbc_cluster.cluster.keep_backup == false
    )
    error_message = "The StarRocks cluster was replaced, or its description and keep_backup were not updated in place."
  }
}

# Changes the replicas and package of the be pool in one apply. Pools do not
# change in place, so the cluster is replaced.
run "changeBePool_existingStarRocksCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "starrocks"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-starrocks"
    vpc_cidr       = "10.76.0.0/16"
    subnet_cidr    = "10.76.1.0/24"
    name           = "tf-test-db-starrocks"
    description    = "StarRocks after update"
    keep_backup    = false
    engine         = "starrocks"
    engine_version = var.starrocks_version
    root_password  = var.db_root_password
    node_pools = [
      {
        name            = "fe"
        replicas        = 1
        disk_size       = 50
        resource_config = var.resource_package
        storage_class   = var.storage_class
      },
      {
        name            = "be"
        replicas        = 5
        disk_size       = 100
        resource_config = var.resource_package_large
        storage_class   = var.storage_class
      },
    ]
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeDescriptionAndKeepBackup_existingStarRocksCluster_updatedInPlace.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.enable_ssl == false &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].name == "be" &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].replicas == 5 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].node_count == 5 &&
      viettelcloud_dbc_cluster.cluster.node_pools[1].resource_config.name == var.resource_package_large
    )
    error_message = "The StarRocks cluster was not replaced, or its be pool lacks the new settings."
  }
}

# Chain timescaledb.

# Creates a TimescaleDB cluster with PITR and a daily schedule that keeps
# backups for 8 days. TimescaleDB keeps backups by age, so max_auto_backup
# stays null. The backend rejects public access, so it stays false.
run "createCluster_timescaleDbWithPitrAndRetentionDays_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "timescaledb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region             = var.region
    network_name       = "tf-test-db-timescale"
    vpc_cidr           = "10.77.0.0/16"
    subnet_cidr        = "10.77.1.0/24"
    name               = "tf-test-db-timescale"
    description        = "TimescaleDB before update"
    engine             = "timescaledb"
    engine_version     = var.timescaledb_version
    root_password      = var.db_root_password
    enable_pitr_backup = true
    node_pools = [{
      name            = "timescale"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "daily"
      start_hour         = 2
      retention_days     = 8
    }
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.operational_status == "noop" &&
      viettelcloud_dbc_cluster.cluster.engine == "timescaledb" &&
      viettelcloud_dbc_cluster.cluster.topology == "timescaledb" &&
      viettelcloud_dbc_cluster.cluster.description == "TimescaleDB before update" &&
      viettelcloud_dbc_cluster.cluster.keep_backup == true &&
      viettelcloud_dbc_cluster.cluster.enable_public_access == false &&
      viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      viettelcloud_dbc_cluster.cluster.engine_config == null &&
      viettelcloud_dbc_cluster.cluster.root_password == null &&
      length(viettelcloud_dbc_cluster.cluster.admin_username) > 0 &&
      viettelcloud_dbc_cluster.cluster.enable_auto_backup == true &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.backup_interval == "daily" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.day_of_week == null &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.start_hour == 2 &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.retention_days == 8 &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.max_auto_backup == null &&
      length(viettelcloud_dbc_cluster.cluster.node_pools) == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].name == "timescale" &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 3 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].disk_size == 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].actual_disk_size >= 100 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config.name == var.resource_package &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].storage_class.name == var.storage_class &&
      data.viettelcloud_dbc_cluster.cluster.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.cluster.engine == "timescaledb" &&
      data.viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      data.viettelcloud_dbc_cluster.cluster.auto_backup_config.retention_days == 8 &&
      data.viettelcloud_dbc_cluster.cluster.auto_backup_config.max_auto_backup == null
    )
    error_message = "The TimescaleDB cluster was not created with the expected values."
  }
}

# Updates the description without replacing the cluster.
run "changeDescription_existingTimescaleDbCluster_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "timescaledb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region             = var.region
    network_name       = "tf-test-db-timescale"
    vpc_cidr           = "10.77.0.0/16"
    subnet_cidr        = "10.77.1.0/24"
    name               = "tf-test-db-timescale"
    description        = "TimescaleDB after update"
    engine             = "timescaledb"
    engine_version     = var.timescaledb_version
    root_password      = var.db_root_password
    enable_pitr_backup = true
    node_pools = [{
      name            = "timescale"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "daily"
      start_hour         = 2
      retention_days     = 8
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id == run.createCluster_timescaleDbWithPitrAndRetentionDays_completeStateReturned.cluster_id &&
      viettelcloud_dbc_cluster.cluster.description == "TimescaleDB after update" &&
      data.viettelcloud_dbc_cluster.cluster.description == "TimescaleDB after update"
    )
    error_message = "The TimescaleDB cluster was replaced, or its description was not updated in place."
  }
}

# Changes the schedule to weekly with 14 days of retention, and the replicas
# and package of the pool, in one apply. None of them changes in place, so the
# cluster is replaced. PITR still needs the enabled schedule.
run "changeScheduleAndNodePool_existingTimescaleDbCluster_clusterReplaced" {
  command   = apply
  parallel  = true
  state_key = "timescaledb"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region             = var.region
    network_name       = "tf-test-db-timescale"
    vpc_cidr           = "10.77.0.0/16"
    subnet_cidr        = "10.77.1.0/24"
    name               = "tf-test-db-timescale"
    description        = "TimescaleDB after update"
    engine             = "timescaledb"
    engine_version     = var.timescaledb_version
    root_password      = var.db_root_password
    enable_pitr_backup = true
    node_pools = [{
      name            = "timescale"
      replicas        = 1
      disk_size       = 100
      resource_config = var.resource_package_large
      storage_class   = var.storage_class
    }]
    auto_backup_config = {
      enable_auto_backup = true
      backup_interval    = "weekly"
      day_of_week        = "sunday"
      start_hour         = 3
      retention_days     = 14
    }
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.cluster.id != run.changeDescription_existingTimescaleDbCluster_updatedInPlace.cluster_id &&
      viettelcloud_dbc_cluster.cluster.status == "active" &&
      viettelcloud_dbc_cluster.cluster.enable_pitr_backup == true &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.backup_interval == "weekly" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.day_of_week == "sunday" &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.start_hour == 3 &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.retention_days == 14 &&
      viettelcloud_dbc_cluster.cluster.auto_backup_config.max_auto_backup == null &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].replicas == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].node_count == 1 &&
      viettelcloud_dbc_cluster.cluster.node_pools[0].resource_config.name == var.resource_package_large &&
      data.viettelcloud_dbc_cluster.cluster.auto_backup_config.retention_days == 14
    )
    error_message = "The TimescaleDB cluster was not replaced, or it lacks the new schedule and pool settings."
  }
}

# Chain import.

# The import chain creates the cluster it later imports, so that no other chain
# owns it. The import fixture leaves description and every other Optional and
# Computed attribute unconfigured, so those values can only come from the
# import refresh.
# Creates the cluster that the plan-only import run imports.
run "createCluster_importChainFixture_clusterCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/db_cluster/resource"
  }

  variables {
    region         = var.region
    network_name   = "tf-test-db-import"
    vpc_cidr       = "10.72.0.0/16"
    subnet_cidr    = "10.72.1.0/24"
    name           = "tf-test-db-import"
    engine         = "mongodb"
    engine_version = var.mongodb_version
    topology       = "mongo_rs"
    root_password  = var.db_root_password
    node_pools = [{
      name            = "mongo"
      replicas        = 3
      disk_size       = 100
      resource_config = var.resource_package
      storage_class   = var.storage_class
    }]
  }

  assert {
    condition = (
      length(viettelcloud_dbc_cluster.cluster.id) > 0 &&
      viettelcloud_dbc_cluster.cluster.status == "active"
    )
    error_message = "The import chain's cluster was not created."
  }
}

# Plans an import of that cluster by ID, and checks the attributes that the
# refresh alone supplies. The run never applies, so it leaves the cluster to
# the creating chain, which deletes it at teardown. The write-only password
# plans no change.
run "planImport_existingClusterId_completeStateRestored" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/db_cluster/import"
  }

  variables {
    cluster_id     = run.createCluster_importChainFixture_clusterCreated.cluster_id
    subnet_id      = run.createCluster_importChainFixture_clusterCreated.subnet_id
    node_pools     = run.createCluster_importChainFixture_clusterCreated.node_pools
    region         = run.createCluster_importChainFixture_clusterCreated.region
    name           = run.createCluster_importChainFixture_clusterCreated.name
    engine         = run.createCluster_importChainFixture_clusterCreated.engine
    engine_version = run.createCluster_importChainFixture_clusterCreated.engine_version
    topology       = run.createCluster_importChainFixture_clusterCreated.topology
    root_password  = var.db_root_password
  }

  assert {
    condition = (
      viettelcloud_dbc_cluster.imported.id == run.createCluster_importChainFixture_clusterCreated.cluster_id &&
      viettelcloud_dbc_cluster.imported.status == "active" &&
      viettelcloud_dbc_cluster.imported.description == "" &&
      viettelcloud_dbc_cluster.imported.keep_backup == true &&
      viettelcloud_dbc_cluster.imported.enable_ssl == false &&
      length(viettelcloud_dbc_cluster.imported.admin_username) > 0 &&
      viettelcloud_dbc_cluster.imported.node_pools[0].actual_disk_size >= 100
    )
    error_message = "The imported cluster state is incomplete."
  }
}
