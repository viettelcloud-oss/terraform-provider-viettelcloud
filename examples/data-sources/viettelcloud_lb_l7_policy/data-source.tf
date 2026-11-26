data "viettelcloud_lb_l7_policy" "by_id" {
  id = "00000000-0000-0000-0000-000000000001"
}

data "viettelcloud_lb_l7_policy" "by_name" {
  listener_id = "00000000-0000-0000-0000-000000000002"
  name        = "redirect-to-https"
}
