# This fixture owns one manual backup for a lifecycle test chain. It keeps the
# old backup until its replacement completes, as the example recommends.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "db_instance_id" { type = string }
variable "name" { type = string }
variable "description" {
  type    = string
  default = null
}

resource "viettelcloud_db_backup" "backup" {
  db_instance = { id = var.db_instance_id }
  name        = var.name
  description = var.description

  lifecycle {
    create_before_destroy = true
  }
}

output "backup" {
  value = viettelcloud_db_backup.backup
}
