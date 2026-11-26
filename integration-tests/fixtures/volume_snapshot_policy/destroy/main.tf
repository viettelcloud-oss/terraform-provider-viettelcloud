# Creates one volume snapshot policy, or none, for the destroy chain of
# tests/volume_snapshot_policy_resource.tftest.hcl. Setting enabled to false
# removes the policy from the configuration, so apply deletes it.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "enabled" {
  type = bool
}

variable "name" {
  type = string
}

variable "region" {
  type = string
}

resource "viettelcloud_volume_snapshot_policy" "policy" {
  count = var.enabled ? 1 : 0

  name   = var.name
  region = { name = var.region }
  status = "enabled"

  schedule = {
    days_of_week = ["mon"]
    time         = "01:00"
  }
}

output "policy_id" {
  value = one(viettelcloud_volume_snapshot_policy.policy[*].id)
}
