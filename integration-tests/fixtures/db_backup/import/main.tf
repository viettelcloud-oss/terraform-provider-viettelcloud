# Plan-only fixture. The import chain creates this backup in its own state.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "backup_id" { type = string }
variable "db_instance_id" { type = string }
variable "name" { type = string }

import {
  to = viettelcloud_db_backup.imported
  id = var.backup_id
}

resource "viettelcloud_db_backup" "imported" {
  db_instance = { id = var.db_instance_id }
  name        = var.name
}

output "backup" {
  value = viettelcloud_db_backup.imported
}
