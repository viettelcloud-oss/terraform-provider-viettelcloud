resource "viettelcloud_vpc" "example" {
  name   = "server-example-vpc"
  cidr   = "10.0.0.0/16"
  region = { name = "vn-central-1" }
}

resource "viettelcloud_subnet" "example" {
  name = "server-example-subnet"
  cidr = "10.0.1.0/24"
  vpc  = { id = viettelcloud_vpc.example.id }
}

resource "viettelcloud_volume" "existing" {
  name = "independent-server-volume"
  size = 20
  zone = { name = "vn-central-1a" }
  create_from = {
    source_type = "empty"
    volume_type = { name = "Ceph_HDD_1A_10K" }
  }
}

resource "viettelcloud_server" "example" {
  name = "example-server"
  zone = { name = "vn-central-1a" }

  flavor = {
    kind   = "custom"
    family = "basic"
    vcpus  = 2
    ram    = 2
  }

  boot = {
    boot_type   = "image"
    image       = { os = "Ubuntu", version = "22.04" }
    volume_type = { disk_type = "ssd", speed = "10k", multiattach = false }
    volume_size = 30
  }

  private_ips = [{
    kind   = "subnet"
    subnet = { id = viettelcloud_subnet.example.id }
  }]

  data_volumes = [
    {
      kind        = "new"
      volume_type = { name = "Ceph_HDD_1A_10K" }
      volume_size = 20
    },
    {
      kind = "existing"
      id   = viettelcloud_volume.existing.id
    },
  ]
}
