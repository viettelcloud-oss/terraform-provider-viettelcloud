variable "volume_id" {
  type = string
}

resource "viettelcloud_volume_snapshot" "data" {
  name        = "example-data-snapshot"
  description = "Before the application upgrade"
  volume      = { id = var.volume_id }
}
