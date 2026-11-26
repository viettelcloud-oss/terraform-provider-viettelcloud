data "viettelcloud_subnet" "fs_subnet" {
  name = "my-subnet"
}

resource "viettelcloud_fs_filesystem" "example" {
  name     = "example-filesystem"
  size     = 100
  protocol = "NFS"

  filesystem_type = {
    name      = "Standard NFS"
    zone_name = "zone-a"
  }

  subnet = {
    id = data.viettelcloud_subnet.fs_subnet.id
  }
}
