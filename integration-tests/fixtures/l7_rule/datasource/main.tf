# Fixture for tests/l7_rule_data_source.tftest.hcl. It creates one HEADER rule
# on the L7 policy that var.l7_policy_id names, then reads the rule back by its
# ID and by its ID in upper case.
#
# It creates no Load Balancer, listener, or L7 policy. The L7 policy must
# already exist on an HTTP listener of an ACTIVE Load Balancer.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "l7_policy_id" {
  type = string
}

resource "viettelcloud_lb_l7_rule" "subject" {
  l7_policy    = { id = var.l7_policy_id }
  type         = "HEADER"
  compare_type = "EQUAL_TO"
  key          = "X-Tf-Test-Ds"
  value        = "tf-test-ds"
  invert       = true
}

data "viettelcloud_lb_l7_rule" "by_id" {
  l7_policy_id = viettelcloud_lb_l7_rule.subject.l7_policy.id
  id           = viettelcloud_lb_l7_rule.subject.id
}

data "viettelcloud_lb_l7_rule" "by_upper_case_ids" {
  l7_policy_id = upper(viettelcloud_lb_l7_rule.subject.l7_policy.id)
  id           = upper(viettelcloud_lb_l7_rule.subject.id)
}
