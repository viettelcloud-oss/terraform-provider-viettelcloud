# Policies of one listener share its positions. Create them in position order
# with depends_on, because a policy created at a position can shift the
# policies after it.
resource "viettelcloud_lb_l7_policy" "redirect_prefix" {
  listener    = { id = "00000000-0000-0000-0000-000000000001" }
  name        = "redirect-to-https"
  description = "Redirect HTTP to HTTPS"
  action      = "REDIRECT_PREFIX"
  position    = 1

  redirect_prefix    = "https://example.com"
  redirect_http_code = 301
}

resource "viettelcloud_lb_l7_policy" "redirect_server_group" {
  listener    = { id = "00000000-0000-0000-0000-000000000001" }
  name        = "forward-to-app-pool"
  description = "Forward requests to app server group"
  action      = "REDIRECT_TO_SERVER_GROUP"
  position    = 2

  redirect_server_group = { id = "00000000-0000-0000-0000-000000000002" }

  depends_on = [viettelcloud_lb_l7_policy.redirect_prefix]
}

resource "viettelcloud_lb_l7_policy" "reject" {
  listener    = { id = "00000000-0000-0000-0000-000000000001" }
  name        = "block-traffic"
  description = "Reject matching requests"
  action      = "REJECT"
  position    = 3

  depends_on = [viettelcloud_lb_l7_policy.redirect_server_group]
}

resource "viettelcloud_lb_l7_policy" "redirect_url" {
  listener    = { id = "00000000-0000-0000-0000-000000000001" }
  name        = "redirect-to-external-url"
  description = "Redirect to maintenance page"
  action      = "REDIRECT_TO_URL"
  position    = 4

  redirect_url       = "https://maintenance.example.com"
  redirect_http_code = 302

  depends_on = [viettelcloud_lb_l7_policy.reject]
}
