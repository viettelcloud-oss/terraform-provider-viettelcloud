# This fixture creates one DB instance with one manual backup and reads the
# backup by ID and by every supported filter.
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
  name          = "tf-test-db-backup-lookup-source"
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

resource "viettelcloud_db_backup" "subject" {
  name        = "tf-test-db-backup-lookup"
  description = "lookup subject"
  db_instance = { id = viettelcloud_db_instance.source.id }
}

data "viettelcloud_db_backup" "by_id" { id = viettelcloud_db_backup.subject.id }
data "viettelcloud_db_backup" "unique" {
  name           = viettelcloud_db_backup.subject.name
  db_instance_id = viettelcloud_db_backup.subject.db_instance.id
  backup_type    = "manual"
}
data "viettelcloud_db_backup" "combined" {
  name           = " ${viettelcloud_db_backup.subject.name} "
  db_instance_id = upper(viettelcloud_db_backup.subject.db_instance.id)
  backup_type    = "manual"
  status         = viettelcloud_db_backup.subject.status
  database_type  = viettelcloud_db_backup.subject.database_type
  region         = viettelcloud_db_backup.subject.region
}

output "subject" { value = viettelcloud_db_backup.subject }
output "by_id" { value = data.viettelcloud_db_backup.by_id }
output "unique" { value = data.viettelcloud_db_backup.unique }
output "combined" { value = data.viettelcloud_db_backup.combined }
