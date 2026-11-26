# Plan-only fixture for the import chain of
# tests/l7_rule_resource.tftest.hcl. It imports the rule that run
# "createRule_importChainFixture_ruleCreated" created, rather than creating one
# of its own.
#
# Never apply it. That rule already belongs to state_key "import", so a second
# owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "l7_rule_id" {
  type = string
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

variable "invert" {
  type = bool
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_lb_l7_rule.imported
  id = "${var.l7_policy_id}/${var.l7_rule_id}"
}

# The required attributes and invert are fed from the state of the run that
# created this rule. invert is set because its provider default is false, so
# leaving it out would plan a change from the imported true. key stays unset
# because the PATH rule has none.
resource "viettelcloud_lb_l7_rule" "imported" {
  l7_policy    = { id = var.l7_policy_id }
  type         = var.type
  compare_type = var.compare_type
  value        = var.value
  invert       = var.invert
}

output "rule_id" {
  value = viettelcloud_lb_l7_rule.imported.id
}
