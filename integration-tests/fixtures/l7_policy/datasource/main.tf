# Fixture for tests/l7_policy_data_source.tftest.hcl. It creates one
# REDIRECT_PREFIX policy on the existing listener listener_id, then reads it
# back through every supported lookup criterion and combination.
#
# It does not own the listener or its Load Balancer.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "listener_id" {
  type = string
}

resource "viettelcloud_lb_l7_policy" "subject" {
  listener           = { id = var.listener_id }
  name               = "tf-test-l7-policy-ds"
  description        = "Data source subject"
  action             = "REDIRECT_PREFIX"
  redirect_prefix    = "https://example.com"
  redirect_http_code = 301
}

# The data sources wait for the subject through their references or
# depends_on, so they read it at apply instead of at plan.
data "viettelcloud_lb_l7_policy" "by_id" {
  id = viettelcloud_lb_l7_policy.subject.id
}

data "viettelcloud_lb_l7_policy" "by_upper_case_id" {
  id = upper(viettelcloud_lb_l7_policy.subject.id)
}

data "viettelcloud_lb_l7_policy" "by_name" {
  name = "tf-test-l7-policy-ds"

  depends_on = [viettelcloud_lb_l7_policy.subject]
}

data "viettelcloud_lb_l7_policy" "by_untrimmed_name" {
  name = "  tf-test-l7-policy-ds  "

  depends_on = [viettelcloud_lb_l7_policy.subject]
}

data "viettelcloud_lb_l7_policy" "by_name_and_listener" {
  name        = "tf-test-l7-policy-ds"
  listener_id = viettelcloud_lb_l7_policy.subject.listener.id

  depends_on = [viettelcloud_lb_l7_policy.subject]
}

data "viettelcloud_lb_l7_policy" "by_id_and_listener" {
  id          = viettelcloud_lb_l7_policy.subject.id
  listener_id = upper(var.listener_id)
}

data "viettelcloud_lb_l7_policy" "by_id_and_name" {
  id   = viettelcloud_lb_l7_policy.subject.id
  name = "tf-test-l7-policy-ds"
}
