# This fixture owns one backup for a lifecycle test chain.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "volume_id" { type = string }
variable "name" { type = string }
variable "description" {
  type    = string
  default = null
}

resource "viettelcloud_volume_backup" "backup" {
  volume      = { id = var.volume_id }
  name        = var.name
  description = var.description
}

output "backup" {
  value = viettelcloud_volume_backup.backup
}
