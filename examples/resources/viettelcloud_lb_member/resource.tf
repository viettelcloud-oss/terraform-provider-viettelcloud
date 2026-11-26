resource "viettelcloud_lb_member" "web1" {
  server_group  = { id = "11111111-1111-1111-1111-111111111111" }
  name          = "web-server-1"
  address       = "192.168.1.10"
  protocol_port = 80
}

resource "viettelcloud_lb_member" "web_backup" {
  server_group  = { id = "11111111-1111-1111-1111-111111111111" }
  name          = "web-server-backup"
  address       = "192.168.1.11"
  protocol_port = 80
  weight        = 2
  backup        = true
}
