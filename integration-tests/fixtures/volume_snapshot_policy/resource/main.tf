# Manages one viettelcloud_volume_snapshot_policy for the chains required,
# optional, update, and replace of tests/volume_snapshot_policy_resource.tftest.hcl,
# and for the run that creates the policy of the import chain. description and
# retention default to null, so a run that omits them leaves the attribute
# unconfigured.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

variable "region" {
  type = string
}

variable "days_of_week" {
  type = set(string)
}

variable "time" {
  type = string
}

variable "retention" {
  type    = number
  default = null
}

variable "status" {
  type = string
}

resource "viettelcloud_volume_snapshot_policy" "policy" {
  name        = var.name
  description = var.description
  region      = { name = var.region }
  status      = var.status
  retention   = var.retention

  schedule = {
    days_of_week = var.days_of_week
    time         = var.time
  }
}

output "policy_id" {
  value = viettelcloud_volume_snapshot_policy.policy.id
}

output "policy_name" {
  value = viettelcloud_volume_snapshot_policy.policy.name
}

output "policy_description" {
  value = viettelcloud_volume_snapshot_policy.policy.description
}

output "policy_region" {
  value = viettelcloud_volume_snapshot_policy.policy.region.name
}

output "policy_status" {
  value = viettelcloud_volume_snapshot_policy.policy.status
}

output "policy_retention" {
  value = viettelcloud_volume_snapshot_policy.policy.retention
}

output "policy_schedule_days_of_week" {
  value = viettelcloud_volume_snapshot_policy.policy.schedule.days_of_week
}

output "policy_schedule_time" {
  value = viettelcloud_volume_snapshot_policy.policy.schedule.time
}

output "policy_state" {
  value = viettelcloud_volume_snapshot_policy.policy.state
}

output "policy_volume_count" {
  value = viettelcloud_volume_snapshot_policy.policy.volume_count
}

output "policy_last_execution" {
  value = viettelcloud_volume_snapshot_policy.policy.last_execution
}

output "policy_created_at" {
  value = viettelcloud_volume_snapshot_policy.policy.created_at
}

output "policy_updated_at" {
  value = viettelcloud_volume_snapshot_policy.policy.updated_at
}
