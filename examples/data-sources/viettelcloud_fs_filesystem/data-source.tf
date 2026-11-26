# Look up a filesystem by ID.
data "viettelcloud_fs_filesystem" "by_id" {
  id = "00000000-0000-0000-0000-000000000001"
}

# Look up a filesystem by name and zone.
data "viettelcloud_fs_filesystem" "by_name" {
  name = "example-filesystem"
  zone = "zone-a"
}

# Look up a filesystem by subnet.
data "viettelcloud_fs_filesystem" "by_subnet" {
  subnet_id = "00000000-0000-0000-0000-000000000002"
}
