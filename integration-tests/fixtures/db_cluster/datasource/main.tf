# Creates an isolated MongoDB replica set and looks it up every supported way.
# The data source tests own this cluster, so they run without the resource
# tests.

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
  type = string
}

variable "engine_version" {
  type = string
}

variable "resource_config" {
  type = string
}

variable "storage_class" {
  type = string
}

variable "root_password" {
  type      = string
  sensitive = true
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
  engine         = "mongodb"
  engine_version = { version = var.engine_version }
  region         = { name = var.region }
  subnet         = { id = viettelcloud_subnet.subnet.id }
  topology       = "mongo_rs"
  root_password  = var.root_password

  node_pools = [{
    name            = "mongo"
    replicas        = 3
    resource_config = { name = var.resource_config }
    storage_class   = { name = var.storage_class }
    disk_size       = 100
  }]
}

data "viettelcloud_dbc_cluster" "by_id" {
  id = viettelcloud_dbc_cluster.cluster.id
}

data "viettelcloud_dbc_cluster" "by_name_and_engine" {
  name       = viettelcloud_dbc_cluster.cluster.name
  engine     = "mongodb"
  depends_on = [viettelcloud_dbc_cluster.cluster]
}

data "viettelcloud_dbc_cluster" "by_name_engine_and_region" {
  name       = viettelcloud_dbc_cluster.cluster.name
  engine     = "mongodb"
  region     = var.region
  depends_on = [viettelcloud_dbc_cluster.cluster]
}

data "viettelcloud_dbc_cluster" "by_name_engine_and_status" {
  name       = viettelcloud_dbc_cluster.cluster.name
  engine     = "mongodb"
  status     = "active"
  depends_on = [viettelcloud_dbc_cluster.cluster]
}

output "cluster_id" {
  value = viettelcloud_dbc_cluster.cluster.id
}
