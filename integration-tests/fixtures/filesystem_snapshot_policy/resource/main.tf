# Manages one viettelcloud_fs_filesystem_snapshot_policy for the chains
# lifecycle, full, normalized, and drift of
# tests/filesystem_snapshot_policy_resource.tftest.hcl, and for the run that
# creates the policy of the import chain. description and retention default to
# null, so a run that omits them leaves the attribute unconfigured.
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
  type = list(string)
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

resource "viettelcloud_fs_filesystem_snapshot_policy" "policy" {
  name         = var.name
  description  = var.description
  region       = { name = var.region }
  days_of_week = var.days_of_week
  time         = var.time
  retention    = var.retention
  status       = var.status
}

output "policy_id" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.id
}

output "policy_name" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.name
}

output "policy_description" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.description
}

output "policy_region" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.region.name
}

output "policy_days_of_week" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.days_of_week
}

output "policy_time" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.time
}

output "policy_retention" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.retention
}

output "policy_status" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.status
}

output "policy_filesystem_count" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.filesystem_count
}

output "policy_state" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.state
}

output "policy_region_id" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.region_id
}

output "policy_region_name" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.region_name
}

output "policy_project_id" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.project_id
}

output "policy_owner_id" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.owner_id
}

output "policy_created_at" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.created_at
}

output "policy_updated_at" {
  value = viettelcloud_fs_filesystem_snapshot_policy.policy.updated_at
}
