# Test index — copy a run declaration below and Find Next to jump to its block.
#
# Chain lookup (shared fixture state; one isolated cluster):
#   run "createCluster_isolatedMongoReplicaSet_everyLookupReturnsTheCluster" {

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

variable "resource_package" {
  type    = string
  default = "2c4g"
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

# Creates an isolated cluster and looks it up by ID, by name with engine, by
# name with engine and region, and by name with engine and status. Each lookup
# must return the same cluster with
# the full state mapped. Invalid criteria, no match and ambiguity are covered
# by unit tests, because native expect_failures cannot match provider
# diagnostics.
run "createCluster_isolatedMongoReplicaSet_everyLookupReturnsTheCluster" {
  command = apply

  module {
    source = "./fixtures/db_cluster/datasource"
  }

  variables {
    region          = var.region
    network_name    = "tf-test-db-lookup"
    vpc_cidr        = "10.73.0.0/16"
    subnet_cidr     = "10.73.1.0/24"
    name            = "tf-test-db-lookup"
    description     = "Cluster for the data source test"
    engine_version  = var.mongodb_version
    resource_config = var.resource_package
    storage_class   = var.storage_class
    root_password   = var.db_root_password
  }

  assert {
    condition = (
      data.viettelcloud_dbc_cluster.by_id.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.by_name_and_engine.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.by_name_engine_and_region.id == viettelcloud_dbc_cluster.cluster.id &&
      data.viettelcloud_dbc_cluster.by_name_engine_and_status.id == viettelcloud_dbc_cluster.cluster.id
    )
    error_message = "A lookup returned a different cluster."
  }

  assert {
    condition = (
      data.viettelcloud_dbc_cluster.by_id.name == "tf-test-db-lookup" &&
      data.viettelcloud_dbc_cluster.by_id.description == "Cluster for the data source test" &&
      data.viettelcloud_dbc_cluster.by_id.engine == "mongodb" &&
      data.viettelcloud_dbc_cluster.by_id.topology == "mongo_rs" &&
      data.viettelcloud_dbc_cluster.by_id.engine_version == var.mongodb_version &&
      data.viettelcloud_dbc_cluster.by_id.region == var.region &&
      data.viettelcloud_dbc_cluster.by_id.status == "active" &&
      data.viettelcloud_dbc_cluster.by_id.subnet_id == viettelcloud_subnet.subnet.id &&
      data.viettelcloud_dbc_cluster.by_id.subnet_name == "tf-test-db-lookup-subnet" &&
      data.viettelcloud_dbc_cluster.by_id.vpc_name == "tf-test-db-lookup-vpc" &&
      length(data.viettelcloud_dbc_cluster.by_id.node_pools) == 1 &&
      data.viettelcloud_dbc_cluster.by_id.node_pools[0].name == "mongo" &&
      data.viettelcloud_dbc_cluster.by_id.node_pools[0].replicas == 3 &&
      data.viettelcloud_dbc_cluster.by_id.node_pools[0].disk_size == 100 &&
      data.viettelcloud_dbc_cluster.by_id.node_pools[0].resource_config == var.resource_package &&
      data.viettelcloud_dbc_cluster.by_id.node_pools[0].storage_class == var.storage_class &&
      length(data.viettelcloud_dbc_cluster.by_id.endpoint) > 0 &&
      length(data.viettelcloud_dbc_cluster.by_id.created_at) > 0
    )
    error_message = "The data source did not map the cluster."
  }

  assert {
    condition = (
      data.viettelcloud_dbc_cluster.by_name_and_engine.name == "tf-test-db-lookup" &&
      data.viettelcloud_dbc_cluster.by_name_and_engine.engine == "mongodb" &&
      data.viettelcloud_dbc_cluster.by_name_engine_and_region.region == var.region &&
      data.viettelcloud_dbc_cluster.by_name_engine_and_status.status == "active"
    )
    error_message = "The configured lookup values were not preserved."
  }
}
