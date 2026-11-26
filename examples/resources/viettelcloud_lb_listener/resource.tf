resource "viettelcloud_lb_listener" "http" {
  load_balancer = { id = "00000000-0000-0000-0000-000000000000" }
  name          = "http-listener"
  description   = "HTTP listener on port 80"
  protocol      = "HTTP"
  protocol_port = 80

  # Remove server_group to detach the server group from the listener.
  server_group = { id = "22222222-2222-2222-2222-222222222222" }
}

resource "viettelcloud_lb_listener" "https" {
  load_balancer       = { id = "00000000-0000-0000-0000-000000000000" }
  name                = "https-listener"
  description         = "HTTPS listener on port 443 with TLS"
  protocol            = "TERMINATED_HTTPS"
  protocol_port       = 443
  default_tls_cert    = { id = "11111111-1111-1111-1111-111111111111" }
  sni_tls_certs       = [{ id = "11111111-1111-1111-1111-111111111111" }]
  enable_h2           = true
  set_x_forwarded_for = true
}
