resource "viettelcloud_volume" "empty" {
  name        = "example-empty-volume"
  description = "Managed by Terraform"
  size        = 30
  iops        = 1000

  create_from = {
    source_type = "empty"
    volume_type = { name = "Ceph_HDD_1A_10K" }
  }
}

resource "viettelcloud_volume" "image" {
  name = "example-image-volume"
  size = 30

  create_from = {
    source_type = "image"
    image       = { os = "Ubuntu", version = "22.04" }
    volume_type = { name = "Ceph_HDD_1A_10K" }
  }
}

resource "viettelcloud_volume" "custom_image" {
  name = "example-custom-image-volume"
  size = 30

  create_from = {
    source_type  = "custom_image"
    custom_image = { id = "00000000-0000-0000-0000-000000000001" }
    volume_type  = { name = "Ceph_HDD_1A_10K" }
  }
}

resource "viettelcloud_volume" "snapshot" {
  name = "example-snapshot-volume"

  create_from = {
    source_type = "snapshot"
    snapshot    = { id = "00000000-0000-0000-0000-000000000002" }
  }
}

resource "viettelcloud_volume" "backup" {
  name = "example-backup-volume"
  size = 30

  create_from = {
    source_type = "backup"
    backup      = { id = "00000000-0000-0000-0000-000000000003" }
    volume_type = { name = "Ceph_HDD_1A_10K" }
  }
}
