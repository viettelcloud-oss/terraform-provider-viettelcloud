resource "viettelcloud_lb_server_group" "web_pool" {
  load_balancer = { id = "00000000-0000-0000-0000-000000000001" }
  name          = "web-pool"
  description   = "Web servers backend pool"
  protocol      = "HTTP"
  algorithm     = "ROUND_ROBIN"

  health_check = {
    monitor_type     = "HTTP"
    delay            = 10
    timeout          = 5
    max_retries      = 3
    max_retries_down = 3
    http_method      = "GET"
    path             = "/healthz"
    expected_codes   = "200,204"
  }
}

resource "viettelcloud_lb_server_group" "app_pool" {
  load_balancer = { id = "00000000-0000-0000-0000-000000000001" }
  name          = "app-pool"
  description   = "Application servers backend pool with cookie persistence"
  protocol      = "HTTP"
  algorithm     = "LEAST_CONNECTIONS"

  session_persistence = {
    type        = "APP_COOKIE"
    cookie_name = "SESSIONID"
  }

  health_check = {
    monitor_type     = "TCP"
    delay            = 15
    timeout          = 5
    max_retries      = 3
    max_retries_down = 3
  }
}
