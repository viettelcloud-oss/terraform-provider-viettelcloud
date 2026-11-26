# This fixture creates the two source DB instances of the backup lifecycle
# tests. The second one is the target of the db_instance replacement.
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

resource "viettelcloud_db_instance" "source" {
  for_each      = toset(["primary", "replacement"])
  name          = "tf-test-db-backup-source-${each.key}"
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

output "instance_ids" {
  value = { for key, instance in viettelcloud_db_instance.source : key => instance.id }
}
