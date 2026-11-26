data "viettelcloud_lb_server_group" "by_id" {
  load_balancer_id = "00000000-0000-0000-0000-000000000001"
  id               = "00000000-0000-0000-0000-000000000002"
}

data "viettelcloud_lb_server_group" "by_name" {
  load_balancer_id = "00000000-0000-0000-0000-000000000001"
  name             = "web-pool"
}
