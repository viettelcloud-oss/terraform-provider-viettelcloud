resource "viettelcloud_lb_l7_rule" "path_rule" {
  l7_policy    = { id = "00000000-0000-0000-0000-000000000000" }
  type         = "PATH"
  compare_type = "STARTS_WITH"
  value        = "/api"
}

resource "viettelcloud_lb_l7_rule" "header_rule" {
  l7_policy    = { id = "00000000-0000-0000-0000-000000000000" }
  type         = "HEADER"
  compare_type = "EQUAL_TO"
  key          = "X-Custom-Header"
  value        = "my-value"
  invert       = false
}
