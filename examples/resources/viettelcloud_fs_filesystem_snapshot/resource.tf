variable "filesystem_id" {
  type = string
}

resource "viettelcloud_fs_filesystem_snapshot" "data" {
  name        = "example-data-snapshot"
  description = "Before the application upgrade"
  filesystem  = { id = var.filesystem_id }
}
