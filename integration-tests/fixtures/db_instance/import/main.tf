# Plan-only fixture. The import chain creates this DB instance in its own state.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "instance_id" { type = string }
variable "name" { type = string }
variable "region" { type = string }
variable "database_type" { type = string }
variable "db_version" { type = string }
variable "flavor" { type = string }
variable "volume_type" { type = string }
variable "disk_size" { type = number }
variable "solution" { type = string }
variable "subnet_id" { type = string }

variable "root_password" {
  type      = string
  sensitive = true
}

import {
  to = viettelcloud_db_instance.imported
  id = var.instance_id
}

resource "viettelcloud_db_instance" "imported" {
  name          = var.name
  database_type = var.database_type
  db_version    = { version = var.db_version }
  region        = { name = var.region }
  flavor        = { name = var.flavor }
  volume_type   = { name = var.volume_type }
  disk_size     = var.disk_size
  solution      = var.solution
  subnet        = { id = var.subnet_id }
  root_password = var.root_password
}

# The instance object holds the write-only, sensitive root_password.
output "instance" {
  value     = viettelcloud_db_instance.imported
  sensitive = true
}
