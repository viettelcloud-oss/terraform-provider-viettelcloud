# Lookup Load Balancer Listener by ID
data "viettelcloud_lb_listener" "by_id" {
  load_balancer_id = "00000000-0000-0000-0000-000000000000"
  id               = "22222222-2222-2222-2222-222222222222"
}

# Lookup Load Balancer Listener by name
data "viettelcloud_lb_listener" "by_name" {
  load_balancer_id = "00000000-0000-0000-0000-000000000000"
  name             = "http-listener"
}
