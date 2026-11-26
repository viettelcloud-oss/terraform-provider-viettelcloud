# This fixture owns one backup for a lifecycle test chain. With
# restore_volume_id set, it restores the backup into that volume and reads the
# volume after the restore.
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
variable "restore_volume_id" {
  type    = string
  default = null
}
variable "timeouts" {
  type    = object({ create = optional(string), update = optional(string) })
  default = null
}

resource "viettelcloud_volume_backup" "backup" {
  volume      = { id = var.volume_id }
  name        = var.name
  description = var.description

  restore_volume = var.restore_volume_id == null ? null : { id = var.restore_volume_id }
  timeouts       = var.timeouts
}

# The read waits for the backup, so it shows the volume after the restore.
data "viettelcloud_volume" "restore_target" {
  count      = var.restore_volume_id == null ? 0 : 1
  id         = var.restore_volume_id
  depends_on = [viettelcloud_volume_backup.backup]
}

output "backup" {
  value = viettelcloud_volume_backup.backup
}

output "restore_target_status" {
  value = one(data.viettelcloud_volume.restore_target[*].status)
}
