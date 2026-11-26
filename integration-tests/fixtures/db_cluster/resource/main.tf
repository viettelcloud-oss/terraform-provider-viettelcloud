# Creates a network and one DB cluster on it, then reads the cluster back
# through the data source. Each run chooses the engine, the node pools and the
# optional settings, so one module serves every engine of
# tests/db_cluster_resource.tftest.hcl. An optional variable defaults to null,
# which leaves its attribute unconfigured.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "region" {
  type = string
}

variable "network_name" {
  type = string
}

variable "vpc_cidr" {
  type = string
}

variable "subnet_cidr" {
  type = string
}

variable "name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

variable "keep_backup" {
  type    = bool
  default = null
}

variable "engine" {
  type = string
}

variable "engine_version" {
  type = string
}

variable "topology" {
  type    = string
  default = null
}

variable "root_password" {
  type      = string
  sensitive = true
}

variable "enable_auto_increase_volume_size" {
  type    = bool
  default = null
}

variable "allowed_cidrs" {
  type    = set(string)
  default = null
}

variable "enable_public_access" {
  type    = bool
  default = null
}

variable "enable_ssl" {
  type    = bool
  default = null
}

variable "enable_pitr_backup" {
  type    = bool
  default = null
}

variable "maintenance_config" {
  type = object({
    day_of_week = optional(string)
    start_hour  = optional(number)
  })
  default = null
}

variable "metadata" {
  type    = map(string)
  default = null
}

variable "node_pools" {
  type = list(object({
    name                  = string
    replicas              = number
    resource_config       = string
    disk_size             = optional(number)
    storage_class         = optional(string)
    partition_count       = optional(number)
    engine_roles          = optional(set(string))
    pool_config_attribute = optional(string)
  }))
}

variable "engine_config" {
  type = object({
    mode                  = optional(string)
    auth_enabled          = optional(bool)
    mcrouter_routing_type = optional(string)
  })
  default = null
}

variable "auto_backup_config" {
  type = object({
    enable_auto_backup = bool
    backup_interval    = string
    day_of_week        = optional(string)
    start_hour         = number
    max_auto_backup    = optional(number)
    retention_days     = optional(number)
  })
  default = null
}

resource "viettelcloud_vpc" "vpc" {
  name   = "${var.network_name}-vpc"
  cidr   = var.vpc_cidr
  region = { name = var.region }
}

resource "viettelcloud_subnet" "subnet" {
  name = "${var.network_name}-subnet"
  cidr = var.subnet_cidr
  vpc  = { id = viettelcloud_vpc.vpc.id }
}

resource "viettelcloud_dbc_cluster" "cluster" {
  name           = var.name
  description    = var.description
  keep_backup    = var.keep_backup
  engine         = var.engine
  engine_version = { version = var.engine_version }
  region         = { name = var.region }
  subnet         = { id = viettelcloud_subnet.subnet.id }
  topology       = var.topology
  root_password  = var.root_password

  allowed_cidrs                    = var.allowed_cidrs
  enable_public_access             = var.enable_public_access
  enable_ssl                       = var.enable_ssl
  enable_auto_increase_volume_size = var.enable_auto_increase_volume_size
  enable_pitr_backup               = var.enable_pitr_backup
  maintenance_config               = var.maintenance_config
  metadata                         = var.metadata
  auto_backup_config               = var.auto_backup_config
  engine_config                    = var.engine_config

  node_pools = [for pool in var.node_pools : {
    name            = pool.name
    replicas        = pool.replicas
    disk_size       = pool.disk_size
    resource_config = { name = pool.resource_config }
    storage_class   = pool.storage_class == null ? null : { name = pool.storage_class }
    partition_count = pool.partition_count
    engine_roles    = pool.engine_roles
    pool_config     = pool.pool_config_attribute == null ? null : { attribute = pool.pool_config_attribute }
  }]
}

data "viettelcloud_dbc_cluster" "cluster" {
  id = viettelcloud_dbc_cluster.cluster.id
}

output "cluster_id" {
  value = viettelcloud_dbc_cluster.cluster.id
}

output "subnet_id" {
  value = viettelcloud_subnet.subnet.id
}

output "node_pools" {
  value = viettelcloud_dbc_cluster.cluster.node_pools
}

output "name" {
  value = viettelcloud_dbc_cluster.cluster.name
}

output "engine" {
  value = viettelcloud_dbc_cluster.cluster.engine
}

output "engine_version" {
  value = viettelcloud_dbc_cluster.cluster.engine_version.version
}

output "topology" {
  value = viettelcloud_dbc_cluster.cluster.topology
}

output "region" {
  value = viettelcloud_dbc_cluster.cluster.region.name
}
