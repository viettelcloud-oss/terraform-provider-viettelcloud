# This fixture creates a DB instance with one manual backup. A run that sets
# with_instance = false destroys the instance and keeps the backup, which
# references the instance by the ID that the first run reported.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "region" { type = string }
variable "database_type" { type = string }
variable "db_version" { type = string }
variable "flavor" { type = string }
variable "volume_type" { type = string }
variable "solution" { type = string }
variable "subnet_id" { type = string }

variable "root_password" {
  type      = string
  sensitive = true
}

variable "with_instance" {
  type    = bool
  default = true
}

variable "instance_id" {
  type    = string
  default = null
}

resource "viettelcloud_db_instance" "source" {
  count         = var.with_instance ? 1 : 0
  name          = "tf-test-db-backup-keep-source"
  database_type = var.database_type
  db_version    = { version = var.db_version }
  region        = { name = var.region }
  flavor        = { name = var.flavor }
  volume_type   = { name = var.volume_type }
  disk_size     = 10
  solution      = var.solution
  subnet        = { id = var.subnet_id }
  root_password = var.root_password
}

resource "viettelcloud_db_backup" "kept" {
  name        = "tf-test-db-backup-kept"
  db_instance = { id = var.with_instance ? viettelcloud_db_instance.source[0].id : var.instance_id }
}

data "viettelcloud_db_backup" "kept" {
  id = viettelcloud_db_backup.kept.id
}

output "instance_id" {
  value = var.with_instance ? viettelcloud_db_instance.source[0].id : var.instance_id
}

output "backup" { value = viettelcloud_db_backup.kept }
output "lookup" { value = data.viettelcloud_db_backup.kept }
