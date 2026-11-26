variable "filesystem_id" {
  type = string
}

data "viettelcloud_fs_filesystem_snapshot" "data" {
  name          = "example-data-snapshot"
  filesystem_id = var.filesystem_id
}
