# Plan-only fixture for the import chain of
# tests/security_group_rule_resource.tftest.hcl. It imports the rule that run
# "createSecurityGroupRule_importChainFixture_ruleCreated" created, rather than
# creating one of its own.
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

variable "security_group_rule_id" {
  type = string
}

variable "security_group_id" {
  type = string
}

variable "direction" {
  type = string
}

variable "protocol" {
  type = string
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_security_group_rule.imported
  id = var.security_group_rule_id
}

# Only the required attributes are set, fed from the state of the run that
# created this rule. description, port_range_min, port_range_max, and
# remote_ip_prefix are optional and computed, so leaving them unset keeps
# whatever the import refresh read. Every remaining attribute is computed.
resource "viettelcloud_security_group_rule" "imported" {
  security_group = { id = var.security_group_id }
  direction      = var.direction
  protocol       = var.protocol
}

output "rule_id" {
  value = viettelcloud_security_group_rule.imported.id
}
