# Plan-only fixture for the import chain of
# tests/db_cluster_resource.tftest.hcl. It imports the DB cluster that run
# "createCluster_importChainFixture_clusterCreated" created, rather than
# creating one of its own.
#
# Never apply it. That cluster already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "cluster_id" {
  type = string
}

variable "subnet_id" {
  type = string
}

variable "name" {
  type = string
}

variable "engine" {
  type = string
}

variable "engine_version" {
  type = string
}

variable "topology" {
  type = string
}

variable "region" {
  type = string
}

variable "root_password" {
  type      = string
  sensitive = true
}

# The creating run's node_pools output. Only the attributes that configuration
# sets are read, so the Computed ones it also carries are dropped.
variable "node_pools" {
  type = list(object({
    name            = string
    replicas        = number
    disk_size       = number
    resource_config = object({ name = string })
    storage_class   = object({ name = string })
  }))
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_dbc_cluster.imported
  id = var.cluster_id
}

# Only the attributes whose omission would plan a change are set, fed from the
# state of the run that created this cluster. Everything Optional and Computed
# is left to the import refresh. root_password is write-only, so the refresh
# neither reads it nor plans a change for it.
resource "viettelcloud_dbc_cluster" "imported" {
  name           = var.name
  engine         = var.engine
  engine_version = { version = var.engine_version }
  region         = { name = var.region }
  subnet         = { id = var.subnet_id }
  topology       = var.topology
  root_password  = var.root_password

  node_pools = [for pool in var.node_pools : {
    name            = pool.name
    replicas        = pool.replicas
    disk_size       = pool.disk_size
    resource_config = pool.resource_config
    storage_class   = pool.storage_class
  }]
}

output "cluster_id" {
  value = viettelcloud_dbc_cluster.imported.id
}
