variable "volume_id" {
  type = string
}

resource "viettelcloud_volume_backup" "example" {
  name        = "example-volume-backup"
  description = "Daily recovery point"
  volume      = { id = var.volume_id }
}
