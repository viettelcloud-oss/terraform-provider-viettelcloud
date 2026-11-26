# Manages one viettelcloud_volume_backup_policy for every chain of
# tests/volume_backup_policy_resource.tftest.hcl. description and retention
# default to null, so a run that omits them leaves the attribute unconfigured.
# schedule is one object whose optional fields apply to the chosen
# execution_strategy.
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

variable "status" {
  type = string
}

variable "retention" {
  type    = number
  default = null
}

variable "schedule" {
  type = object({
    execution_strategy = string
    frequency          = optional(number)
    frequency_unit     = optional(string)
    first_execution    = optional(string)
    days_of_month      = optional(set(number))
    time               = optional(string)
  })
}

resource "viettelcloud_volume_backup_policy" "policy" {
  name        = var.name
  description = var.description
  region      = { name = var.region }
  status      = var.status
  retention   = var.retention
  schedule    = var.schedule
}

output "policy_id" {
  value = viettelcloud_volume_backup_policy.policy.id
}

output "policy_name" {
  value = viettelcloud_volume_backup_policy.policy.name
}

output "policy_description" {
  value = viettelcloud_volume_backup_policy.policy.description
}

output "policy_region" {
  value = viettelcloud_volume_backup_policy.policy.region.name
}

output "policy_status" {
  value = viettelcloud_volume_backup_policy.policy.status
}

output "policy_retention" {
  value = viettelcloud_volume_backup_policy.policy.retention
}

output "policy_schedule" {
  value = viettelcloud_volume_backup_policy.policy.schedule
}

output "policy_state" {
  value = viettelcloud_volume_backup_policy.policy.state
}

output "policy_volume_count" {
  value = viettelcloud_volume_backup_policy.policy.volume_count
}

output "policy_next_execution" {
  value = viettelcloud_volume_backup_policy.policy.next_execution
}

output "policy_last_execution" {
  value = viettelcloud_volume_backup_policy.policy.last_execution
}

output "policy_created_at" {
  value = viettelcloud_volume_backup_policy.policy.created_at
}

output "policy_updated_at" {
  value = viettelcloud_volume_backup_policy.policy.updated_at
}
