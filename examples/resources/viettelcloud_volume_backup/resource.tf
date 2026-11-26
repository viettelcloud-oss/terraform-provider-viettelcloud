variable "volume_id" {
  type = string
}

# Set after the backup exists to restore it into that volume. The restore
# overwrites all data on the volume.
variable "restore_volume_id" {
  type    = string
  default = null
}

resource "viettelcloud_volume_backup" "example" {
  name        = "example-volume-backup"
  description = "Daily recovery point"
  volume      = { id = var.volume_id }

  restore_volume = var.restore_volume_id == null ? null : { id = var.restore_volume_id }
}
