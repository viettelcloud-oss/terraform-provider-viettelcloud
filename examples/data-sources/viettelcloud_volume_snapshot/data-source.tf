variable "volume_id" {
  type = string
}

data "viettelcloud_volume_snapshot" "data" {
  name      = "example-data-snapshot"
  volume_id = var.volume_id
}
