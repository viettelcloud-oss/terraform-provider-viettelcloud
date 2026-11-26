# Fixture for every chain of tests/l7_rule_resource.tftest.hcl. It manages one
# L7 rule on the L7 policy that var.l7_policy_id names.
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

variable "type" {
  type = string
}

variable "compare_type" {
  type = string
}

variable "value" {
  type = string
}

variable "key" {
  type    = string
  default = null
}

variable "invert" {
  type    = bool
  default = null
}

resource "viettelcloud_lb_l7_rule" "rule" {
  l7_policy    = { id = var.l7_policy_id }
  type         = var.type
  compare_type = var.compare_type
  value        = var.value
  key          = var.key
  invert       = var.invert
}

output "rule_id" {
  value = viettelcloud_lb_l7_rule.rule.id
}

output "rule_l7_policy_id" {
  value = viettelcloud_lb_l7_rule.rule.l7_policy.id
}

output "rule_type" {
  value = viettelcloud_lb_l7_rule.rule.type
}

output "rule_compare_type" {
  value = viettelcloud_lb_l7_rule.rule.compare_type
}

output "rule_value" {
  value = viettelcloud_lb_l7_rule.rule.value
}

output "rule_key" {
  value = viettelcloud_lb_l7_rule.rule.key
}

output "rule_invert" {
  value = viettelcloud_lb_l7_rule.rule.invert
}

output "rule_provisioning_status" {
  value = viettelcloud_lb_l7_rule.rule.provisioning_status
}

output "rule_status" {
  value = viettelcloud_lb_l7_rule.rule.status
}

output "rule_created_at" {
  value = viettelcloud_lb_l7_rule.rule.created_at
}

output "rule_updated_at" {
  value = viettelcloud_lb_l7_rule.rule.updated_at
}
