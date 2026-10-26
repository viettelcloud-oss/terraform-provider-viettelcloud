import {
  to = viettelcloud_server.imported
  id = "00000000-0000-0000-0000-000000000001"
}

resource "viettelcloud_server" "imported" {
  name   = "existing-server"
  zone   = { name = "vn-central-1a" }
  flavor = { kind = "predefined", name = "b1.micro2x" }
  boot = {
    boot_type   = "image"
    image       = { name = "ubuntu-22.04-publiccloud" }
    volume_type = { name = "Ceph_HDD_1A_10K" }
    volume_size = 30
  }

  data_volumes = [{
    kind                  = "existing"
    id                    = "00000000-0000-0000-0000-000000000002"
    delete_on_termination = false
  }]
}
