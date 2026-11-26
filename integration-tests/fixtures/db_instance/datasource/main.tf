# This fixture creates one DB instance and reads it by ID and by name.
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

resource "viettelcloud_db_instance" "subject" {
  name          = "tf-test-db-instance-datasource"
  description   = "lookup subject"
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

data "viettelcloud_db_instance" "by_id" { id = viettelcloud_db_instance.subject.id }
data "viettelcloud_db_instance" "by_name" { name = viettelcloud_db_instance.subject.name }
data "viettelcloud_db_instance" "by_upper_id" { id = upper(viettelcloud_db_instance.subject.id) }
data "viettelcloud_db_instance" "by_padded_name" { name = " ${viettelcloud_db_instance.subject.name} " }

# The instance object holds the write-only, sensitive root_password.
output "subject" {
  value     = viettelcloud_db_instance.subject
  sensitive = true
}
output "by_id" { value = data.viettelcloud_db_instance.by_id }
output "by_name" { value = data.viettelcloud_db_instance.by_name }
output "by_upper_id" { value = data.viettelcloud_db_instance.by_upper_id }
output "by_padded_name" { value = data.viettelcloud_db_instance.by_padded_name }
